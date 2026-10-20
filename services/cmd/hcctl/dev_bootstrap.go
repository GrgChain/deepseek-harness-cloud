package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

// Local startup uses the operations identity to fill missing data before the
// restricted application processes start. Existing settings and accounts win.
func bootstrapLocal(ctx context.Context) error {
	pool, err := workspacePool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	var database string
	if err = pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil || database != "hc_dev" {
		return errors.New("dev-bootstrap only supports the isolated hc_dev database")
	}
	org := os.Getenv("HC_ORGANIZATION_ID")
	if org == "" || os.Getenv("HC_ENVIRONMENT") != "local" {
		return errors.New("dev-bootstrap requires local identity configuration")
	}
	// Different services may start together. Keep the lock on a dedicated
	// connection while the existing import and approval transactions run.
	lock, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(731804531)`); err != nil {
		return err
	}
	defer lock.Exec(context.Background(), `SELECT pg_advisory_unlock(731804531)`)
	var exists bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1)`, org).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if err = importBootstrapAccounts(ctx, pool, org); err != nil {
			return fmt.Errorf("initialize local accounts: %w", err)
		}
	}
	if _, err = os.Lstat(".local/worker-config.json"); errors.Is(err, os.ErrNotExist) {
		// Login-only installations can initialize the organization pool before
		// a runtime is installed. No template is approved without its proofs.
		return devlocal.InitResources(ctx, pool, org, os.Getenv("HC_OPERATOR_REF"), false)
	} else if err != nil {
		return err
	}
	if err = runWorkspace("workspace-init"); err != nil {
		return fmt.Errorf("initialize workspace authority: %w", err)
	}
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approved_templates WHERE enabled)`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if err = runWorkspace("workspace-template"); err != nil {
			return fmt.Errorf("initialize workspace template: %w", err)
		}
		if err = approveLocalRuntime(ctx); err != nil {
			return fmt.Errorf("initialize verified runtime: %w", err)
		}
	}
	return devlocal.InitResources(ctx, pool, org, os.Getenv("HC_OPERATOR_REF"), true)
}

func importBootstrapAccounts(ctx context.Context, pool *pgxpool.Pool, org string) error {
	raw, err := config.ReadPrivateFile(os.Getenv("HC_IDENTITY_ACCOUNTS_FILE"))
	if err != nil {
		return err
	}
	in, err := identity.ParseAccountImport(raw, org)
	if err != nil {
		return err
	}
	return identity.ImportAccounts(ctx, pool, in, os.Getenv("HC_OIDC_ISSUER"), os.Getenv("HC_OPERATOR_REF"))
}
