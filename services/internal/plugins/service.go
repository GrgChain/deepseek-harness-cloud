package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Plugin struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	PackageName    string   `json:"package_name"`
	Version        string   `json:"version"`
	Source         string   `json:"source"`
	Author         string   `json:"author"`
	Category       string   `json:"category"`
	Summary        string   `json:"summary"`
	Description    string   `json:"description"`
	Keywords       []string `json:"keywords"`
	Repository     string   `json:"repository"`
	Documentation  string   `json:"documentation"`
	Compatibility  string   `json:"compatibility"`
	CheckedAt      string   `json:"checked_at"`
	Custom         bool     `json:"custom"`
	AdditionMethod string   `json:"addition_method"`
	Filename       string   `json:"filename"`
	SHA256         string   `json:"sha256"`
	SourceNote     string   `json:"source_note"`
	Revision       int64    `json:"revision"`
}

type Resolver interface {
	Resolve(context.Context, string) (Plugin, error)
}

type Service struct {
	pool        *pgxpool.Pool
	registry    Resolver
	cipher      *security.SecretCipher
	environment string
	maintenance bool
}

func NewService(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string, maintenance bool) *Service {
	return &Service{pool: pool, registry: Registry{}, cipher: cipher, environment: environment, maintenance: maintenance}
}

func (s *Service) archiveContext(p identity.Principal, id string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: p.UserID + "/" + id, Purpose: "plugin-archive"}
}

func (s *Service) WithRegistry(registry Resolver) *Service {
	s.registry = registry
	return s
}

func invalid(message string) error { return webapi.Fail(422, "PLUGIN_INVALID", message) }

func categoryValid(category string) bool {
	return category == "会话与记忆" || category == "开发工具" || category == "集成扩展"
}

func (s *Service) begin(ctx context.Context, p identity.Principal, write bool) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	permission := "instances:read"
	if write {
		permission = "plugins:manage"
	}
	err = identity.RequirePermission(ctx, tx, p, permission)
	if err == nil && write && s.maintenance {
		err = webapi.Fail(503, "MAINTENANCE", "平台维护中，请稍后重试")
	}
	if err == nil && write {
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "plugins:"+p.OrganizationID+":"+p.UserID)
	}
	if err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}

func (s *Service) authorizeWrite(ctx context.Context, p identity.Principal) error {
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	return tx.Commit(ctx)
}

func (s *Service) List(ctx context.Context, p identity.Principal) ([]Plugin, error) {
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT id,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at::text,addition_method,archive_filename,archive_sha256,source_note,revision,custom FROM (
 SELECT id::text,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,addition_method,archive_filename,archive_sha256,source_note,revision,true AS custom,created_at,0 AS sort_order
 FROM personal_plugins WHERE org_id=$1 AND owner_id=$2
 UNION ALL
 SELECT id::text,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,'online','','',source_note,revision,false,'epoch'::timestamptz,sort_order
 FROM plugin_catalog_defaults d WHERE NOT EXISTS(SELECT 1 FROM personal_plugins p WHERE p.org_id=$1 AND p.owner_id=$2 AND p.package_name=d.package_name)
 ) visible ORDER BY custom DESC,created_at DESC,sort_order,id`, p.OrganizationID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Plugin{}
	for rows.Next() {
		v, scanErr := scanPlugin(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) Preview(ctx context.Context, p identity.Principal, spec string) (Plugin, error) {
	if err := s.authorizeWrite(ctx, p); err != nil {
		return Plugin{}, err
	}
	v, err := s.registry.Resolve(ctx, strings.TrimSpace(spec))
	if err != nil {
		return Plugin{}, err
	}
	if err := s.checkAvailable(ctx, p, v.PackageName); err != nil {
		return Plugin{}, err
	}
	v.ID, v.Source, v.Custom = "", "community", true
	v.AdditionMethod, v.Filename, v.SHA256 = "online", "", ""
	v.Revision = 1
	if v.Keywords == nil {
		v.Keywords = []string{}
	}
	return v, nil
}

func (s *Service) PreviewArchive(ctx context.Context, p identity.Principal, filename string, archive []byte) (Plugin, error) {
	if err := s.authorizeWrite(ctx, p); err != nil {
		return Plugin{}, err
	}
	v, err := ResolveArchive(ctx, filename, archive)
	if err != nil {
		return Plugin{}, err
	}
	if err := s.checkAvailable(ctx, p, v.PackageName); err != nil {
		return Plugin{}, err
	}
	v.ID, v.Source, v.Custom = "", "community", true
	v.Revision = 1
	return v, nil
}

func (s *Service) Create(ctx context.Context, p identity.Principal, packageName, version, category, trace string) (Plugin, error) {
	if err := s.authorizeWrite(ctx, p); err != nil {
		return Plugin{}, err
	}
	packageName, version = strings.TrimSpace(packageName), strings.TrimSpace(version)
	parsedName, parsedVersion, specErr := parsePackageSpec(packageName + "@" + version)
	if specErr != nil || parsedName != packageName || parsedVersion != version || !fixedVersion(version) || !categoryValid(category) {
		return Plugin{}, invalid("请填写有效包名、固定版本及插件分类")
	}
	if strings.HasPrefix(packageName, "@deepseek-ai/") {
		return Plugin{}, invalid("仅支持添加第三方 DSH 插件")
	}
	if err := s.checkAvailable(ctx, p, packageName); err != nil {
		return Plugin{}, err
	}
	// Resolve again at the selected fixed version. Browser-supplied metadata never
	// becomes a saved catalog entry, and no registry request holds database locks.
	v, err := s.registry.Resolve(ctx, packageName+"@"+version)
	if err != nil {
		return Plugin{}, err
	}
	if v.PackageName != packageName || v.Version != version {
		return Plugin{}, webapi.Fail(503, "PLUGIN_UNAVAILABLE", "插件元数据与所选版本不一致，请重新预览")
	}
	v.AdditionMethod, v.Filename, v.SHA256 = "online", "", ""
	return s.save(ctx, p, v, category, trace, nil)
}

func (s *Service) CreateArchive(ctx context.Context, p identity.Principal, filename string, archive []byte, category, trace string) (Plugin, error) {
	if err := s.authorizeWrite(ctx, p); err != nil {
		return Plugin{}, err
	}
	if !categoryValid(category) {
		return Plugin{}, invalid("请选择有效的插件分类")
	}
	v, err := ResolveArchive(ctx, filename, archive)
	if err != nil {
		return Plugin{}, err
	}
	return s.save(ctx, p, v, category, trace, archive)
}

func (s *Service) save(ctx context.Context, p identity.Principal, v Plugin, category, trace string, archive []byte) (Plugin, error) {
	v.ID, v.Source, v.Category, v.Custom = webapi.ID(), "community", category, true
	v.Revision = 1
	if v.Keywords == nil {
		v.Keywords = []string{}
	}
	var keyID *string
	var nonce, ciphertext []byte
	if v.AdditionMethod == "offline" {
		if s.cipher == nil {
			return Plugin{}, webapi.Fail(503, "PLUGIN_UNAVAILABLE", "插件包存储暂不可用，请稍后重试")
		}
		envelope, err := s.cipher.Encrypt(archive, s.archiveContext(p, v.ID))
		if err != nil {
			return Plugin{}, err
		}
		keyID, nonce, ciphertext = &envelope.KeyID, envelope.Nonce, envelope.Ciphertext
	}
	tx, err := s.begin(ctx, p, true)
	if err != nil {
		return Plugin{}, err
	}
	defer tx.Rollback(context.Background())
	var exists bool
	var count int
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM personal_plugins WHERE org_id=$1 AND owner_id=$2 AND package_name=$3)
 OR EXISTS(SELECT 1 FROM plugin_catalog_defaults WHERE package_name=$3),
 (SELECT count(*) FROM personal_plugins WHERE org_id=$1 AND owner_id=$2 AND default_id IS NULL)`, p.OrganizationID, p.UserID, v.PackageName).Scan(&exists, &count); err != nil {
		return Plugin{}, err
	}
	if exists {
		return Plugin{}, webapi.Fail(409, "PLUGIN_EXISTS", "插件已在列表中")
	}
	if count >= 100 {
		return Plugin{}, webapi.Fail(409, "PLUGIN_LIMIT", "最多添加 100 个插件")
	}
	_, err = tx.Exec(ctx, `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,addition_method,archive_filename,archive_sha256,archive_key_id,archive_nonce,archive_ciphertext,source_note)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`, v.ID, p.OrganizationID, p.UserID, v.Name, v.PackageName, v.Version, v.Author, v.Category, v.Summary, v.Description, v.Keywords, v.Repository, v.Documentation, v.Compatibility, v.CheckedAt, v.AdditionMethod, v.Filename, v.SHA256, keyID, nonce, ciphertext, v.SourceNote)
	if err != nil {
		return Plugin{}, err
	}
	if trace == "" {
		trace = webapi.ID()
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id)
 VALUES($1,$2,$3,$3,$4,'PLUGIN_ADD','SUCCEEDED',$5)`, webapi.ID(), p.OrganizationID, p.UserID, v.ID, trace)
	if err != nil {
		return Plugin{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Plugin{}, err
	}
	return v, nil
}

type ArchiveDownload struct {
	Data             []byte
	Filename, SHA256 string
}

func (s *Service) DownloadArchive(ctx context.Context, p identity.Principal, id string) (ArchiveDownload, error) {
	missing := func() error {
		return webapi.Fail(404, "PLUGIN_ARCHIVE_NOT_FOUND", "插件包不存在，或无权访问")
	}
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return ArchiveDownload{}, missing()
	}
	id = parsed.String()
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return ArchiveDownload{}, err
	}
	defer tx.Rollback(context.Background())
	var result ArchiveDownload
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT archive_filename,archive_sha256,archive_key_id,archive_nonce,archive_ciphertext FROM personal_plugins WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND addition_method='offline'`, p.OrganizationID, p.UserID, id).Scan(&result.Filename, &result.SHA256, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if err == pgx.ErrNoRows {
		return ArchiveDownload{}, missing()
	}
	if err != nil {
		return ArchiveDownload{}, err
	}
	if s.cipher == nil {
		return ArchiveDownload{}, webapi.Fail(503, "PLUGIN_UNAVAILABLE", "插件包暂时无法下载，请稍后重试")
	}
	result.Data, err = s.cipher.Decrypt(envelope, s.archiveContext(p, id))
	if err != nil {
		return ArchiveDownload{}, webapi.Fail(503, "PLUGIN_UNAVAILABLE", "插件包暂时无法下载，请稍后重试")
	}
	hash := sha256.Sum256(result.Data)
	if len(result.Data) > maxArchiveBytes || hex.EncodeToString(hash[:]) != result.SHA256 {
		clear(result.Data)
		return ArchiveDownload{}, webapi.Fail(503, "PLUGIN_UNAVAILABLE", "插件包暂时无法下载，请稍后重试")
	}
	if err = tx.Commit(ctx); err != nil {
		clear(result.Data)
		return ArchiveDownload{}, err
	}
	return result, nil
}
