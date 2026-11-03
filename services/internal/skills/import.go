package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type staged struct {
	Bundles     []Bundle          `json:"bundles"`
	Expected    map[string]int64  `json:"expected"`
	ExpectedIDs map[string]string `json:"expected_ids"`
	SourceURL   string            `json:"source_url"`
	Digests     map[string]string `json:"digests"`
}

func (s *Service) Preview(ctx context.Context, p identity.Principal, filename string, data []byte, source, id string, version int64) (Preview, error) {
	// Check authority before downloading; release database locks during network I/O.
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return Preview{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Preview{}, err
	}
	if source != "" {
		data, source, err = s.fetch(ctx, source)
		if err != nil {
			return Preview{}, err
		}
		u, sourceErr := sourceURL(source)
		if sourceErr != nil {
			return Preview{}, sourceErr
		}
		filename = pathBase(u.Path)
		if u.Hostname() == "github.com" || u.Hostname() == "skills.sh" || u.Hostname() == "clawhub.ai" || u.Hostname() == "skillhub.cn" || u.Hostname() == "api.skillhub.cn" {
			filename = "skills.zip"
			if bytes.HasPrefix(data, []byte("---")) {
				filename = "SKILL.md"
			}
		}
	}
	bundles, err := parseImport(filename, data)
	if err != nil {
		return Preview{}, err
	}
	tx, err = s.begin(ctx, p, true)
	if err != nil {
		return Preview{}, err
	}
	defer tx.Rollback(context.Background())
	stage := staged{Bundles: []Bundle{}, Expected: map[string]int64{}, ExpectedIDs: map[string]string{}, SourceURL: source, Digests: map[string]string{}}
	out := Preview{Items: []PreviewItem{}, SourceURL: source}
	if id != "" {
		// A repository can contain several skills. Updating one must never
		// stage its neighbours or apply a rename to a different local record.
		current, err := load(ctx, tx, p, id)
		if err != nil {
			return out, err
		}
		if current.Version != version {
			return out, conflict()
		}
		var selected []Bundle
		for _, bundle := range bundles {
			meta, _ := validateBundle(bundle)
			if meta.Name == current.Name {
				selected = append(selected, bundle)
			}
		}
		if len(selected) != 1 {
			return out, invalid("来源中找不到原技能，名称可能已变化，请重新通过链接导入")
		}
		bundles = selected
	}
	for _, b := range bundles {
		b, err = setPolicy(b, false, false)
		if err != nil {
			return out, err
		}
		meta, _ := validateBundle(b)
		current, err := find(ctx, tx, p, meta.Name)
		if err != nil {
			return out, err
		}
		if id != "" && (current == nil || current.ID != id) {
			return out, invalid("远端 Skill 名称已变化，请通过链接另存为副本")
		}
		row := PreviewItem{Name: meta.Name, Description: meta.Description, Files: filesView(b), ChangedFiles: fileNames(b)}
		stage.Expected[meta.Name] = 0
		stage.Digests[meta.Name] = bundleDigest(b)
		if current != nil {
			if id != "" && current.Version != version {
				return out, conflict()
			}
			stage.Expected[meta.Name] = current.Version
			stage.ExpectedIDs[meta.Name] = current.ID
			row.Conflict = true
			old, err := s.open(p, *current)
			if err != nil {
				return out, err
			}
			normalized, err := setPolicy(old, false, false)
			if err != nil {
				return out, err
			}
			row.LocalChanged = current.SourceURL != "" && current.SourceDigest != bundleDigest(normalized)
			row.ChangedFiles = changedFiles(normalized, b)
			if id != "" {
				b, err = setPolicy(b, current.ModelInvocable, current.UserInvocable)
				if err != nil {
					return out, err
				}
				out.Unchanged = len(row.ChangedFiles) == 0
			}
		}
		stage.Bundles = append(stage.Bundles, b)
		out.Items = append(out.Items, row)
	}
	if id != "" && out.Unchanged {
		return out, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM skill_import_previews WHERE expires_at<=clock_timestamp()`); err != nil {
		return out, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM skill_import_previews WHERE org_id=$1 AND owner_id=$2`, p.OrganizationID, p.UserID).Scan(&count); err != nil {
		return out, err
	}
	if count >= 4 {
		_, err = tx.Exec(ctx, `DELETE FROM skill_import_previews WHERE id=(SELECT id FROM skill_import_previews WHERE org_id=$1 AND owner_id=$2 ORDER BY expires_at LIMIT 1)`, p.OrganizationID, p.UserID)
		if err != nil {
			return out, err
		}
	}
	out.Token = webapi.ID()
	envelope, err := s.seal(p, out.Token, "skill-import", stage)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO skill_import_previews(id,org_id,owner_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6)`, out.Token, p.OrganizationID, p.UserID, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func pathBase(value string) string { parts := strings.Split(value, "/"); return parts[len(parts)-1] }

func (s *Service) CheckUpdate(ctx context.Context, p identity.Principal, id string, version int64) (Preview, error) {
	v, err := s.Get(ctx, p, id)
	if err != nil {
		return Preview{}, err
	}
	if v.Version != version {
		return Preview{}, conflict()
	}
	if v.SourceURL == "" {
		return Preview{}, invalid("此 Skill 没有关联来源，请先使用链接导入")
	}
	return s.Preview(ctx, p, "", nil, v.SourceURL, id, version)
}

func (s *Service) Apply(ctx context.Context, p identity.Principal, token, mode, trace string) (map[string][]string, error) {
	if !idPattern.MatchString(token) || mode != "skip" && mode != "replace" {
		return nil, invalid("预览或冲突处理方式无效")
	}
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM skill_import_previews WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND expires_at>clock_timestamp() FOR UPDATE`, token, p.OrganizationID, p.UserID).Scan(&envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	}
	if err != nil {
		return nil, err
	}
	data, err := s.cipher.Decrypt(envelope, s.aad(p, token, "skill-import"))
	if err != nil {
		return nil, err
	}
	defer clear(data)
	var stage staged
	if err = json.Unmarshal(data, &stage); err != nil {
		return nil, err
	}
	result := map[string][]string{"applied": {}, "skipped": {}}
	for _, b := range stage.Bundles {
		meta, err := validateBundle(b)
		if err != nil {
			return nil, err
		}
		current, err := find(ctx, tx, p, meta.Name)
		if err != nil {
			return nil, err
		}
		actual := int64(0)
		actualID := ""
		if current != nil {
			actual = current.Version
			actualID = current.ID
		}
		if actual != stage.Expected[meta.Name] || actualID != stage.ExpectedIDs[meta.Name] {
			return nil, conflict()
		}
		if current != nil && mode == "skip" {
			result["skipped"] = append(result["skipped"], meta.Name)
			continue
		}
		if current != nil {
			_, err = tx.Exec(ctx, `INSERT INTO skill_replacements(skill_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4) ON CONFLICT(skill_id) DO UPDATE SET key_id=excluded.key_id,nonce=excluded.nonce,ciphertext=excluded.ciphertext,created_at=clock_timestamp()`, current.ID, current.envelope.KeyID, current.envelope.Nonce, current.envelope.Ciphertext)
			if err != nil {
				return nil, err
			}
		}
		var synced *time.Time
		if stage.SourceURL != "" {
			now := time.Now().UTC()
			synced = &now
		}
		v, err := s.put(ctx, tx, p, current, b, stage.SourceURL, stage.Digests[meta.Name], synced)
		if err != nil {
			return nil, err
		}
		if err = audit(ctx, tx, p, v.ID, "SKILL_IMPORT", trace); err != nil {
			return nil, err
		}
		result["applied"] = append(result["applied"], meta.Name)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM skill_import_previews WHERE id=$1 AND org_id=$2 AND owner_id=$3`, token, p.OrganizationID, p.UserID); err != nil {
		return nil, err
	}
	return result, tx.Commit(ctx)
}
