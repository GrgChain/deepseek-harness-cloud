package instances

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

type OperationError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type OperationView struct {
	ID           string          `json:"id"`
	InstanceID   string          `json:"instance_id"`
	Type         string          `json:"type"`
	Status       string          `json:"status"`
	Stage        string          `json:"stage"`
	RetryOf      *string         `json:"retry_of"`
	BuildID      *string         `json:"build_id"`
	TraceID      string          `json:"trace_id"`
	Error        *OperationError `json:"error"`
	RetryAllowed bool            `json:"retry_allowed"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func operationError(code, trace string) *OperationError {
	messages := map[string]string{"MODEL_UNAVAILABLE": "所选模型尚未就绪，请在控制台确认模型校验通过后重试，无需在工作空间重新连接", "WORKSPACE_UNAVAILABLE": "计算服务暂不可用，请按操作状态恢复", "STORAGE_MISSING": "原数据资产不可用，请联系管理员核实", "CONFIGURATION_INVALID": "模型配置不可用，请更新配置", "OPERATION_UNCERTAIN": "外部结果待对账，请勿重复创建", "DEPENDENCY_UNAVAILABLE": "平台依赖暂不可用", "INTERNAL_ERROR": "操作未完成，请通过请求编号联系管理员", "AUTH_REVOKED": "实例所属账户已失效，计算资源已停止", "CREDENTIAL_REVOKED": "模型凭据已撤销，计算资源已停止"}
	message, ok := messages[code]
	if !ok {
		code = "INTERNAL_ERROR"
		message = messages[code]
	}
	return &OperationError{Code: code, Message: message, RequestID: trace}
}

func (s *QueryService) Operation(ctx context.Context, p identity.Principal, id string) (OperationView, error) {
	tx, _, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return OperationView{}, err
	}
	defer tx.Rollback(context.Background())
	if !idPattern.MatchString(id) {
		return OperationView{}, invalidQuery()
	}
	var view OperationView
	var code *string
	err = tx.QueryRow(ctx, `SELECT op.id::text,op.instance_id::text,op.type,op.status,op.stage,op.retry_of::text,op.build_id::text,op.trace_id,op.error_code,op.created_at,op.updated_at FROM operations op JOIN instances i ON i.id=op.instance_id WHERE i.org_id=$1 AND i.owner_id=$2 AND op.id=$3`, p.OrganizationID, p.UserID, id).Scan(&view.ID, &view.InstanceID, &view.Type, &view.Status, &view.Stage, &view.RetryOf, &view.BuildID, &view.TraceID, &code, &view.CreatedAt, &view.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, queryNotFound()
	}
	if err != nil {
		return view, err
	}
	view.CreatedAt = view.CreatedAt.UTC()
	view.UpdatedAt = view.UpdatedAt.UTC()
	if code != nil {
		view.Error = operationError(*code, view.TraceID)
	}
	view.RetryAllowed, err = retryPreview(ctx, tx, p, id)
	if err != nil {
		return view, err
	}
	return view, tx.Commit(ctx)
}
