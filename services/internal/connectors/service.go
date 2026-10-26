package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Connection struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	EndpointID     string          `json:"endpoint_id"`
	URL            string          `json:"url"`
	CatalogID      string          `json:"catalog_id,omitempty"`
	Protocol       string          `json:"protocol_version"`
	Auth           string          `json:"auth_type"`
	Version        int64           `json:"version"`
	CatalogVersion int64           `json:"catalog_version"`
	Enabled        bool            `json:"enabled"`
	Status         string          `json:"status"`
	Server         json.RawMessage `json:"server_info"`
	Tools          []Tool          `json:"tools"`
	VerifiedAt     *time.Time      `json:"verified_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	envelope       security.Envelope
}
type Input struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	EndpointID      string `json:"endpoint_id"`
	URL             string `json:"url"`
	CatalogID       string `json:"catalog_id,omitempty"`
	Protocol        string `json:"protocol_version"`
	Auth            string `json:"auth_type"`
	Credential      string `json:"credential"`
	Enabled         bool   `json:"enabled"`
	ExpectedVersion int64  `json:"expected_version"`
	ConfirmedName   string `json:"confirmed_name"`
}
type Service struct {
	pool         *pgxpool.Pool
	cipher       *security.SecretCipher
	environment  string
	maintenance  bool
	portalOrigin string
	policy       func() (Policy, error)
	active       sync.Map
}
type activeCall struct {
	owner, connection string
	cancel            context.CancelFunc
}

func NewService(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string, maintenance bool, path string, portalOrigin ...string) *Service {
	origin := ""
	if len(portalOrigin) > 0 {
		origin = strings.TrimRight(portalOrigin[0], "/")
	}
	return &Service{pool: pool, cipher: cipher, environment: environment, maintenance: maintenance, portalOrigin: origin, policy: func() (Policy, error) { return LoadPolicy(path) }}
}
func invalid(message string) error { return webapi.Fail(422, "CONNECTOR_INVALID", message) }
func missing() error               { return webapi.Fail(404, "NOT_FOUND", "连接器或调用记录不存在") }
func conflict() error {
	return webapi.Fail(409, "VERSION_CONFLICT", "连接或工具目录已变更，请刷新后重试")
}
func unavailable() error {
	return webapi.Fail(503, "CONNECTOR_UNAVAILABLE", "连接器服务暂不可用，请稍后重试")
}
func (s *Service) begin(ctx context.Context, p identity.Principal, permission string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	err = identity.RequirePermission(ctx, tx, p, permission)
	if err == nil && permission != "connectors:read" {
		if s.maintenance {
			err = webapi.Fail(503, "MAINTENANCE", "平台维护中")
		} else {
			_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "connector:"+p.OrganizationID+":"+p.UserID)
		}
	}
	if err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}
func audit(ctx context.Context, tx pgx.Tx, p identity.Principal, id, action, trace string) error {
	if trace == "" {
		trace = webapi.ID()
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,'SUCCEEDED',$6)`, webapi.ID(), p.OrganizationID, p.UserID, id, action, trace)
	return err
}

const columns = `id::text,name,description,endpoint_id,protocol_version,auth_type,version,catalog_version,enabled,status,server_info,tools,verified_at,updated_at,key_id,nonce,ciphertext,endpoint_url,catalog_id`

func scan(row pgx.Row) (Connection, error) {
	var c Connection
	var tools []byte
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.EndpointID, &c.Protocol, &c.Auth, &c.Version, &c.CatalogVersion, &c.Enabled, &c.Status, &c.Server, &tools, &c.VerifiedAt, &c.UpdatedAt, &c.envelope.KeyID, &c.envelope.Nonce, &c.envelope.Ciphertext, &c.URL, &c.CatalogID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = missing()
	}
	if err == nil {
		err = json.Unmarshal(tools, &c.Tools)
	}
	return c, err
}
func load(ctx context.Context, tx pgx.Tx, p identity.Principal, id string) (Connection, error) {
	if !uuidPattern.MatchString(id) {
		return Connection{}, missing()
	}
	return scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND deleted_at IS NULL`, p.OrganizationID, p.UserID, id))
}
func (s *Service) aad(p identity.Principal, id string) security.Context {
	return security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: id, Purpose: "connector-credential"}
}
func (s *Service) endpoint(id string) (Endpoint, error) {
	policy, err := s.loadPolicy()
	if err != nil {
		return Endpoint{}, unavailable()
	}
	for _, e := range policy.Endpoints {
		if e.ID == id {
			return e, nil
		}
	}
	return Endpoint{}, webapi.Fail(422, "ENDPOINT_NOT_APPROVED", "此MCP服务尚未批准或批准已撤销")
}

// Personal endpoints permit public HTTPS. Only an exact platform endpoint may
// opt into private addresses or HTTP; DNS checks and IP pinning still apply.
func (s *Service) personalEndpoint(address, catalog string) (Endpoint, error) {
	e := Endpoint{ID: "personal", URL: address, CatalogID: catalog}
	policy, err := s.loadPolicy()
	if err != nil {
		return e, unavailable()
	}
	for _, approved := range policy.Endpoints {
		if approved.URL == address {
			e.AllowPrivate, e.AllowHTTP = approved.AllowPrivate, approved.AllowHTTP
			break
		}
	}
	u, err := parseEndpoint(e)
	if err != nil {
		return e, invalid("请输入 HTTPS MCP 服务地址，凭据请单独填写；内网或 HTTP 地址须由管理员配置")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && (!allowedIP(ip, e.AllowPrivate) || u.Scheme == "http" && !ip.IsPrivate() && !ip.IsLoopback()) {
		return e, invalid("此 MCP 服务地址不可访问，内网地址须由管理员配置")
	}
	return e, nil
}
func (s *Service) connectionEndpoint(c Connection) (Endpoint, error) {
	if c.EndpointID == "personal" {
		return s.personalEndpoint(c.URL, c.CatalogID)
	}
	return s.endpoint(c.EndpointID)
}
func (s *Service) List(ctx context.Context, p identity.Principal) (any, error) {
	tx, err := s.begin(ctx, p, "connectors:read")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND deleted_at IS NULL ORDER BY updated_at DESC,id`, p.OrganizationID, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Connection{}
	for rows.Next() {
		c, e := scan(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	policy, err := s.loadPolicy()
	if err != nil {
		return nil, unavailable()
	}
	for i := range items {
		if items[i].EndpointID != "personal" {
			for _, e := range policy.Endpoints {
				if e.ID == items[i].EndpointID {
					items[i].URL, items[i].CatalogID = e.URL, e.CatalogID
				}
			}
		}
	}
	return map[string]any{"items": items, "endpoints": policy.Endpoints, "catalog": builtinCatalog()}, nil
}
func validateInput(in Input) error {
	validTarget := in.URL != "" && in.EndpointID == "" || in.URL == "" && endpointID.MatchString(in.EndpointID) && in.EndpointID != "personal"
	if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 100 || !utf8.ValidString(in.Name) || len(in.Description) > 1000 || !validTarget || in.CatalogID != "" && !knownCatalogID(in.CatalogID) || in.Protocol != "2026-07-28" && in.Protocol != "2025-11-25" || in.Auth != "none" && in.Auth != "bearer" && in.Auth != "oauth2" || len(in.Credential) > 4096 || strings.IndexFunc(in.Credential, unicode.IsControl) >= 0 {
		return invalid("请检查名称、服务、协议版本及认证配置")
	}
	return nil
}
func (s *Service) Save(ctx context.Context, p identity.Principal, id string, in Input, trace string) (Connection, error) {
	if err := validateInput(in); err != nil {
		return Connection{}, err
	}
	var target Endpoint
	var err error
	if in.URL != "" {
		target, err = s.personalEndpoint(in.URL, in.CatalogID)
		in.EndpointID = "personal"
	} else {
		target, err = s.endpoint(in.EndpointID)
	}
	if err != nil {
		return Connection{}, err
	}
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(context.Background())
	var old Connection
	fresh := id == ""
	if fresh {
		id = webapi.ID()
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND deleted_at IS NULL`, p.OrganizationID, p.UserID).Scan(&n); err != nil {
			return old, err
		}
		if n >= 50 {
			return old, invalid("个人连接器不能超过50个")
		}
	} else {
		old, err = load(ctx, tx, p, id)
		if err != nil {
			return old, err
		}
		if old.Version != in.ExpectedVersion {
			return old, conflict()
		}
	}
	credential := []byte(in.Credential)
	if in.Auth == "oauth2" && in.Credential != "" {
		return old, invalid("OAuth 凭据只能通过官方授权获取")
	}
	if in.Auth == "none" || in.Auth == "oauth2" && (fresh || old.Auth != "oauth2") {
		credential = []byte("-")
	} else if in.Credential == "" {
		if fresh || old.Auth != in.Auth {
			return old, invalid("请输入此服务的Bearer凭据")
		}
		previous, targetErr := s.connectionEndpoint(old)
		if targetErr != nil || previous.URL != target.URL {
			return old, invalid("服务地址已变更，请重新填写此服务的 Bearer 凭据")
		}
		credential, err = s.cipher.Decrypt(old.envelope, s.aad(p, id))
		if err != nil {
			return old, unavailable()
		}
	}
	defer clear(credential)
	envelope, err := s.cipher.Encrypt(credential, s.aad(p, id))
	if err != nil {
		return old, err
	}
	if fresh {
		_, err = tx.Exec(ctx, `INSERT INTO mcp_connectors(id,org_id,owner_id,name,description,endpoint_id,protocol_version,auth_type,enabled,key_id,nonce,ciphertext,endpoint_url,catalog_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, p.OrganizationID, p.UserID, strings.TrimSpace(in.Name), in.Description, in.EndpointID, in.Protocol, in.Auth, in.Enabled, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, in.URL, in.CatalogID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET name=$2,description=$3,endpoint_id=$4,protocol_version=$5,auth_type=$6,enabled=$7,key_id=$8,nonce=$9,ciphertext=$10,endpoint_url=$11,catalog_id=$12,status='UNVERIFIED',tools='[]',server_info='{}',catalog_version=catalog_version+1,version=version+1,verified_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, id, strings.TrimSpace(in.Name), in.Description, in.EndpointID, in.Protocol, in.Auth, in.Enabled, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, in.URL, in.CatalogID)
	}
	if err == nil && in.Auth == "oauth2" {
		_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET status='AUTH_REQUIRED' WHERE id=$1`, id)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return old, invalid("已有同名连接器")
	}
	if err != nil {
		return old, err
	}
	if err = audit(ctx, tx, p, id, "CONNECTOR_SAVED", trace); err != nil {
		return old, err
	}
	c, err := load(ctx, tx, p, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		s.cancelConnection(p.UserID, id)
	}
	return c, err
}
func (s *Service) Delete(ctx context.Context, p identity.Principal, id string, in Input, trace string) error {
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	c, err := load(ctx, tx, p, id)
	if err != nil {
		return err
	}
	if c.Version != in.ExpectedVersion {
		return conflict()
	}
	if in.ConfirmedName != c.Name {
		return invalid("请输入正确的连接器名称")
	}
	e, err := s.cipher.Encrypt([]byte("-"), s.aad(p, id))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET deleted_at=clock_timestamp(),enabled=false,tools='[]',key_id=$2,nonce=$3,ciphertext=$4,version=version+1 WHERE id=$1`, id, e.KeyID, e.Nonce, e.Ciphertext)
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM mcp_oauth_transactions WHERE connector_id=$1`, id)
	}
	if err == nil {
		err = audit(ctx, tx, p, id, "CONNECTOR_DELETED", trace)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		s.cancelConnection(p.UserID, id)
	}
	return err
}
func (s *Service) cancelConnection(owner, id string) {
	s.active.Range(func(_, v any) bool {
		call := v.(activeCall)
		if call.owner == owner && call.connection == id {
			call.cancel()
		}
		return true
	})
}

func mergeTools(old, new []Tool) []Tool {
	previous := map[string]Tool{}
	for _, v := range old {
		previous[v.Name] = v
	}
	for i := range new {
		v, ok := previous[new[i].Name]
		if !ok {
			new[i].Change = "added"
		} else if v.Hash == new[i].Hash {
			new[i].Enabled = v.Enabled
		} else {
			new[i].Change = "changed"
		}
	}
	return new
}
func (s *Service) Sync(ctx context.Context, p identity.Principal, id string, version int64, trace string) (Connection, error) {
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return Connection{}, err
	}
	c, err := load(ctx, tx, p, id)
	if err != nil {
		tx.Rollback(context.Background())
		return c, err
	}
	if c.Version != version {
		tx.Rollback(context.Background())
		return c, conflict()
	}
	if !c.Enabled {
		tx.Rollback(context.Background())
		return c, invalid("请先启用连接器")
	}
	credential, credentialErr := s.credential(ctx, tx, p, c)
	if credentialErr != nil && !errors.Is(credentialErr, errAuth) {
		tx.Rollback(context.Background())
		return c, credentialErr
	}
	if err = tx.Commit(ctx); err != nil {
		return c, err
	}
	if c.Version != version {
		return c, conflict()
	}
	if !c.Enabled {
		return c, invalid("请先启用连接器")
	}
	e, err := s.connectionEndpoint(c)
	if err != nil {
		return c, err
	}
	discovery, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	var session *mcp.ClientSession
	remoteErr := credentialErr
	if remoteErr == nil {
		session, remoteErr = openSession(discovery, e, credential, c.Protocol)
	}
	var tools []Tool
	server := []byte(`{}`)
	if remoteErr == nil {
		tools, remoteErr = discover(discovery, session)
		if remoteErr == nil {
			info := map[string]any{"name": session.InitializeResult().ServerInfo.Name, "version": session.InitializeResult().ServerInfo.Version, "capabilities": session.InitializeResult().Capabilities}
			removed := []string{}
			live := map[string]bool{}
			for _, v := range tools {
				live[v.Name] = true
			}
			for _, v := range c.Tools {
				if !live[v.Name] {
					removed = append(removed, v.Name)
				}
			}
			info["removed_tools"] = removed
			server, _ = json.Marshal(info)
		}
		session.Close()
	}
	tx, err = s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return c, err
	}
	defer tx.Rollback(context.Background())
	latest, err := load(ctx, tx, p, id)
	if err != nil {
		return c, err
	}
	if latest.Version != version {
		return c, conflict()
	}
	if approved, checkErr := s.connectionEndpoint(c); checkErr != nil {
		return c, checkErr
	} else if approved != e {
		return c, conflict()
	}
	state := "READY"
	encoded, _ := json.Marshal(mergeTools(c.Tools, tools))
	if remoteErr != nil {
		state = "FAILED"
		if errors.Is(remoteErr, errAuth) {
			state = "AUTH_REQUIRED"
		}
		encoded, _ = json.Marshal(c.Tools)
		info := map[string]any{}
		_ = json.Unmarshal(c.Server, &info)
		info["verification_error"] = verificationMessage(remoteErr)
		server, _ = json.Marshal(info)
	}
	_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET status=$2,tools=$3,server_info=$4,version=version+1,catalog_version=catalog_version+1,verified_at=CASE WHEN $2='READY' THEN clock_timestamp() ELSE verified_at END,updated_at=clock_timestamp() WHERE id=$1`, id, state, encoded, server)
	if err == nil {
		err = audit(ctx, tx, p, id, "CONNECTOR_SYNCED", trace)
	}
	if err != nil {
		return c, err
	}
	c, err = load(ctx, tx, p, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return c, err
}

// Only fixed descriptions cross the API boundary; remote errors may contain secrets.
func verificationMessage(err error) string {
	switch {
	case errors.Is(err, errAuth):
		return "服务凭据无效或已过期，请更新后重新验证"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "验证超时或已取消，请稍后重试"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "protocol"):
		return "服务协议不兼容，请检查所选协议版本"
	case strings.Contains(text, "schema"):
		return "工具参数定义不受支持，请检查服务目录"
	case strings.Contains(text, "limit"), strings.Contains(text, "cursor"), strings.Contains(text, "repeated tool"):
		return "工具目录或响应超限，请检查服务分页及内容"
	case strings.Contains(text, "certificate"), strings.Contains(text, "tls"):
		return "服务证书验证失败，请检查服务证书"
	case strings.Contains(text, "denied"):
		return "服务地址或重定向不符合批准策略，请联系管理员"
	default:
		return "无法完成服务验证，请检查服务状态和网络后重试"
	}
}

func (s *Service) SelectTools(ctx context.Context, p identity.Principal, id string, version int64, names []string, trace string) (Connection, error) {
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback(context.Background())
	c, err := load(ctx, tx, p, id)
	if err != nil {
		return c, err
	}
	if c.Version != version {
		return c, conflict()
	}
	if !c.Enabled || c.Status != "READY" {
		return c, invalid("请先验证连接器")
	}
	selected := map[string]bool{}
	for _, name := range names {
		if selected[name] {
			return c, invalid("工具选择重复")
		}
		selected[name] = true
	}
	for i := range c.Tools {
		c.Tools[i].Enabled = selected[c.Tools[i].Name]
		c.Tools[i].Change = ""
		delete(selected, c.Tools[i].Name)
	}
	if len(selected) != 0 {
		return c, invalid("所选工具已不存在")
	}
	encoded, _ := json.Marshal(c.Tools)
	_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET tools=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, encoded)
	if err == nil {
		err = audit(ctx, tx, p, id, "CONNECTOR_TOOLS_SELECTED", trace)
	}
	if err == nil {
		c, err = load(ctx, tx, p, id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return c, err
}
