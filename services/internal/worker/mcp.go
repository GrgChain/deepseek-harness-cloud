package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/connectors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspaceextensions"
)

type runtimeMCPServer struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Version        int64             `json:"version"`
	CatalogVersion int64             `json:"catalog_version"`
	Tools          []connectors.Tool `json:"tools"`
}

func (b *ExtensionBroker) publishInstanceMCP(ctx context.Context, instance string) {
	root, err := os.OpenRoot(filepath.Join(b.root, instance))
	if err != nil {
		return
	}
	defer root.Close()
	var generation string
	if b.pool.QueryRow(ctx, `SELECT runtime_generation::text FROM access_routes WHERE instance_id=$1 AND enabled`, instance).Scan(&generation) != nil {
		return
	}
	_ = b.publishMCP(ctx, instance, root, generation)
}

// Only the catalogs of servers explicitly bound to this workspace cross the
// private channel. Every invocation still checks current ownership and grants.
func (b *ExtensionBroker) mcpServers(ctx context.Context, instance string) ([]runtimeMCPServer, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	p := identity.Principal{WorkspaceID: instance}
	err = tx.QueryRow(ctx, `SELECT org_id::text,owner_id::text,auth_generation FROM workspace_extensions WHERE instance_id=$1`, instance).Scan(&p.OrganizationID, &p.UserID, &p.Generation)
	if err != nil {
		return nil, err
	}
	if _, err = identity.LockPrincipal(ctx, tx, p); err != nil {
		return nil, err
	}
	snapshot, err := workspaceextensions.Load(ctx, tx, p.OrganizationID, p.UserID, instance)
	if err != nil {
		return nil, err
	}
	servers := []runtimeMCPServer{}
	for _, item := range snapshot.Items {
		if item.Kind != "connector" {
			continue
		}
		valid, err := workspaceextensions.Current(ctx, tx, p, instance, p.Generation, item)
		if err != nil {
			return nil, err
		}
		if !valid {
			continue
		}
		server := runtimeMCPServer{ID: item.ID}
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT name,version,catalog_version,tools FROM mcp_connectors WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND deleted_at IS NULL`, item.ID, p.OrganizationID, p.UserID).Scan(&server.Name, &server.Version, &server.CatalogVersion, &raw)
		if err != nil {
			return nil, err
		}
		var tools []connectors.Tool
		if err = json.Unmarshal(raw, &tools); err != nil {
			return nil, err
		}
		server.Tools = []connectors.Tool{}
		for _, tool := range tools {
			if tool.Enabled {
				server.Tools = append(server.Tools, tool)
			}
		}
		servers = append(servers, server)
	}
	return servers, tx.Commit(ctx)
}

func (b *ExtensionBroker) publishMCP(ctx context.Context, instance string, root *os.Root, generation string) error {
	servers, err := b.mcpServers(ctx, instance)
	if err != nil {
		// Withdraw the previous catalog on revoked ownership or unavailable auth.
		_ = root.Remove(".hc-mcp-tools.json")
		return err
	}
	raw, err := json.Marshal(map[string]any{"generation": generation, "expires_at": time.Now().Add(15 * time.Second).UnixMilli(), "servers": servers})
	if err != nil {
		return err
	}
	temp := ".hc-mcp-" + webapi.ID()
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temp, ".hc-mcp-tools.json")
}

// Refresh bound servers without blocking the private invocation channel. A
// failed discovery retains its last catalog; retries resume on the next sweep.
func (b *ExtensionBroker) refreshMCP(ctx context.Context) {
	rows, err := b.pool.Query(ctx, `SELECT DISTINCT ON(c.id) c.id::text,c.org_id::text,c.owner_id::text,e.auth_generation,c.version,e.instance_id::text FROM mcp_connectors c JOIN memberships m ON m.org_id=c.org_id AND m.user_id=c.owner_id JOIN workspace_extensions e ON e.org_id=c.org_id AND e.owner_id=c.owner_id JOIN instances i ON i.id=e.instance_id WHERE c.enabled AND c.deleted_at IS NULL AND m.status='ACTIVE' AND e.auth_generation=m.auth_generation AND i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND i.deleted_at IS NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(e.configuration->'items') item WHERE item->>'kind'='connector' AND item->>'id'=c.id::text) ORDER BY c.id,e.instance_id`)
	if err != nil {
		return
	}
	type target struct {
		id        string
		principal identity.Principal
		version   int64
	}
	targets := []target{}
	for rows.Next() {
		var t target
		if rows.Scan(&t.id, &t.principal.OrganizationID, &t.principal.UserID, &t.principal.Generation, &t.version, &t.principal.WorkspaceID) == nil {
			targets = append(targets, t)
		}
	}
	rows.Close()
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		_, _ = b.connectors.Sync(ctx, t.principal, t.id, t.version, "")
	}
}
