package contextservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func validateAction(action string, in Action) error {
	if in.SessionRequest != nil {
		return invalid("原生会话请求仅允许已授权的工作空间通道")
	}
	if !readAction(action) && !remoteWrite(action) && action != "verify" && action != "preview" {
		return missing()
	}
	maxRecordID := 128
	if action == "session-get" {
		maxRecordID = 2048
	}
	maxPackage := 2 << 20
	if action == "preview" {
		maxPackage = 16 << 20
	}
	if in.ExpectedVersion < 1 || len(in.Query) > 4000 || len(in.Content) > 256<<10 || len(in.Title) > 200 || len(in.Source) > 1000 || len(in.Token) > 128 || len(in.RecordID) > maxRecordID || len(in.Type) > 100 || len(in.Package) > maxPackage || !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) {
		return invalid("请求内容或版本不正确")
	}
	if in.Limit < 0 || in.Limit > 20 {
		return invalid("返回条数须为 1～20")
	}
	if in.Page < 0 || in.Page > 1000000 || in.PageSize < 0 || in.PageSize > 100 || (in.Page != 0 || in.PageSize != 0) && (action != "list" || in.Page < 1 || in.PageSize < 1) {
		return invalid("分页参数不正确")
	}
	if action == "search" && strings.TrimSpace(in.Query) == "" {
		return invalid("请输入检索问题")
	}
	if in.SearchMode != "" && (action != "search" || (in.SearchMode != "semantic" && in.SearchMode != "exact" && in.SearchMode != "keyword" && in.SearchMode != "hybrid")) {
		return invalid("检索方式不正确")
	}
	if in.MinSimilarity != nil && (action != "search" || in.SearchMode == "exact" || in.SearchMode == "keyword" || math.IsNaN(*in.MinSimilarity) || math.IsInf(*in.MinSimilarity, 0) || *in.MinSimilarity < -1 || *in.MinSimilarity > 1) {
		return invalid("最低相关度须在 -1 到 1 之间，仅适用于语义或混合检索")
	}
	if in.MemorySource != "" && (action != "search" || in.MemorySource != "automatic" && in.MemorySource != "manual") {
		return invalid("记忆来源须为自动生成或手动添加")
	}
	if remoteWrite(action) && (!endpointPattern.MatchString(in.IdempotencyKey) || !in.Confirmed) {
		return invalid("请确认操作并提供唯一操作标识")
	}
	return nil
}

// Call records remote mutations before sending them. Unknown deletes suppress
// reads; pending indexing may serve the last usable version. Reconciliation
// replays the original request because a timeout does not prove rollback.
func (s *Service) Call(ctx context.Context, p identity.Principal, kind, id, action string, in Action, trace string) (json.RawMessage, error) {
	// Existing workspace memory tools read search citations through get. Route
	// native result URIs through the same constrained reader as the portal.
	if kind == "memory" && action == "get" && strings.HasPrefix(in.RecordID, "viking://") {
		action = "session-get"
	}
	if err := validateAction(action, in); err != nil {
		return nil, err
	}
	if kind != "knowledge" && in.SearchMode != "" {
		return nil, invalid("此服务不支持该检索方式")
	}
	if kind != "knowledge" && (in.Page != 0 || in.PageSize != 0) {
		return nil, invalid("此服务不支持文档分页")
	}
	write := !readAction(action)
	tx, err := s.begin(ctx, p, kind, write)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	v, err := load(ctx, tx, p, kind, id)
	if err != nil {
		return nil, err
	}
	if in.MemorySource != "" && (kind != "memory" || v.Backend != "openviking" && v.Backend != "mem0" && v.Backend != "graphiti") {
		return nil, invalid("此服务不支持记忆来源筛选")
	}
	if (action == "session-list" || action == "session-get") && (kind != "memory" || v.Backend != "openviking") {
		return nil, missing()
	}
	if len(in.Package) > 2<<20 && (kind != "memory" || action != "preview") {
		return nil, invalid("迁移包不能超过 2 MiB")
	}
	if in.ExpectedVersion != v.Version {
		return nil, conflict()
	}
	if !v.Enabled {
		return nil, webapi.Fail(409, "CONTEXT_DISABLED", "连接已停用")
	}
	if action != "verify" && v.Status != "READY" {
		return nil, invalid("请先验证外部连接")
	}
	e, err := s.endpoint(v.EndpointID, v.Backend)
	if err != nil {
		return nil, err
	}
	// One corpus per connection, shared by every authorized workspace.
	// Retain the historical storage key so existing shared content remains readable.
	in.Scope = "personal"
	in.Scope, err = scope(ctx, tx, p, in.Scope)
	if err != nil {
		return nil, err
	}
	if in.Limit == 0 {
		in.Limit = 5
	}
	operationID := ""
	if remoteWrite(action) {
		reqBytes, _ := json.Marshal(struct {
			Action string
			Input  Action
		}{action, in})
		requestHash := sha256.Sum256(reqBytes)
		idemHash := sha256.Sum256([]byte(in.IdempotencyKey))
		var storedHash []byte
		var status string
		var envelope security.Envelope
		err = tx.QueryRow(ctx, `SELECT id::text,request_sha256,status,key_id,nonce,ciphertext FROM context_operations WHERE connection_id=$1 AND idempotency_sha256=$2`, id, idemHash[:]).Scan(&operationID, &storedHash, &status, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
		if err == nil {
			if !bytes.Equal(storedHash, requestHash[:]) {
				return nil, conflict()
			}
			if status == "SUCCEEDED" {
				data, err := s.cipher.Decrypt(envelope, s.aad(p, operationID, "context-operation"))
				return data, err
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			if err = pending(ctx, tx, id); err != nil {
				return nil, err
			}
			operationID = webapi.ID()
			envelope, err = s.cipher.Encrypt(reqBytes, s.aad(p, operationID, "context-operation"))
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO context_operations(id,org_id,owner_id,connection_id,connection_version,action,idempotency_sha256,request_sha256,status,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'PENDING',$9,$10,$11)`, operationID, p.OrganizationID, p.UserID, id, v.Version, action, idemHash[:], requestHash[:], envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	} else if readAction(action) {
		var deleting bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM context_operations WHERE connection_id=$1 AND action='delete' AND status IN ('PENDING','UNKNOWN'))`, id).Scan(&deleting); err != nil {
			return nil, err
		}
		if deleting {
			return nil, webapi.Fail(409, "CONTEXT_PENDING", "外部删除尚未确认，请先核实待确认操作")
		}
	} else if err = pending(ctx, tx, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	result, remoteErr := s.remote(ctx, e, v, p, action, in)
	// Persist the remote outcome even if the browser disconnected. Then recheck
	// the current principal and configuration before returning any external text.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	finish, err := s.pool.Begin(finishCtx)
	if err != nil {
		return nil, unavailable()
	}
	defer finish.Rollback(context.Background())
	if operationID != "" {
		status := "SUCCEEDED"
		outcome := "SUCCEEDED"
		if remoteErr != nil {
			status = "UNKNOWN"
			outcome = "ACCEPTED"
			var problem *webapi.Problem
			if errors.As(remoteErr, &problem) && problem.Status >= 400 && problem.Status < 500 {
				status = "FAILED"
				outcome = "FAILED"
			}
			_, err = finish.Exec(finishCtx, `UPDATE context_operations SET status=$2,updated_at=clock_timestamp() WHERE id=$1 AND status<>'SUCCEEDED'`, operationID, status)
		} else {
			envelope, encryptErr := s.cipher.Encrypt(result, s.aad(p, operationID, "context-operation"))
			if encryptErr != nil {
				return nil, encryptErr
			}
			_, err = finish.Exec(finishCtx, `UPDATE context_operations SET status=$2,key_id=$3,nonce=$4,ciphertext=$5,updated_at=clock_timestamp() WHERE id=$1`, operationID, status, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
		}
		if err != nil {
			return nil, err
		}
		if trace == "" {
			trace = webapi.ID()
		}
		if _, err = finish.Exec(finishCtx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id) VALUES($1,$2,$3,$3,$4,$5,$6,$7)`, webapi.ID(), p.OrganizationID, p.UserID, id, "CONTEXT_"+strings.ToUpper(action), outcome, trace); err != nil {
			return nil, err
		}

	}
	if err = finish.Commit(finishCtx); err != nil {
		return nil, err
	}
	check, err := s.begin(ctx, p, kind, write)
	if err != nil {
		return nil, err
	}
	defer check.Rollback(context.Background())
	current, err := load(ctx, check, p, kind, id)
	if err != nil {
		return nil, err
	}
	if current.Version != v.Version || !current.Enabled {
		return nil, conflict()
	}
	if _, err = s.endpoint(v.EndpointID, v.Backend); err != nil {
		return nil, err
	}
	if remoteErr != nil {
		if action == "verify" {
			_, _ = check.Exec(ctx, `UPDATE context_connections SET status='FAILED',verified_at=NULL WHERE id=$1`, id)
			_ = check.Commit(ctx)
		}
		var problem *webapi.Problem
		if operationID != "" && (!errors.As(remoteErr, &problem) || problem.Status >= 500) {
			return nil, webapi.Fail(503, "CONTEXT_PENDING", "外部服务正在处理，请稍后点击“核实待确认操作”")
		}
		return nil, remoteErr
	}
	if action == "verify" {
		var verified struct {
			Contract     string          `json:"contract"`
			Backend      string          `json:"backend"`
			Ready        bool            `json:"ready"`
			Capabilities json.RawMessage `json:"capabilities"`
		}
		if json.Unmarshal(result, &verified) != nil || verified.Contract != Contract || verified.Backend != v.Backend || !verified.Ready || len(verified.Capabilities) == 0 {
			_, _ = check.Exec(ctx, `UPDATE context_connections SET status='FAILED',verified_at=NULL WHERE id=$1`, id)
			_ = check.Commit(ctx)
			return nil, webapi.Fail(422, "CONTEXT_UNSUPPORTED", "服务或版本未通过适配验证")
		}
		if _, err = check.Exec(ctx, `UPDATE context_connections SET status='READY',capabilities=$2,verified_at=clock_timestamp() WHERE id=$1`, id, verified.Capabilities); err != nil {
			return nil, err
		}
		if err = audit(ctx, check, p, id, "CONTEXT_VERIFY", trace); err != nil {
			return nil, err
		}
	}
	if err = check.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Reconcile never accepts new content or an arbitrary external operation ID.
func (s *Service) Reconcile(ctx context.Context, p identity.Principal, kind, id, trace string) (json.RawMessage, error) {
	tx, err := s.begin(ctx, p, kind, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = load(ctx, tx, p, kind, id); err != nil {
		return nil, err
	}
	var operationID string
	var envelope security.Envelope
	err = tx.QueryRow(ctx, `SELECT id::text,key_id,nonce,ciphertext FROM context_operations WHERE org_id=$1 AND owner_id=$2 AND connection_id=$3 AND status IN ('PENDING','UNKNOWN') ORDER BY created_at LIMIT 1`, p.OrganizationID, p.UserID, id).Scan(&operationID, &envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	}
	if err != nil {
		return nil, err
	}
	data, err := s.cipher.Decrypt(envelope, s.aad(p, operationID, "context-operation"))
	if err != nil {
		return nil, err
	}
	var saved struct {
		Action string
		Input  Action
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Call(ctx, p, kind, id, saved.Action, saved.Input, trace)
}
