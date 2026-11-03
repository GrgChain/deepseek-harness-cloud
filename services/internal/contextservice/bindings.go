package contextservice

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func scope(ctx context.Context, tx pgx.Tx, p identity.Principal, value string) (string, error) {
	if value == "" || value == "personal" {
		return "personal", nil
	}
	id := strings.TrimPrefix(value, "workspace:")
	if id == value || !idPattern.MatchString(id) {
		return "", invalid("记忆范围不正确")
	}
	if err := identity.RequirePermission(ctx, tx, p, "instances:read"); err != nil {
		return "", err
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instances WHERE org_id=$1 AND owner_id=$2 AND id=$3)`, p.OrganizationID, p.UserID, id).Scan(&exists)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", missing()
	}
	return "workspace:" + id, nil
}

func (s *Service) Bind(ctx context.Context, p identity.Principal, kind, id, instance string, version, bindingVersion int64, enabled bool, trace string) (Binding, error) {
	b := Binding{InstanceID: instance, ConnectionID: id, Enabled: enabled, Integration: "NOT_CONNECTED"}
	tx, err := s.begin(ctx, p, kind, true)
	if err != nil {
		return b, err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "instances:configure"); err != nil {
		return b, err
	}
	v, err := load(ctx, tx, p, kind, id)
	if err != nil {
		return b, err
	}
	if v.Version != version {
		return b, conflict()
	}
	if enabled && (!v.Enabled || v.Status != "READY") {
		return b, invalid("连接验证通过后才能绑定")
	}
	if !idPattern.MatchString(instance) {
		return b, missing()
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instances WHERE org_id=$1 AND owner_id=$2 AND id=$3 AND observed_state NOT IN ('DELETED','DELETING'))`, p.OrganizationID, p.UserID, instance).Scan(&exists); err != nil {
		return b, err
	}
	if !exists {
		return b, missing()
	}
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT version FROM context_bindings WHERE instance_id=$1 AND connection_id=$2 FOR UPDATE`, instance, id).Scan(&currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return b, err
	}
	if currentVersion != bindingVersion {
		return b, conflict()
	}
	err = tx.QueryRow(ctx, `INSERT INTO context_bindings(org_id,owner_id,instance_id,connection_id,enabled) VALUES($1,$2,$3,$4,$5) ON CONFLICT(instance_id,connection_id) DO UPDATE SET enabled=EXCLUDED.enabled,version=context_bindings.version+1,updated_at=clock_timestamp() RETURNING version`, p.OrganizationID, p.UserID, instance, id, enabled).Scan(&b.Version)
	if err != nil {
		return b, err
	}
	if err = audit(ctx, tx, p, id, "CONTEXT_BINDING_SAVE", trace); err != nil {
		return b, err
	}
	return b, tx.Commit(ctx)
}
