package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/connectors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/contextservice"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/workspaceextensions"
)

// ExtensionBroker uses the existing per-instance private runtime bind mount.
// It opens no network listener and never gives a container a platform secret.
type ExtensionBroker struct {
	pool       *pgxpool.Pool
	root       string
	connectors *connectors.Service
	context    *contextservice.Service
	active     sync.Map
	slots      chan struct{}
}

func NewExtensionBroker(pool *pgxpool.Pool, root string, cipher *security.SecretCipher, environment string, maintenance bool) *ExtensionBroker {
	return &ExtensionBroker{pool: pool, root: root, slots: make(chan struct{}, 8), connectors: connectors.NewService(pool, cipher, environment, maintenance, os.Getenv("HC_CONNECTOR_ENDPOINTS_FILE")), context: contextservice.NewService(pool, cipher, environment, maintenance, os.Getenv("HC_CONTEXT_ENDPOINTS_FILE"))}
}

type extensionRequest struct {
	ID         string         `json:"id"`
	Generation string         `json:"generation"`
	Kind       string         `json:"kind"`
	SourceID   string         `json:"source_id"`
	Tool       string         `json:"tool,omitempty"`
	Arguments  map[string]any `json:"arguments,omitempty"`
	Query      string         `json:"query,omitempty"`
	Action     string         `json:"action,omitempty"`
	RecordID   string         `json:"record_id,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	Confirmed  bool           `json:"confirmed,omitempty"`
}

func privateChannelRead(root *os.Root, name string) ([]byte, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrHostIdentity
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Getuid()) {
		return nil, ErrHostIdentity
	}
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if len(raw) > 1<<20 {
		return nil, ErrHostIdentity
	}
	return raw, err
}
func (b *ExtensionBroker) Run(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var tasks sync.WaitGroup
	defer tasks.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			rows, err := b.pool.Query(ctx, `SELECT e.instance_id::text FROM workspace_extensions e JOIN instances i ON i.id=e.instance_id WHERE i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND i.deleted_at IS NULL`)
			if err != nil {
				continue
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				if _, busy := b.active.LoadOrStore(id, true); busy {
					continue
				}
				select {
				case b.slots <- struct{}{}:
					tasks.Add(1)
					go func() {
						defer tasks.Done()
						defer b.active.Delete(id)
						defer func() { <-b.slots }()
						b.process(ctx, id)
					}()
				default:
					b.active.Delete(id)
				}
			}
		}
	}
}
func (b *ExtensionBroker) process(ctx context.Context, instance string) {
	root, err := os.OpenRoot(filepath.Join(b.root, instance))
	if err != nil {
		return
	}
	defer root.Close()
	var generation string
	if err = b.pool.QueryRow(ctx, `SELECT runtime_generation::text FROM access_routes WHERE instance_id=$1 AND enabled`, instance).Scan(&generation); err != nil {
		return
	}
	if raw, e := privateChannelRead(root, ".hc-extensions-ready.json"); e == nil {
		var ready struct {
			Generation string `json:"generation"`
		}
		if webapi.StrictUnmarshal(raw, &ready) == nil && ready.Generation == generation {
			_, _ = b.pool.Exec(ctx, `UPDATE workspace_extensions SET applied_at=clock_timestamp(),applied_generation=$2 WHERE instance_id=$1 AND applied_generation IS DISTINCT FROM $2::uuid`, instance, generation)
		}
	}
	// A persisted processing file is an interrupted intent, retried with its
	// original key. The connector service ledger prevents a duplicate side effect.
	raw, err := privateChannelRead(root, ".hc-extension-processing.json")
	if errors.Is(err, os.ErrNotExist) {
		raw, err = privateChannelRead(root, ".hc-extension-request.json")
		if err == nil {
			err = root.Rename(".hc-extension-request.json", ".hc-extension-processing.json")
		}
	}
	if err != nil {
		return
	}
	defer clear(raw)
	var request extensionRequest
	if webapi.StrictUnmarshal(raw, &request) != nil || !uuidPattern.MatchString(request.ID) || request.Generation != generation {
		_ = root.Remove(".hc-extension-processing.json")
		return
	}
	result, callErr := b.invoke(ctx, instance, request)
	response := struct {
		ID     string `json:"id"`
		Result any    `json:"result,omitempty"`
		Error  string `json:"error,omitempty"`
	}{ID: request.ID, Result: result}
	if callErr != nil {
		response.Result = nil
		response.Error = "所选扩展暂不可用，请检查连接、权限和工作空间状态"
		var problem *webapi.Problem
		if errors.As(callErr, &problem) {
			response.Error = problem.Message
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > 1<<20 {
		encoded, _ = json.Marshal(map[string]string{"id": request.ID, "error": "结果超过限制，请缩小检索范围；外部调用结果请在平台核实"})
	}
	defer clear(encoded)
	temp := ".hc-extension-result-" + webapi.ID()
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return
	}
	_, err = file.Write(encoded)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = root.Remove(temp)
		return
	}
	if root.Rename(temp, ".hc-extension-response.json") != nil {
		_ = root.Remove(temp)
		return
	}
	_ = root.Remove(".hc-extension-processing.json")
}
func (b *ExtensionBroker) invoke(ctx context.Context, instance string, request extensionRequest) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
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
	var selected *workspaceextensions.Item
	for i := range snapshot.Items {
		v := &snapshot.Items[i]
		if v.Kind == request.Kind && v.ID == request.SourceID {
			selected = v
			break
		}
	}
	if selected == nil || selected.Kind == "plugin" {
		return nil, identity.ErrForbidden
	}
	current, err := workspaceextensions.Current(ctx, tx, p, instance, p.Generation, *selected)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, identity.ErrForbidden
	}
	if request.Kind == "connector" && (!request.Confirmed || !slices.Contains(selected.Tools, request.Tool)) {
		return nil, identity.ErrForbidden
	}
	if request.Kind != "connector" && !slices.Contains(selected.Scopes, request.Scope) {
		return nil, identity.ErrForbidden
	}
	if request.Kind != "connector" && request.Action != "" && request.Action != "search" && request.Action != "get" {
		return nil, identity.ErrForbidden
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	var result any
	if request.Kind == "connector" {
		result, err = b.connectors.Invoke(ctx, p, request.SourceID, connectors.InvokeInput{ExpectedVersion: selected.Version, CatalogVersion: selected.CatalogVersion, Tool: request.Tool, Arguments: request.Arguments, IdempotencyKey: request.ID, Confirmed: true, RiskConfirmed: true, Timeout: 60}, request.ID)
	} else {
		action := request.Action
		if action == "" {
			action = "search"
		}
		result, err = b.context.Call(ctx, p, request.Kind, request.SourceID, action, contextservice.Action{ExpectedVersion: selected.Version, Scope: request.Scope, Query: request.Query, RecordID: request.RecordID, Limit: 5}, request.ID)
	}
	if err != nil {
		return nil, err
	}
	// An unbind while an upstream request is in flight also hides its result.
	check, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer check.Rollback(context.Background())
	if _, err = identity.LockPrincipal(ctx, check, p); err != nil {
		return nil, err
	}
	current, err = workspaceextensions.Current(ctx, check, p, instance, p.Generation, *selected)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, identity.ErrForbidden
	}
	if err = check.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
