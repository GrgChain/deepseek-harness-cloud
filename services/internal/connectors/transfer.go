package connectors

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Config struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Protocol    string `json:"protocol_version"`
	Auth        string `json:"auth_type"`
	CatalogID   string `json:"catalog_id,omitempty"`
}
type Package struct {
	Format      string   `json:"format"`
	Connections []Config `json:"connections"`
}
type ImportItem struct {
	Config
	EndpointID string `json:"endpoint_id"`
	Conflict   bool   `json:"conflict"`
}
type ImportPreview struct {
	Items    []ImportItem `json:"items"`
	Warnings []string     `json:"warnings"`
	Imported int          `json:"imported"`
}

func parsePackage(raw json.RawMessage) (Package, []string, error) {
	var data map[string]json.RawMessage
	if len(raw) > maxBytes || json.Unmarshal(raw, &data) != nil {
		return Package{}, nil, invalid("请选择不超过1MiB的JSON配置")
	}
	warnings := []string{"导入不保留凭据，工具默认关闭；保存后请填写认证并验证。"}
	var result Package
	if legacy, ok := data["mcpServers"]; ok {
		if len(data) != 1 {
			return result, nil, invalid("mcpServers不能混合其他配置格式")
		}
		var servers map[string]json.RawMessage
		if json.Unmarshal(legacy, &servers) != nil {
			return result, nil, invalid("mcpServers格式不正确")
		}
		result.Format = "hc-mcp-v1"
		for name, entry := range servers {
			var server map[string]json.RawMessage
			if json.Unmarshal(entry, &server) != nil {
				return result, nil, invalid("MCP服务配置不正确")
			}
			if _, exists := server["command"]; exists {
				return result, nil, invalid("暂不支持stdio命令，请导入远程MCP服务")
			}
			if _, exists := server["args"]; exists {
				return result, nil, invalid("暂不支持启动参数")
			}
			var url, typ string
			json.Unmarshal(server["url"], &url)
			json.Unmarshal(server["type"], &typ)
			if typ != "" && typ != "http" && typ != "streamable-http" {
				return result, nil, invalid("仅支持Streamable HTTP远程配置")
			}
			auth := "none"
			if _, ok := server["headers"]; ok {
				auth = "bearer"
				warnings = append(warnings, "已忽略导入的认证头，须重新填写Bearer凭据。")
			}
			for key := range server {
				if key != "url" && key != "type" && key != "headers" {
					warnings = append(warnings, "已忽略不支持的服务配置字段："+key)
				}
			}
			result.Connections = append(result.Connections, Config{Name: name, URL: url, Protocol: "2026-07-28", Auth: auth})
		}
	} else if webapi.StrictUnmarshal(raw, &result) != nil || result.Format != "hc-mcp-v1" {
		return result, nil, invalid("请选择hc-mcp-v1或mcpServers配置；配置不得含凭据字段")
	}
	if len(result.Connections) == 0 || len(result.Connections) > 50 {
		return result, nil, invalid("每次导入1～50个连接器")
	}
	return result, warnings, nil
}
func (s *Service) Export(ctx context.Context, p identity.Principal) (Package, error) {
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return Package{}, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND deleted_at IS NULL ORDER BY name,id`, p.OrganizationID, p.UserID)
	if err != nil {
		return Package{}, err
	}
	defer rows.Close()
	result := Package{Format: "hc-mcp-v1", Connections: []Config{}}
	for rows.Next() {
		c, e := scan(rows)
		if e != nil {
			return result, e
		}
		endpoint, e := s.connectionEndpoint(c)
		if e != nil {
			return result, e
		}
		result.Connections = append(result.Connections, Config{Name: c.Name, Description: c.Description, URL: endpoint.URL, Protocol: c.Protocol, Auth: c.Auth, CatalogID: endpoint.CatalogID})
	}
	return result, rows.Err()
}
func (s *Service) Import(ctx context.Context, p identity.Principal, raw json.RawMessage, confirmed bool, trace string) (ImportPreview, error) {
	pkg, warnings, err := parsePackage(raw)
	result := ImportPreview{Items: []ImportItem{}, Warnings: warnings}
	if err != nil {
		return result, err
	}
	tx, err := s.begin(ctx, p, "connectors:manage")
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	seen := map[string]bool{}
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND deleted_at IS NULL`, p.OrganizationID, p.UserID).Scan(&count)
	if err != nil {
		return result, err
	}
	for _, config := range pkg.Connections {
		input := Input{Name: config.Name, Description: config.Description, URL: config.URL, CatalogID: config.CatalogID, Protocol: config.Protocol, Auth: config.Auth}
		if err = validateInput(input); err != nil {
			return result, err
		}
		if _, err = s.personalEndpoint(config.URL, config.CatalogID); err != nil {
			return result, err
		}
		name := strings.TrimSpace(config.Name)
		if seen[name] {
			return result, invalid("导入配置包含重复名称")
		}
		seen[name] = true
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mcp_connectors WHERE org_id=$1 AND owner_id=$2 AND name=$3 AND deleted_at IS NULL)`, p.OrganizationID, p.UserID, name).Scan(&exists)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, ImportItem{config, "personal", exists})
		if exists {
			continue
		}
		count++
		if count > 50 {
			return result, invalid("导入后连接器不能超过50个")
		}
		if !confirmed {
			continue
		}
		id := webapi.ID()
		envelope, e := s.cipher.Encrypt([]byte("-"), s.aad(p, id))
		if e != nil {
			return result, e
		}
		_, err = tx.Exec(ctx, `INSERT INTO mcp_connectors(id,org_id,owner_id,name,description,endpoint_id,protocol_version,auth_type,key_id,nonce,ciphertext,endpoint_url,catalog_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, id, p.OrganizationID, p.UserID, name, config.Description, "personal", config.Protocol, config.Auth, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, config.URL, config.CatalogID)
		if err == nil && config.Auth == "oauth2" {
			_, err = tx.Exec(ctx, `UPDATE mcp_connectors SET status='AUTH_REQUIRED' WHERE id=$1`, id)
		}
		if err == nil {
			err = audit(ctx, tx, p, id, "CONNECTOR_IMPORTED", trace)
		}
		if err != nil {
			return result, err
		}
		result.Imported++
	}
	if confirmed {
		err = tx.Commit(ctx)
	}
	return result, err
}
