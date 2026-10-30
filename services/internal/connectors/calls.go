package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type InvokeInput struct {
	ExpectedVersion int64          `json:"expected_version"`
	CatalogVersion  int64          `json:"catalog_version"`
	Tool            string         `json:"tool"`
	Arguments       map[string]any `json:"arguments"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Confirmed       bool           `json:"confirmed"`
	RiskConfirmed   bool           `json:"risk_confirmed"`
	Timeout         int            `json:"timeout_seconds"`
}
type Call struct {
	ID             string              `json:"id"`
	ConnectionID   string              `json:"connector_id"`
	Version        int64               `json:"connector_version"`
	CatalogVersion int64               `json:"catalog_version"`
	Tool           string              `json:"tool"`
	Status         string              `json:"status"`
	Reason         string              `json:"reason"`
	CreatedAt      time.Time           `json:"created_at"`
	CompletedAt    *time.Time          `json:"completed_at"`
	Result         *mcp.CallToolResult `json:"result,omitempty"`
	Replayed       bool                `json:"replayed,omitempty"`
	requestHash    string
}

const callColumns = `id::text,connector_id::text,connector_version,catalog_version,tool_name,CASE WHEN status='RUNNING' AND created_at+make_interval(secs=>timeout_seconds+5)<clock_timestamp() THEN 'UNKNOWN' ELSE status END,reason,created_at,completed_at,request_sha256`

func scanCall(row pgx.Row) (Call, error) {
	var c Call
	err := row.Scan(&c.ID, &c.ConnectionID, &c.Version, &c.CatalogVersion, &c.Tool, &c.Status, &c.Reason, &c.CreatedAt, &c.CompletedAt, &c.requestHash)
	if err == pgx.ErrNoRows {
		err = missing()
	}
	return c, err
}
func (s *Service) GetCall(ctx context.Context, p identity.Principal, id string) (Call, error) {
	tx, err := s.begin(ctx, p, "connectors:read")
	if err != nil {
		return Call{}, err
	}
	defer tx.Rollback(context.Background())
	if !uuidPattern.MatchString(id) {
		return Call{}, missing()
	}
	return scanCall(tx.QueryRow(ctx, `SELECT `+callColumns+` FROM mcp_calls WHERE org_id=$1 AND owner_id=$2 AND id=$3`, p.OrganizationID, p.UserID, id))
}
func (s *Service) Calls(ctx context.Context, p identity.Principal, id string, offset, limit int) (any, error) {
	tx, err := s.begin(ctx, p, "connectors:read")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = load(ctx, tx, p, id); err != nil {
		return nil, err
	}
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_calls WHERE org_id=$1 AND owner_id=$2 AND connector_id=$3`, p.OrganizationID, p.UserID, id).Scan(&count)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+callColumns+` FROM mcp_calls WHERE org_id=$1 AND owner_id=$2 AND connector_id=$3 ORDER BY created_at DESC,id LIMIT $4 OFFSET $5`, p.OrganizationID, p.UserID, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Call{}
	for rows.Next() {
		c, e := scanCall(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, c)
	}
	return map[string]any{"items": items, "total": count}, rows.Err()
}
func (s *Service) Cancel(ctx context.Context, p identity.Principal, id, trace string) error {
	tx, err := s.begin(ctx, p, "connectors:invoke")
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if !uuidPattern.MatchString(id) {
		return missing()
	}
	var cid string
	err = tx.QueryRow(ctx, `UPDATE mcp_calls SET cancel_requested=true WHERE id=$1 AND org_id=$2 AND owner_id=$3 AND status='RUNNING' RETURNING connector_id::text`, id, p.OrganizationID, p.UserID).Scan(&cid)
	if err == pgx.ErrNoRows {
		return missing()
	}
	if err == nil {
		err = audit(ctx, tx, p, id, "CONNECTOR_CALL_CANCEL_REQUESTED", trace)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		if value, ok := s.active.Load(id); ok {
			value.(activeCall).cancel()
		}
	}
	return err
}

func risky(t Tool) bool {
	var definition struct {
		Annotations *struct {
			ReadOnly    bool  `json:"readOnlyHint"`
			Destructive *bool `json:"destructiveHint"`
		} `json:"annotations"`
	}
	if json.Unmarshal(t.Definition, &definition) != nil || definition.Annotations == nil {
		return true
	}
	return !definition.Annotations.ReadOnly || definition.Annotations.Destructive == nil || *definition.Annotations.Destructive
}
func (s *Service) Invoke(ctx context.Context, p identity.Principal, id string, in InvokeInput, trace string) (Call, error) {
	if !uuidPattern.MatchString(in.IdempotencyKey) || !in.Confirmed {
		return Call{}, invalid("请确认工具和发送参数后再调用")
	}
	if in.Timeout == 0 {
		in.Timeout = 60
	}
	if in.Timeout < 1 || in.Timeout > 120 || in.Arguments == nil {
		return Call{}, invalid("请提交JSON对象参数及1～120秒超时")
	}
	encoded, err := json.Marshal(in)
	if err != nil || len(encoded) > maxBytes {
		return Call{}, invalid("调用参数不能超过1MiB")
	}
	hash := digest(encoded)
	tx, err := s.begin(ctx, p, "connectors:invoke")
	if err != nil {
		return Call{}, err
	}
	defer tx.Rollback(context.Background())
	previous, previousErr := scanCall(tx.QueryRow(ctx, `SELECT `+callColumns+` FROM mcp_calls WHERE org_id=$1 AND owner_id=$2 AND idempotency_key=$3`, p.OrganizationID, p.UserID, in.IdempotencyKey))
	if previousErr == nil {
		if previous.requestHash != hash || previous.ConnectionID != id {
			return Call{}, webapi.Fail(409, "IDEMPOTENCY_CONFLICT", "此提交标识已用于其他调用")
		}
		previous.Replayed = true
		return previous, nil
	}
	if problem, ok := previousErr.(*webapi.Problem); !ok || problem.Code != "NOT_FOUND" {
		return Call{}, previousErr
	}
	c, err := load(ctx, tx, p, id)
	if err != nil {
		return Call{}, err
	}
	if c.Version != in.ExpectedVersion || c.CatalogVersion != in.CatalogVersion {
		return Call{}, conflict()
	}
	if !c.Enabled || c.Status != "READY" {
		return Call{}, invalid("连接器尚未就绪，请重新验证")
	}
	var tool *Tool
	for i := range c.Tools {
		if c.Tools[i].Name == in.Tool && c.Tools[i].Enabled {
			tool = &c.Tools[i]
		}
	}
	if tool == nil {
		return Call{}, invalid("此工具未启用或已移除")
	}
	if risky(*tool) && !in.RiskConfirmed {
		return Call{}, invalid("此工具可能修改外部数据，请再次确认")
	}
	schema, err := schemaFor(tool.Schema)
	if err != nil || schema.Validate(in.Arguments) != nil {
		return Call{}, invalid("参数不符合此工具的JSON Schema，请检查必填项和类型")
	}
	var active, connectionActive int
	var recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_calls WHERE org_id=$1 AND owner_id=$2 AND created_at>clock_timestamp()-interval '1 minute'`, p.OrganizationID, p.UserID).Scan(&recent); err != nil {
		return Call{}, err
	}
	if recent >= 30 {
		return Call{}, webapi.Fail(429, "CONNECTOR_BUSY", "工具调用过于频繁，请稍后重试")
	}
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE connector_id=$3) FROM mcp_calls WHERE org_id=$1 AND owner_id=$2 AND status='RUNNING' AND created_at+make_interval(secs=>timeout_seconds+5)>clock_timestamp()`, p.OrganizationID, p.UserID, id).Scan(&active, &connectionActive)
	if err != nil {
		return Call{}, err
	}
	if active >= 3 || connectionActive >= 1 {
		return Call{}, webapi.Fail(409, "CONNECTOR_BUSY", "已有工具正在执行，请等待或取消后再试")
	}
	e, err := s.connectionEndpoint(c)
	if err != nil {
		return Call{}, err
	}
	credential, err := s.credential(ctx, tx, p, c)
	if err != nil {
		if errors.Is(err, errAuth) {
			_, updateErr := tx.Exec(ctx, `UPDATE mcp_connectors SET status='AUTH_REQUIRED' WHERE id=$1`, id)
			if updateErr == nil {
				updateErr = tx.Commit(ctx)
			}
			if updateErr != nil {
				return Call{}, updateErr
			}
			return Call{}, invalid("授权已失效，请重新授权连接器")
		}
		return Call{}, err
	}
	call := Call{ID: webapi.ID(), ConnectionID: id, Version: c.Version, CatalogVersion: c.CatalogVersion, Tool: in.Tool, Status: "RUNNING"}
	_, err = tx.Exec(ctx, `INSERT INTO mcp_calls(id,org_id,owner_id,connector_id,connector_version,catalog_version,tool_name,request_sha256,idempotency_key,status,timeout_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'RUNNING',$10)`, call.ID, p.OrganizationID, p.UserID, id, c.Version, c.CatalogVersion, in.Tool, hash, in.IdempotencyKey, in.Timeout)
	if err == nil {
		err = audit(ctx, tx, p, call.ID, "CONNECTOR_CALL_STARTED", trace)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return Call{}, err
	}
	work, stop := context.WithTimeout(ctx, time.Duration(in.Timeout)*time.Second)
	defer stop()
	s.active.Store(call.ID, activeCall{p.UserID, id, stop})
	defer s.active.Delete(call.ID)
	// A new SDK session discovers the actual definition before dispatch. No tool
	// arguments are sent unless the immutable user-approved definition still matches.
	discovery, endDiscovery := context.WithTimeout(work, 10*time.Second)
	session, remoteErr := openSession(discovery, e, credential, c.Protocol)
	if remoteErr == nil {
		var live []Tool
		live, remoteErr = discover(discovery, session)
		if remoteErr == nil {
			found := false
			for _, v := range live {
				if v.Name == tool.Name && v.Hash == tool.Hash {
					found = true
				}
			}
			if !found {
				remoteErr = conflict()
			}
		}
	}
	endDiscovery()
	if remoteErr == nil {
		check, checkErr := s.begin(work, p, "connectors:invoke")
		if checkErr == nil {
			latest, le := load(work, check, p, id)
			if le != nil {
				checkErr = le
			} else if latest.Version != c.Version || !latest.Enabled || latest.Status != "READY" {
				checkErr = conflict()
			}
			var cancelled bool
			if checkErr == nil {
				checkErr = check.QueryRow(work, `SELECT cancel_requested FROM mcp_calls WHERE id=$1`, call.ID).Scan(&cancelled)
				if cancelled {
					checkErr = invalid("调用已申请取消")
				}
			}
			if checkErr == nil {
				var approved Endpoint
				approved, checkErr = s.connectionEndpoint(c)
				if checkErr == nil && approved != e {
					checkErr = conflict()
				}
			}
			check.Rollback(context.Background())
		}
		remoteErr = checkErr
	}
	dispatched := false
	var result *mcp.CallToolResult
	if remoteErr == nil {
		dispatched = true
		result, remoteErr = session.CallTool(work, &mcp.CallToolParams{Name: in.Tool, Arguments: in.Arguments})
	}
	if session != nil {
		session.Close()
	}
	call.Status = "SUCCEEDED"
	if remoteErr != nil {
		if dispatched {
			call.Status = "UNKNOWN"
			call.Reason = "请求已发出但结果未确认，请核对外部系统，勿重复提交"
		} else {
			call.Status = "REJECTED"
			call.Reason = "未发送工具参数；连接、目录或授权校验失败，请刷新验证"
		}
	} else if result == nil || result.NeedsInput() {
		call.Status = "UNKNOWN"
		call.Reason = "服务需要额外交互，本阶段不自动继续，请核对外部状态"
	} else if result.IsError {
		call.Status = "TOOL_ERROR"
		call.Reason = "外部工具返回执行错误"
	}
	finish, finishStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishStop()
	completion, err := s.pool.Begin(finish)
	if err != nil {
		return Call{}, unavailable()
	}
	defer completion.Rollback(context.Background())
	_, err = completion.Exec(finish, `UPDATE mcp_calls SET status=$2,reason=$3,completed_at=clock_timestamp() WHERE id=$1`, call.ID, call.Status, call.Reason)
	if err == nil {
		outcome := "SUCCEEDED"
		if call.Status == "UNKNOWN" {
			outcome = "ACCEPTED"
		} else if call.Status == "REJECTED" {
			outcome = "DENIED"
		} else if call.Status == "TOOL_ERROR" {
			outcome = "FAILED"
		}
		_, err = completion.Exec(finish, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$3,$4,'CONNECTOR_CALL_FINISHED',$5,$6,jsonb_build_object('call_status',$7::text))`, webapi.ID(), p.OrganizationID, p.UserID, call.ID, outcome, trace, call.Status)
	}
	if err == nil {
		err = completion.Commit(finish)
	}
	if err != nil {
		return Call{}, unavailable()
	}
	// Responses also recheck current identity; removed grants must not disclose
	// an in-flight result. Only status is durable, never arguments or result text.
	checked, err := s.GetCall(ctx, p, call.ID)
	if err != nil {
		return Call{}, err
	}
	current, err := s.begin(ctx, p, "connectors:invoke")
	if err != nil {
		return Call{}, err
	}
	latest, loadErr := load(ctx, current, p, id)
	current.Rollback(context.Background())
	if loadErr != nil || latest.Version != c.Version || !latest.Enabled {
		return checked, nil
	}
	checked.Result = result
	return checked, nil
}
