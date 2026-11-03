package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
