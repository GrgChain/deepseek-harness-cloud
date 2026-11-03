package plugins

import (
	"context"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type pluginScanner interface{ Scan(...any) error }

func scanPlugin(row pluginScanner) (Plugin, error) {
	v := Plugin{Source: "community"}
	err := row.Scan(&v.ID, &v.Name, &v.PackageName, &v.Version, &v.Author, &v.Category, &v.Summary, &v.Description, &v.Keywords, &v.Repository, &v.Documentation, &v.Compatibility, &v.CheckedAt, &v.AdditionMethod, &v.Filename, &v.SHA256, &v.SourceNote, &v.Revision, &v.Custom)
	return v, err
}

func (s *Service) checkAvailable(ctx context.Context, p identity.Principal, name string) error {
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_catalog_defaults WHERE package_name=$3)
 OR EXISTS(SELECT 1 FROM personal_plugins WHERE org_id=$1 AND owner_id=$2 AND package_name=$3)`, p.OrganizationID, p.UserID, name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return webapi.Fail(409, "PLUGIN_EXISTS", "插件已在列表中")
	}
	return tx.Commit(ctx)
}

// EditInput changes catalog presentation only. Package identity and the stored
// offline artifact are deliberately absent from this write model.
type EditInput struct {
	Name             string   `json:"name"`
	Summary          string   `json:"summary"`
	Description      string   `json:"description"`
	Author           string   `json:"author"`
	Version          string   `json:"version"`
	Category         string   `json:"category"`
	Keywords         []string `json:"keywords"`
	Repository       string   `json:"repository"`
	Documentation    string   `json:"documentation"`
	Compatibility    string   `json:"compatibility"`
	CheckedAt        string   `json:"checked_at"`
	SourceNote       string   `json:"source_note"`
	ExpectedRevision int64    `json:"expected_revision"`
}

func plainText(value string, limit int, multiline bool) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t' || r == '\r'))
	}) < 0
}

func metadataURL(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 2048 || strings.TrimSpace(value) != value || !plainText(value, 2048, false) {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Opaque == ""
}

func (input *EditInput) validate() error {
	input.Name, input.Author = strings.TrimSpace(input.Name), strings.TrimSpace(input.Author)
	if input.ExpectedRevision < 1 || input.ExpectedRevision == math.MaxInt64 || input.Name == "" || input.Author == "" || !fixedVersion(input.Version) || !categoryValid(input.Category) || !plainText(input.Name, 214, false) || !plainText(input.Author, 200, false) || !plainText(input.Summary, 1000, true) || !plainText(input.Description, 4000, true) || !plainText(input.Compatibility, 2000, true) || !plainText(input.SourceNote, 4000, true) || !metadataURL(input.Repository) || !metadataURL(input.Documentation) || input.Keywords == nil || len(input.Keywords) > 20 {
		return invalid("请填写有效的插件资料、固定收录版本及编辑版本")
	}
	for _, keyword := range input.Keywords {
		if strings.TrimSpace(keyword) == "" || !plainText(keyword, 60, false) {
			return invalid("关键词最多 20 项，每项 1 至 60 个字符")
		}
	}
	date, err := time.Parse("2006-01-02", input.CheckedAt)
	if err != nil || date.Year() < 1 || date.Format("2006-01-02") != input.CheckedAt {
		return invalid("来源核对日期必须为有效的 YYYY-MM-DD 日期")
	}
	return nil
}

func (input EditInput) apply(v Plugin) Plugin {
	v.Name, v.Summary, v.Description, v.Author = input.Name, input.Summary, input.Description, input.Author
	v.Version, v.Category, v.Keywords = input.Version, input.Category, input.Keywords
	v.Repository, v.Documentation, v.Compatibility = input.Repository, input.Documentation, input.Compatibility
	v.CheckedAt, v.SourceNote = input.CheckedAt, input.SourceNote
	v.Revision++
	return v
}

func (s *Service) Edit(ctx context.Context, p identity.Principal, id string, input EditInput, trace string) (Plugin, error) {
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return Plugin{}, err
	}
	defer tx.Rollback(context.Background())
	if err = input.validate(); err != nil {
		return Plugin{}, err
	}
	missing := webapi.Fail(404, "PLUGIN_NOT_FOUND", "插件不存在，或无权访问")
	conflict := webapi.Fail(409, "PLUGIN_VERSION_CONFLICT", "插件资料已变化，请刷新后重试")
	var parsed pgtype.UUID
	if err = parsed.Scan(id); err != nil {
		return Plugin{}, missing
	}
	id = parsed.String()
	v, err := scanPlugin(tx.QueryRow(ctx, `SELECT id::text,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at::text,addition_method,archive_filename,archive_sha256,source_note,revision,true
 FROM personal_plugins WHERE id=$1 AND org_id=$2 AND owner_id=$3`, id, p.OrganizationID, p.UserID))
	isDefault := err == pgx.ErrNoRows
	if isDefault {
		v, err = scanPlugin(tx.QueryRow(ctx, `SELECT id::text,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at::text,'online','','',source_note,revision,false
 FROM plugin_catalog_defaults WHERE id=$1`, id))
	}
	if err == pgx.ErrNoRows {
		return Plugin{}, missing
	}
	if err != nil {
		return Plugin{}, err
	}
	if v.Revision != input.ExpectedRevision {
		return Plugin{}, conflict
	}
	v = input.apply(v)
	if isDefault {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM personal_plugins WHERE org_id=$1 AND owner_id=$2 AND (default_id=$3 OR package_name=$4))`, p.OrganizationID, p.UserID, id, v.PackageName).Scan(&exists); err != nil {
			return Plugin{}, err
		}
		if exists {
			return Plugin{}, conflict
		}
		v.ID, v.Custom = webapi.ID(), true
		tag, insertErr := tx.Exec(ctx, `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,revision,default_id)
 SELECT $1,$2,$3,$4,package_name,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,id FROM plugin_catalog_defaults WHERE id=$17 AND revision=$18`,
			v.ID, p.OrganizationID, p.UserID, v.Name, v.Version, v.Author, v.Category, v.Summary, v.Description, v.Keywords, v.Repository, v.Documentation, v.Compatibility, v.CheckedAt, v.SourceNote, v.Revision, id, input.ExpectedRevision)
		if insertErr != nil {
			return Plugin{}, insertErr
		}
		if tag.RowsAffected() != 1 {
			return Plugin{}, conflict
		}
	} else {
		tag, updateErr := tx.Exec(ctx, `UPDATE personal_plugins SET name=$4,package_version=$5,author=$6,category=$7,summary=$8,description=$9,keywords=$10,repository=$11,documentation=$12,compatibility=$13,checked_at=$14,source_note=$15,revision=revision+1
 WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND revision=$16`,
			id, p.OrganizationID, p.UserID, v.Name, v.Version, v.Author, v.Category, v.Summary, v.Description, v.Keywords, v.Repository, v.Documentation, v.Compatibility, v.CheckedAt, v.SourceNote, input.ExpectedRevision)
		if updateErr != nil {
			return Plugin{}, updateErr
		}
		if tag.RowsAffected() != 1 {
			return Plugin{}, conflict
		}
	}
	if trace == "" {
		trace = webapi.ID()
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id)
 VALUES($1,$2,$3,$3,$4,'PLUGIN_UPDATE','SUCCEEDED',$5)`, webapi.ID(), p.OrganizationID, p.UserID, v.ID, trace)
	if err != nil {
		return Plugin{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Plugin{}, err
	}
	return v, nil
}
