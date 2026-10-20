package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/operations"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

func isOperationsCommand(name string) bool {
	switch name {
	case "storage-hold", "storage-backup-reconcile", "backup", "restore", "cleanup", "secrets-reencrypt", "storage-backup", "storage-restore", "storage-reclaim", "dev-runtime-upgrade", "dev-runtime-rollback", "recovery-inventory", "recovery-retain", "recovery-adopt", "recovery-fence-source", "audit-archive", "audit-hold", "backup-daily":
		return true
	}
	return false
}
func runOperations(name string, args []string) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apply := flags.Bool("apply", false, "apply the reviewed operation")
	batch := flags.Int("batch", 500, "maximum rows per transaction, 1..500")
	input := flags.String("input", "", "encrypted backup path")
	output := flags.String("output", "", "new encrypted backup path")
	target := flags.String("target-dsn-file", "", "private isolated target connection file")
	confirm := flags.String("confirm-database", "", "exact isolated target database")
	from := flags.String("from-key", "", "key ID to reencrypt")
	backupID := flags.String("backup-id", "", "exact persisted backup attempt UUID")
	asset := flags.String("asset", "", "original asset UUID")
	confirmAsset := flags.String("confirm-asset", "", "exact asset UUID confirmation")
	preimage := flags.String("preimage", "", "new encrypted target preimage path")
	reason := flags.String("reason-code", "", "safe maintenance reason code")
	previousTemplate := flags.String("from-template-version", "", "exact approved previous workspace template version")
	targetTemplate := flags.String("to-template-version", "", "exact retained approved workspace template version")
	confirmOrg := flags.String("confirm-org", "", "exact audit organization confirmation")
	holdState := flags.String("hold-state", "", "explicit active or released audit hold")
	directory := flags.String("directory", "", "private dedicated local daily backup repository")
	inventoryRun := flags.String("inventory-run", "", "exact reviewed recovery inventory run UUID")
	metadataDSN := flags.String("metadata-dsn-file", "", "private independently restored metadata checkpoint connection file")
	confirmMetadata := flags.String("confirm-metadata-database", "", "exact isolated metadata checkpoint database")
	instance := flags.String("instance", "", "exact orphan instance UUID to adopt")
	sourceDSN := flags.String("source-dsn-file", "", "private old control-plane source connection file")
	confirmSource := flags.String("confirm-source-database", "", "exact old control-plane source database")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *batch < 1 || *batch > 500 {
		return operations.ErrInput
	}
	if *apply && (name == "storage-hold" || name == "storage-backup-reconcile" || name == "restore" || name == "secrets-reencrypt" || name == "storage-restore" || name == "storage-reclaim" || name == "dev-runtime-upgrade" || name == "dev-runtime-rollback" || name == "recovery-inventory" || name == "recovery-retain" || name == "recovery-adopt" || name == "recovery-fence-source" || name == "audit-archive" || name == "audit-hold" || name == "backup-daily") && os.Getenv("HC_MAINTENANCE") != "true" {
		return errors.New("apply requires coordinated maintenance: HC_MAINTENANCE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	emit := func(value any) error { return json.NewEncoder(os.Stdout).Encode(value) }
	if name == "dev-runtime-rollback" {
		if *previousTemplate == "" || *targetTemplate == "" || *reason == "" {
			return operations.ErrInput
		}
		if err := rollbackLocalRuntime(ctx, *previousTemplate, *targetTemplate, *reason, *apply); err != nil {
			return errLocalApproval
		}
		return emit(map[string]any{"applied": *apply, "previous_template_version": *previousTemplate, "template_version": *targetTemplate, "frozen_instances_preserved": true})
	}
	if name == "recovery-inventory" || name == "recovery-retain" || name == "recovery-adopt" || name == "recovery-fence-source" {
		dsn, err := config.ReadSecret(*target)
		if err != nil {
			return err
		}
		poolConfig, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return operations.ErrInput
		}
		poolConfig.MaxConns = 1
		pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			return operations.ErrInventory
		}
		defer pool.Close()
		o, err := worker.LoadOptions(os.Getenv("HC_WORKER_CONFIG_FILE"))
		if err != nil {
			return err
		}
		host, err := worker.NewLocalHost(o.Host)
		if err != nil {
			return err
		}
		client, err := workspaceengine.NewEmbedded(pool, o.WorkspaceOrganizationID, o.ControlUserID)
		if err != nil {
			return operations.ErrInventory
		}
		observer := &operations.LocalInventoryObserver{Host: host, Workspace: client, WorkspaceOrganization: o.WorkspaceOrganizationID, ControlUser: o.ControlUserID}
		if name == "recovery-fence-source" {
			dsn, err := config.ReadSecret(*sourceDSN)
			if err != nil {
				return operations.ErrInput
			}
			cfg, err := pgxpool.ParseConfig(dsn)
			if err != nil {
				return operations.ErrInput
			}
			cfg.MaxConns = 1
			source, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				return operations.ErrRecoveryFence
			}
			defer source.Close()
			service := operations.NewRecoverySourceFence(source, pool, observer, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
			report, err := service.Fence(ctx, *confirmSource, *confirm, *inventoryRun, *reason, *apply)
			if err != nil {
				return err
			}
			return emit(report)
		}
		if name == "recovery-adopt" {
			metadata, err := config.ReadSecret(*metadataDSN)
			if err != nil {
				return operations.ErrInput
			}
			cfg, err := pgxpool.ParseConfig(metadata)
			if err != nil {
				return operations.ErrInput
			}
			cfg.MaxConns = 1
			source, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				return operations.ErrRecoveryAdoption
			}
			defer source.Close()
			service := operations.NewRecoveryAdoption(pool, source, observer, recoveredRuntimeVerifier(o, client, host), os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
			report, err := service.Adopt(ctx, *confirm, *confirmMetadata, *instance, *inventoryRun, *reason, *apply)
			if err != nil {
				return err
			}
			return emit(report)
		}
		if name == "recovery-retain" {
			service := operations.NewRecoveryLedger(pool, observer, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
			report, err := service.Retain(ctx, *confirm, *inventoryRun, *reason, *apply)
			if err != nil {
				return err
			}
			return emit(report)
		}
		service := operations.NewRecoveryInventory(pool, observer, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
		report, err := service.Run(ctx, *confirm, *apply)
		if err != nil {
			return err
		}
		return emit(report)
	}
	if name == "dev-runtime-upgrade" {
		if *previousTemplate == "" {
			return operations.ErrInput
		}
		if err := verifyAndApproveLocalRuntime(ctx, *previousTemplate, *apply); err != nil {
			return errLocalApproval
		}
		return emit(map[string]any{"applied": *apply, "previous_template_version": *previousTemplate, "frozen_instances_preserved": true})
	}
	if name == "restore" {
		dsn, err := config.ReadSecret(*target)
		if err != nil {
			return err
		}
		key, err := backupKey()
		if err != nil {
			return err
		}
		defer clear(key)
		report, err := operations.RestoreDatabase(ctx, dsn, *input, *confirm, os.Getenv("HC_OPERATOR_REF"), key, *apply)
		if err != nil {
			return err
		}
		return emit(report)
	}
	dsn, err := config.ReadSecret(os.Getenv("HC_DB_DSN_FILE"))
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("operations database connection failed")
	}
	defer pool.Close()
	check := db.Check
	if name == "backup" {
		check = db.CheckBackup
	}
	if err = check(ctx, pool); err != nil {
		return err
	}
	switch name {
	case "backup-daily":
		o, err := worker.LoadOptions(os.Getenv("HC_WORKER_CONFIG_FILE"))
		if err != nil {
			return err
		}
		host, err := worker.NewLocalHost(o.Host)
		if err != nil {
			return err
		}
		var key []byte
		if *apply {
			key, err = backupKey()
			if err != nil {
				return err
			}
			defer clear(key)
		}
		source := operations.NewPostgresDailySource(pool, dsn, host, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
		report, err := operations.NewDailyBackup(source, *directory, key).Run(ctx, *apply)
		if err != nil {
			return err
		}
		return emit(report)
	case "audit-archive":
		var key []byte
		if *apply {
			key, err = backupKey()
			if err != nil {
				return err
			}
			defer clear(key)
		}
		report, err := operations.NewAuditArchiver(pool, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF")).Batch(ctx, *output, key, *confirmOrg, *batch, *apply)
		if err != nil {
			return err
		}
		return emit(report)
	case "audit-hold":
		if *holdState != "active" && *holdState != "released" {
			return operations.ErrInput
		}
		report, err := operations.NewAuditArchiver(pool, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF")).Hold(ctx, *holdState == "active", *reason, *confirmOrg, *apply)
		if err != nil {
			return err
		}
		return emit(report)
	case "storage-hold", "storage-backup-reconcile", "storage-backup", "storage-restore", "storage-reclaim":
		o, err := worker.LoadOptions(os.Getenv("HC_WORKER_CONFIG_FILE"))
		if err != nil {
			return err
		}
		host, err := worker.NewLocalHost(o.Host)
		if err != nil {
			return err
		}
		service := operations.NewStorageService(pool, host, os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
		if name == "storage-hold" {
			if *holdState != "active" && *holdState != "released" {
				return operations.ErrInput
			}
			err := service.SetStorageHold(ctx, *asset, *confirmAsset, *reason, *holdState == "active", *apply)
			if err != nil {
				return err
			}
			return emit(map[string]any{"applied": *apply, "asset_id": *asset, "legal_hold": *holdState == "active"})
		}
		if name == "storage-backup-reconcile" {
			if !*apply {
				return operations.ErrInput
			}
			key, err := backupKey()
			if err != nil {
				return err
			}
			defer clear(key)
			state, err := service.ReconcileBackup(ctx, *backupID, key)
			if err != nil {
				return err
			}
			return emit(map[string]any{"backup_id": *backupID, "status": state})
		}
		if name == "storage-reclaim" {
			report, err := service.Reclaim(ctx, *asset, *confirmAsset, *reason, *apply)
			if err != nil {
				return errors.New("protected storage reclamation failed; existing quota remains conservative")
			}
			return emit(report)
		}
		key, err := backupKey()
		if err != nil {
			return err
		}
		defer clear(key)
		if name == "storage-backup" {
			proof, err := service.Backup(ctx, *asset, *output, key)
			if err != nil {
				return errors.New("offline encrypted asset backup failed")
			}
			return emit(proof)
		}
		report, err := service.Restore(ctx, *input, *asset, *preimage, *confirmAsset, key, *apply)
		if err != nil {
			return errors.New("offline asset restore failed; inspect persisted maintenance intent before resuming")
		}
		return emit(report)
	case "backup":
		key, err := backupKey()
		if err != nil {
			return err
		}
		defer clear(key)
		proof, err := operations.BackupDatabase(ctx, pool, dsn, *output, os.Getenv("HC_OPERATOR_REF"), key)
		if err != nil {
			return err
		}
		return emit(proof)
	case "cleanup":
		cleaner := operations.NewCleaner(pool)
		var result operations.CleanupResult
		if *apply {
			result, err = cleaner.Once(ctx, *batch)
		} else {
			result, err = cleaner.Preview(ctx, *batch)
		}
		if err != nil {
			return errors.New("bounded cleanup failed")
		}
		return emit(map[string]any{"applied": *apply, "records": result})
	case "secrets-reencrypt":
		cipher, err := security.LoadSecretCipher(os.Getenv("HC_DATA_KEY_ID"), os.Getenv("HC_DATA_KEY_FILE"), os.Getenv("HC_DATA_KEYRING_FILE"))
		if err != nil {
			return err
		}
		rotator := operations.NewRotator(pool, cipher, os.Getenv("HC_ENVIRONMENT"), os.Getenv("HC_ORGANIZATION_ID"), os.Getenv("HC_OPERATOR_REF"))
		before, err := rotator.Count(ctx, *from)
		if err != nil {
			return errors.New("key rotation preview failed")
		}
		changed := 0
		if *apply {
			for range 10000 {
				n, err := rotator.Batch(ctx, *from, *batch)
				if err != nil {
					return errors.New("key rotation batch failed; prior committed batches remain resumable")
				}
				changed += n
				if n == 0 {
					break
				}
			}
		}
		after, err := rotator.Count(ctx, *from)
		if err != nil {
			return errors.New("key rotation final count failed")
		}
		if err = emit(map[string]any{"applied": *apply, "before": before, "changed": changed, "remaining": after, "current_key_id": cipher.CurrentKeyID()}); err != nil {
			return err
		}
		if *apply && after != 0 {
			return errors.New("old-key records remain; resume after active writers finish")
		}
		return nil
	}
	return operations.ErrInput
}
func backupKey() ([]byte, error) {
	key, err := config.ReadPrivateFile(os.Getenv("HC_BACKUP_KEY_FILE"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("HC_BACKUP_KEY_FILE requires a private independent 32-byte key")
	}
	path := os.Getenv("HC_DATA_KEY_FILE")
	if path == "" {
		clear(key)
		return nil, errors.New("HC_DATA_KEY_FILE is required to verify backup-key separation")
	}
	if path != "" {
		data, err := config.ReadPrivateFile(path)
		same := bytes.Equal(key, data)
		clear(data)
		if err != nil || same {
			clear(key)
			return nil, errors.New("backup key must be independent from data encryption keys")
		}
	}
	if ringPath := os.Getenv("HC_DATA_KEYRING_FILE"); ringPath != "" {
		raw, err := config.ReadPrivateFile(ringPath)
		var ring struct {
			Schema int               `json:"schema_version"`
			Keys   map[string]string `json:"keys"`
		}
		if err != nil || webapi.StrictUnmarshal(raw, &ring) != nil || ring.Schema != 1 || len(ring.Keys) == 0 || len(ring.Keys) > 16 {
			clear(key)
			return nil, errors.New("invalid data keyring for backup separation")
		}
		for _, path := range ring.Keys {
			value, err := config.ReadPrivateFile(path)
			same := bytes.Equal(key, value)
			clear(value)
			if err != nil || same {
				clear(key)
				return nil, errors.New("backup key must be independent from all data keyring entries")
			}
		}
	}
	return key, nil
}
