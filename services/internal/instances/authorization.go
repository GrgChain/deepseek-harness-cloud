package instances

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func requireAction(ctx context.Context, tx pgx.Tx, p identity.Principal, action string) error {
	permission := "instances:" + strings.ToLower(action)
	if err := identity.RequirePermission(ctx, tx, p, permission); err != nil {
		return err
	}
	// Creation always starts compute, so it also needs running authority.
	if action == "CREATE" {
		return identity.RequirePermission(ctx, tx, p, "instances:start")
	}
	return nil
}
func queryPermissions(ctx context.Context, tx pgx.Tx, p identity.Principal) (identity.Principal, error) {
	err := tx.QueryRow(ctx, `SELECT public.hc_effective_permissions($1,$2)`, p.OrganizationID, p.UserID).Scan(&p.Permissions)
	return p, err
}
func filterCapabilities(p identity.Principal, view *InstanceView) {
	view.CanEnter = view.CanEnter && p.Has("instances:access")
	filtered := []string{}
	for _, action := range view.AllowedActions {
		if p.Has("instances:" + action) {
			filtered = append(filtered, action)
		}
	}
	view.AllowedActions = filtered
}
