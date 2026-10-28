package identity

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// LockPrincipal rechecks a current Portal session and membership inside a short
// application transaction. Its returned role comes from PostgreSQL, never a DTO.
func LockPrincipal(ctx context.Context, tx pgx.Tx, p Principal) (string, error) {
	if p.WorkspaceID != "" {
		var role *string
		err := tx.QueryRow(ctx, `SELECT public.hc_extensions_lock_authority($1,$2,$3,$4)`, p.OrganizationID, p.UserID, p.WorkspaceID, p.Generation).Scan(&role)
		if err != nil {
			return "", err
		}
		if role == nil {
			return "", ErrUnauthenticated
		}
		return *role, nil
	}
	var role string
	var current *string
	err := tx.QueryRow(ctx, `SELECT public.hc_access_lock_principal($1,$2,$3,$4)`, p.OrganizationID, p.UserID, p.SessionID, p.Generation).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	if current == nil {
		return "", ErrUnauthenticated
	}
	role = *current
	return role, nil
}
