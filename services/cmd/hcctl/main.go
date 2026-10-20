package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func run() error {
	if len(os.Args) == 2 && (os.Args[1] == "workspace-init" || os.Args[1] == "workspace-template" || os.Args[1] == "workspace-names-migrate") {
		return runWorkspace(os.Args[1])
	}
	if len(os.Args) >= 2 && isOperationsCommand(os.Args[1]) {
		return runOperations(os.Args[1], os.Args[2:])
	}
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: hcctl dev-init | dev-bootstrap | dev-auth-init | workspace-init | workspace-template | workspace-names-migrate | dev-worker-init | dev-access-init | dev-runtime-approve | dev-runtime-upgrade | dev-runtime-rollback | identity-apply | identity-import | migrate | dev-grants | backup | backup-daily | restore | recovery-inventory | recovery-retain | recovery-adopt | recovery-fence-source | storage-backup | storage-backup-reconcile | storage-hold | storage-restore | storage-reclaim | cleanup | secrets-reencrypt | audit-archive | audit-hold")
	}
	if os.Args[1] == "dev-init" {
		return devlocal.Init(".local")
	}
	if os.Args[1] == "dev-bootstrap" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return bootstrapLocal(ctx)
	}
	if os.Args[1] == "dev-runtime-approve" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return approveLocalRuntime(ctx)
	}
	if os.Args[1] == "dev-auth-init" {
		return devlocal.InitAuth(".local/auth")
	}
	if os.Args[1] == "dev-worker-init" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return initLocalWorker(ctx, os.Getenv("HC_STORAGE_ROOT"), os.Getenv("HC_INSTANCE_DOMAIN"))
	}
	if os.Args[1] == "dev-access-init" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return initLocalAccess(ctx)
	}
	if os.Args[1] == "identity-apply" || os.Args[1] == "identity-import" {
		dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
		if err != nil {
			return err
		}
		path := os.Getenv("HC_IDENTITY_MAPPING_FILE")
		if os.Args[1] == "identity-import" {
			path = os.Getenv("HC_IDENTITY_ACCOUNTS_FILE")
		}
		data, err := config.ReadPrivateFile(path)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		defer pool.Close()
		if os.Args[1] == "identity-import" {
			in, err := identity.ParseAccountImport(data, os.Getenv("HC_ORGANIZATION_ID"))
			if err != nil {
				return err
			}
			return identity.ImportAccounts(ctx, pool, in, os.Getenv("HC_OIDC_ISSUER"), os.Getenv("HC_OPERATOR_REF"))
		}
		m, err := identity.ParseMapping(data, os.Getenv("HC_OIDC_ISSUER"), os.Getenv("HC_ORGANIZATION_ID"))
		if err != nil {
			return err
		}
		return identity.ApplyMapping(ctx, pool, m, os.Getenv("HC_OPERATOR_REF"))
	}
	if os.Args[1] != "migrate" && os.Args[1] != "dev-grants" {
		return fmt.Errorf("unknown command")
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if os.Args[1] == "migrate" {
		return db.Apply(ctx, dsn)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("local database connection failed")
	}
	defer conn.Close(context.Background())
	var database string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return err
	}
	if database != "hc_dev" {
		return fmt.Errorf("dev-grants only supports the isolated hc_dev database")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(731804522)"); err != nil {
		return err
	}
	var roles int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM pg_roles WHERE rolname IN ('hc_api','hc_worker','hc_gateway','hc_model_gateway')").Scan(&roles); err != nil {
		return err
	}
	if roles == 0 {
		bootstrap, err := config.ReadPrivateFile(".local/postgres-init.sql")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(bootstrap)); err != nil {
			return fmt.Errorf("local role initialization failed")
		}
	} else if roles != 4 {
		return fmt.Errorf("partial local role configuration requires repair")
	}
	if _, err := tx.Exec(ctx, "GRANT USAGE ON SCHEMA public TO hc_api, hc_worker, hc_gateway, hc_model_gateway; GRANT SELECT ON hc_schema_migrations TO hc_api, hc_worker, hc_gateway, hc_model_gateway"); err != nil {
		return err
	}
	if err := devlocal.GrantAPI(ctx, tx); err != nil {
		return err
	}
	if err := devlocal.GrantWorker(ctx, tx); err != nil {
		return err
	}
	if err := devlocal.GrantGateway(ctx, tx); err != nil {
		return err
	}
	if err := devlocal.GrantModelGateway(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) < 2 || !isOperationsCommand(os.Args[1]) {
		fmt.Println("Done")
	}
}
