package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Skill struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	ModelInvocable bool       `json:"model_invocable"`
	UserInvocable  bool       `json:"user_invocable"`
	Version        int64      `json:"version"`
	FileCount      int        `json:"file_count"`
	Size           int        `json:"size_bytes"`
	SourceURL      string     `json:"source_url"`
	SourceDigest   string     `json:"-"`
	SyncedAt       *time.Time `json:"synced_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	HasPrevious    bool       `json:"has_previous"`
	Content        string     `json:"content,omitempty"`
	Files          []File     `json:"files,omitempty"`
	envelope       security.Envelope
}
type File struct {
	Name string `json:"name"`
	Size int    `json:"size_bytes"`
}
type FileContent struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Version  int64  `json:"version"`
	Editable bool   `json:"editable"`
}
type List struct {
	Items []Skill `json:"items"`
	Total int     `json:"total"`
	Page  int     `json:"page"`
	Limit int     `json:"limit"`
}
type PreviewItem struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Files        []File   `json:"files"`
	Conflict     bool     `json:"conflict"`
	LocalChanged bool     `json:"local_changed"`
	ChangedFiles []string `json:"changed_files"`
}
type Preview struct {
	Token     string        `json:"token"`
	Items     []PreviewItem `json:"items"`
	SourceURL string        `json:"source_url"`
	Unchanged bool          `json:"unchanged"`
}
type staged struct {
	Bundles     []Bundle          `json:"bundles"`
	Expected    map[string]int64  `json:"expected"`
	ExpectedIDs map[string]string `json:"expected_ids"`
	SourceURL   string            `json:"source_url"`
	Digests     map[string]string `json:"digests"`
}
type Service struct {
	pool        *pgxpool.Pool
	cipher      *security.SecretCipher
	environment string
	maintenance bool
	fetch       sourceFetcher
}

func NewService(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string, maintenance bool) *Service {
	return &Service{pool, cipher, environment, maintenance, fetchSource}
}
func missing() error { return webapi.Fail(404, "NOT_FOUND", "Skill 或预览不存在，或已过期") }
func conflict() error {
	return webapi.Fail(409, "VERSION_CONFLICT", "Skill 已被修改，请刷新后重新操作")
}
func (s *Service) aad(p identity.Principal, id, purpose string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: id, Purpose: purpose}
}
func (s *Service) begin(ctx context.Context, p identity.Principal, write bool) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	permission := "skills:read"
	if write {
		permission = "skills:manage"
	}
	if err = identity.RequirePermission(ctx, tx, p, permission); err == nil && write && s.maintenance {
		err = webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	if err == nil && write {
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "skills:"+p.OrganizationID+":"+p.UserID)
	}
	if err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}

const columns = `s.id::text,s.name,s.description,s.model_invocable,s.user_invocable,s.version,s.file_count,s.size_bytes,s.source_url,s.source_digest,s.synced_at,s.created_at,s.updated_at,EXISTS(SELECT 1 FROM skill_replacements b WHERE b.skill_id=s.id),s.key_id,s.nonce,s.ciphertext`

func scan(row pgx.Row) (Skill, error) {
	var v Skill
	err := row.Scan(&v.ID, &v.Name, &v.Description, &v.ModelInvocable, &v.UserInvocable, &v.Version, &v.FileCount, &v.Size, &v.SourceURL, &v.SourceDigest, &v.SyncedAt, &v.CreatedAt, &v.UpdatedAt, &v.HasPrevious, &v.envelope.KeyID, &v.envelope.Nonce, &v.envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		err = missing()
	}
	return v, err
}
func load(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (Skill, error) {
	if !idPattern.MatchString(id) {
		return Skill{}, missing()
	}
	return scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM personal_skills s WHERE s.org_id=$1 AND s.owner_id=$2 AND s.id=$3`, p.OrganizationID, p.UserID, id))
}
func find(ctx context.Context, tx pgx.Tx, p identity.Principal, name string) (*Skill, error) {
	v, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM personal_skills s WHERE s.org_id=$1 AND s.owner_id=$2 AND s.name=$3`, p.OrganizationID, p.UserID, name))
	var problem *webapi.Problem
	if errors.As(err, &problem) && problem.Status == 404 {
		return nil, nil
	}
	return &v, err
}
func (s *Service) open(p identity.Principal, v Skill) (Bundle, error) {
	var b Bundle
	data, err := s.cipher.Decrypt(v.envelope, s.aad(p, v.ID, "skill-bundle"))
	if err != nil {
		return b, err
	}
	defer clear(data)
	err = json.Unmarshal(data, &b)
	if err == nil {
		var meta Metadata
		meta, err = validateBundle(b)
		if meta.Name != v.Name || bundleSize(b) != v.Size || len(b.Files) != v.FileCount {
			err = errors.New("skill metadata mismatch")
		}
	}
	return b, err
}
func (s *Service) seal(p identity.Principal, id, purpose string, value any) (security.Envelope, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return security.Envelope{}, err
	}
	defer clear(data)
	return s.cipher.Encrypt(data, s.aad(p, id, purpose))
}
func filesView(b Bundle) []File {
	out := []File{}
	for _, name := range fileNames(b) {
		out = append(out, File{name, len(b.Files[name])})
	}
	return out
}
func audit(ctx context.Context, tx pgx.Tx, p identity.Principal, id, action, trace string) error {
	if trace == "" {
		trace = webapi.ID()
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,'SUCCEEDED',$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, action, trace)
	return err
}

func (s *Service) List(ctx context.Context, p identity.Principal, q string, page, limit int) (List, error) {
	out := List{Items: []Skill{}, Page: page, Limit: limit}
	if len(q) > 200 || page < 1 || page > 10000 || limit < 1 || limit > 100 {
		return out, invalid("分页或搜索参数无效")
	}
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `DELETE FROM skill_import_previews WHERE expires_at<=clock_timestamp()`); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM personal_skills WHERE org_id=$1 AND owner_id=$2 AND (strpos(lower(name),lower($3))>0 OR strpos(lower(description),lower($3))>0)`, p.OrganizationID, p.UserID, q).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT s.id::text,s.name,s.description,s.model_invocable,s.user_invocable,s.version,s.file_count,s.size_bytes,s.source_url,s.synced_at,s.created_at,s.updated_at,EXISTS(SELECT 1 FROM skill_replacements b WHERE b.skill_id=s.id) FROM personal_skills s WHERE s.org_id=$1 AND s.owner_id=$2 AND (strpos(lower(s.name),lower($3))>0 OR strpos(lower(s.description),lower($3))>0) ORDER BY s.updated_at DESC,s.id LIMIT $4 OFFSET $5`, p.OrganizationID, p.UserID, q, limit, (page-1)*limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v Skill
		err = rows.Scan(&v.ID, &v.Name, &v.Description, &v.ModelInvocable, &v.UserInvocable, &v.Version, &v.FileCount, &v.Size, &v.SourceURL, &v.SyncedAt, &v.CreatedAt, &v.UpdatedAt, &v.HasPrevious)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Service) Get(ctx context.Context, p identity.Principal, id string) (Skill, error) {
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return Skill{}, err
	}
	defer tx.Rollback(context.Background())
	v, err := load(ctx, tx, p, id)
	if err != nil {
		return v, err
	}
	b, err := s.open(p, v)
	if err != nil {
		return v, err
	}
	v.Content = string(b.Files["SKILL.md"])
	v.Files = filesView(b)
	return v, tx.Commit(ctx)
}

func editableText(data []byte) bool {
	return utf8.Valid(data) && !bytes.ContainsRune(data, 0) && strings.HasPrefix(http.DetectContentType(data), "text/")
}

func (s *Service) ReadFile(ctx context.Context, p identity.Principal, id, filename string) (FileContent, error) {
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return FileContent{}, err
	}
	defer tx.Rollback(context.Background())
	current, err := load(ctx, tx, p, id)
	if err != nil {
		return FileContent{}, err
	}
	if !safePath(filename) {
		return FileContent{}, invalid("文件路径无效")
	}
	bundle, err := s.open(p, current)
	if err != nil {
		return FileContent{}, err
	}
	data, exists := bundle.Files[filename]
	if !exists {
		return FileContent{}, missing()
	}
	out := FileContent{Path: filename, Version: current.Version, Editable: editableText(data)}
	if out.Editable {
		out.Content = string(data)
	}
	return out, tx.Commit(ctx)
}
func checkLimit(ctx context.Context, tx pgx.Tx, p identity.Principal, countDelta, sizeDelta int) error {
	var count, size int
	if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM personal_skills WHERE org_id=$1 AND owner_id=$2`, p.OrganizationID, p.UserID).Scan(&count, &size); err != nil {
		return err
	}
	if count+countDelta > 200 || size+sizeDelta > 64<<20 {
		return webapi.Fail(409, "SKILL_LIMIT", "个人技能库最多 200 项，总内容不超过 64 MiB")
	}
	return nil
}
func (s *Service) put(ctx context.Context, tx pgx.Tx, p identity.Principal, current *Skill, b Bundle, source, digest string, synced *time.Time) (Skill, error) {
	meta, err := validateBundle(b)
	if err != nil {
		return Skill{}, err
	}
	id := webapi.ID()
	if current != nil {
		id = current.ID
	}
	envelope, err := s.seal(p, id, "skill-bundle", b)
	if err != nil {
		return Skill{}, err
	}
	if current == nil {
		_, err = tx.Exec(ctx, `INSERT INTO personal_skills(id,org_id,owner_id,name,description,model_invocable,user_invocable,file_count,size_bytes,key_id,nonce,ciphertext,source_url,source_digest,synced_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, id, p.OrganizationID, p.UserID, meta.Name, meta.Description, meta.ModelInvocable, meta.UserInvocable, len(b.Files), bundleSize(b), envelope.KeyID, envelope.Nonce, envelope.Ciphertext, source, digest, synced)
	} else {
		_, err = tx.Exec(ctx, `UPDATE personal_skills SET description=$4,model_invocable=$5,user_invocable=$6,version=version+1,file_count=$7,size_bytes=$8,key_id=$9,nonce=$10,ciphertext=$11,source_url=$12,source_digest=$13,synced_at=$14,updated_at=clock_timestamp() WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID, meta.Description, meta.ModelInvocable, meta.UserInvocable, len(b.Files), bundleSize(b), envelope.KeyID, envelope.Nonce, envelope.Ciphertext, source, digest, synced)
	}
	if err != nil {
		return Skill{}, err
	}
	return load(ctx, tx, p, id)
}
func (s *Service) Create(ctx context.Context, p identity.Principal, name, description, trace string) (Skill, error) {
	b, err := newBundle(name, description)
	if err != nil {
		return Skill{}, err
	}
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return Skill{}, err
	}
	defer tx.Rollback(context.Background())
	existing, err := find(ctx, tx, p, name)
	if err != nil {
		return Skill{}, err
	}
	if existing != nil {
		return Skill{}, webapi.Fail(409, "SKILL_EXISTS", "已存在同名 Skill，请换一个名称")
	}
	if err = checkLimit(ctx, tx, p, 1, bundleSize(b)); err != nil {
		return Skill{}, err
	}
	v, err := s.put(ctx, tx, p, nil, b, "", "", nil)
	if err == nil {
		err = audit(ctx, tx, p, v.ID, "SKILL_CREATE", trace)
	}
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (s *Service) Edit(ctx context.Context, p identity.Principal, id string, version int64, content string, policy *[2]bool, trace string) (Skill, error) {
	return s.edit(ctx, p, id, version, "SKILL.md", content, policy, false, trace)
}

func (s *Service) EditFile(ctx context.Context, p identity.Principal, id string, version int64, filename, content, trace string) (Skill, error) {
	return s.edit(ctx, p, id, version, filename, content, nil, false, trace)
}

func (s *Service) CreateFile(ctx context.Context, p identity.Principal, id string, version int64, filename, content, trace string) (Skill, error) {
	return s.edit(ctx, p, id, version, filename, content, nil, true, trace)
}

func (s *Service) edit(ctx context.Context, p identity.Principal, id string, version int64, filename, content string, policy *[2]bool, create bool, trace string) (Skill, error) {
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return Skill{}, err
	}
	defer tx.Rollback(context.Background())
	current, err := load(ctx, tx, p, id)
	if err != nil {
		return current, err
	}
	if current.Version != version {
		return current, conflict()
	}
	b, err := s.open(p, current)
	if err != nil {
		return current, err
	}
	if policy != nil {
		b, err = setPolicy(b, policy[0], policy[1])
	} else {
		if !safePath(filename) {
			return current, invalid("文件路径无效")
		}
		old, exists := b.Files[filename]
		if create {
			for existing := range b.Files {
				if strings.EqualFold(existing, filename) {
					return current, invalid("已存在同名文件，请使用其他路径")
				}
			}
		} else if !exists {
			return current, missing()
		}
		if len(content) > MaxFile || !editableText(old) || !editableText([]byte(content)) {
			return current, invalid("只支持编辑不超过 1 MiB 的 UTF-8 文本文件，二进制文件请下载查看")
		}
		b.Files[filename] = []byte(content)
	}
	if err != nil {
		return current, err
	}
	meta, err := validateBundle(b)
	if err != nil {
		return current, err
	}
	if meta.Name != current.Name {
		return current, invalid("编辑不能更改 Skill 名称，请通过导入创建副本")
	}
	if err = checkLimit(ctx, tx, p, 0, bundleSize(b)-current.Size); err != nil {
		return current, err
	}
	v, err := s.put(ctx, tx, p, &current, b, current.SourceURL, current.SourceDigest, current.SyncedAt)
	if err == nil {
		err = audit(ctx, tx, p, id, "SKILL_UPDATE", trace)
	}
	if err != nil {
		return v, err
	}
	return v, tx.Commit(ctx)
}
func (s *Service) Delete(ctx context.Context, p identity.Principal, id string, version int64, name, trace string) error {
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	v, err := load(ctx, tx, p, id)
	if err != nil {
		return err
	}
	if v.Version != version {
		return conflict()
	}
	if name != v.Name {
		return invalid("输入的 Skill 名称不匹配")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM personal_skills WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, id, "SKILL_DELETE", trace); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
		delta, count := bundleSize(b), 1
		if current != nil {
			delta -= current.Size
			count = 0
		}
		if err = checkLimit(ctx, tx, p, count, delta); err != nil {
			return nil, err
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
func (s *Service) Export(ctx context.Context, p identity.Principal, ids []string, previous bool) ([]byte, string, error) {
	if len(ids) < 1 || len(ids) > 50 || previous && len(ids) != 1 {
		return nil, "", invalid("请选择 1–50 项 Skill")
	}
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(context.Background())
	bundles := []Bundle{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, "", invalid("导出不能包含重复选择")
		}
		seen[id] = true
		v, err := load(ctx, tx, p, id)
		if err != nil {
			return nil, "", err
		}
		if previous {
			err = tx.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM skill_replacements WHERE skill_id=$1`, id).Scan(&v.envelope.KeyID, &v.envelope.Nonce, &v.envelope.Ciphertext)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, "", missing()
			}
			if err != nil {
				return nil, "", err
			}
			raw, err := s.cipher.Decrypt(v.envelope, s.aad(p, id, "skill-bundle"))
			if err != nil {
				return nil, "", err
			}
			var b Bundle
			err = json.Unmarshal(raw, &b)
			clear(raw)
			if err != nil {
				return nil, "", err
			}
			bundles = append(bundles, b)
		} else {
			b, err := s.open(p, v)
			if err != nil {
				return nil, "", err
			}
			bundles = append(bundles, b)
		}
	}
	data, name, err := exportBundles(bundles)
	if err != nil {
		return nil, "", err
	}
	if previous {
		name = "previous-" + name
	}
	return data, name, tx.Commit(ctx)
}
