package db

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/000001_initial.sql
var baseline string

//go:embed migrations/000002_worker_authority_lock.sql
var workerAuthority string

//go:embed migrations/000003_model_probe_schedule.sql
var modelProbeSchedule string

//go:embed migrations/000004_access_authority_lock.sql
var accessAuthority string

//go:embed migrations/000005_bounded_cleanup.sql
var boundedCleanup string

//go:embed migrations/000006_storage_maintenance.sql
var storageMaintenance string

//go:embed migrations/000007_audit_retention.sql
var auditRetention string

//go:embed migrations/000008_recovery_quota_holds.sql
var recoveryQuotaHolds string

//go:embed migrations/000009_embedded_workspaces.sql
var embeddedWorkspaces string

//go:embed migrations/000010_workspace_names.sql
var workspaceNames string

//go:embed migrations/000011_identity_management.sql
var identityManagement string

//go:embed migrations/000012_model_gateways.sql
var modelGateways string

//go:embed migrations/000013_personal_model_management.sql
var personalModels string

//go:embed migrations/000014_model_registry_idempotency.sql
var modelRegistryIdempotency string

//go:embed migrations/000015_personal_model_create_replay.sql
var personalModelCreateReplay string

//go:embed migrations/000016_personal_model_execution.sql
var personalModelExecution string

//go:embed migrations/000017_personal_model_authority.sql
var personalModelAuthority string

//go:embed migrations/000018_model_catalog_availability.sql
var modelCatalogAvailability string

//go:embed migrations/000019_personal_default_tombstone.sql
var personalDefaultTombstone string

//go:embed migrations/000020_personal_model_compensation.sql
var personalModelCompensation string

//go:embed migrations/000021_password_reset_intent.sql
var passwordResetIntent string

//go:embed migrations/000022_model_gateway_checks.sql
var modelGatewayChecks string

//go:embed migrations/000023_internal_oidc.sql
var internalOIDC string

//go:embed migrations/000024_native_model_gateway.sql
var nativeModelGateway string

//go:embed migrations/000025_custom_model_catalog.sql
var customModelCatalog string

//go:embed migrations/000026_user_resource_quotas.sql
var userResourceQuotas string

//go:embed migrations/000027_personal_provider_connections.sql
var personalProviderConnections string

//go:embed migrations/000028_provider_account_naming.sql
var providerAccountNaming string

//go:embed migrations/000029_selectable_resource_specs.sql
var selectableResourceSpecs string

//go:embed migrations/000030_authority_reference_locks.sql
var authorityReferenceLocks string

//go:embed migrations/000031_unified_instance_quota.sql
var unifiedInstanceQuota string

//go:embed migrations/000032_provider_default_models.sql
var providerDefaultModels string

//go:embed migrations/000033_instance_display_names.sql
var instanceDisplayNames string

//go:embed migrations/000034_runtime_defaults.sql
var runtimeDefaults string

//go:embed migrations/000035_assignable_role_permissions.sql
var assignableRolePermissions string

//go:embed migrations/000036_openai_responses.sql
var openAIResponses string

//go:embed migrations/000037_pending_identity_login_email.sql
var pendingIdentityLoginEmail string

//go:embed migrations/000038_identity_provisioning_lock.sql
var identityProvisioningLock string

//go:embed migrations/000039_default_user_quota.sql
var defaultUserQuota string

//go:embed migrations/000040_personal_workspace_authority.sql
var personalWorkspaceAuthority string

//go:embed migrations/000041_system_resource_defaults.sql
var systemResourceDefaults string

//go:embed migrations/000042_expanded_compute_specs.sql
var expandedComputeSpecs string

//go:embed migrations/000043_storage_management.sql
var storageManagement string

//go:embed migrations/000044_owner_storage_release.sql
var ownerStorageRelease string

//go:embed migrations/000045_storage_file_browsing.sql
var storageFileBrowsing string

type migration struct {
	version int64
	sql     string
}

func migrations() []migration {
	return []migration{{1, baseline}, {2, workerAuthority}, {3, modelProbeSchedule}, {4, accessAuthority}, {5, boundedCleanup}, {6, storageMaintenance}, {7, auditRetention}, {8, recoveryQuotaHolds}, {9, embeddedWorkspaces}, {10, workspaceNames}, {11, identityManagement}, {12, modelGateways}, {13, personalModels}, {14, modelRegistryIdempotency}, {15, personalModelCreateReplay}, {16, personalModelExecution}, {17, personalModelAuthority}, {18, modelCatalogAvailability}, {19, personalDefaultTombstone}, {20, personalModelCompensation}, {21, passwordResetIntent}, {22, modelGatewayChecks}, {23, internalOIDC}, {24, nativeModelGateway}, {25, customModelCatalog}, {26, userResourceQuotas}, {27, personalProviderConnections}, {28, providerAccountNaming}, {29, selectableResourceSpecs}, {30, authorityReferenceLocks}, {31, unifiedInstanceQuota}, {32, providerDefaultModels}, {33, instanceDisplayNames}, {34, runtimeDefaults}, {35, assignableRolePermissions}, {36, openAIResponses}, {37, pendingIdentityLoginEmail}, {38, identityProvisioningLock}, {39, defaultUserQuota}, {40, personalWorkspaceAuthority}, {41, systemResourceDefaults}, {42, expandedComputeSpecs}, {43, storageManagement}, {44, ownerStorageRelease}, {45, storageFileBrowsing}}
}
func checksumOf(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Apply serializes migrators and commits the schema and checksum in one transaction.
// Application processes only check the ledger; they never execute DDL.
func Apply(ctx context.Context, dsn string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("migration database connection failed")
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(731804521)"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS hc_schema_migrations (version bigint PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var versions []int64
	var checksums []string
	if err := tx.QueryRow(ctx, "SELECT coalesce(array_agg(version ORDER BY version),'{}'::bigint[]),coalesce(array_agg(checksum ORDER BY version),'{}'::text[]) FROM hc_schema_migrations").Scan(&versions, &checksums); err != nil {
		return err
	}
	expected := migrations()
	if len(versions) > len(expected) {
		return errors.New("schema version or migration checksum mismatch")
	}
	for i, version := range versions {
		if version != expected[i].version || checksums[i] != checksumOf(expected[i].sql) {
			return errors.New("schema version or migration checksum mismatch")
		}
	}
	for _, next := range expected[len(versions):] {
		if _, err := tx.Exec(ctx, next.sql); err != nil {
			return fmt.Errorf("schema migration %d: %w", next.version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO hc_schema_migrations(version,checksum) VALUES($1,$2)", next.version, checksumOf(next.sql)); err != nil {
			return err
		}
	}
	if err := Check(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func Check(ctx context.Context, conn Querier) error {
	return checkLedger(ctx, conn, false)
}

// CheckBackup permits a known, checksummed older schema for pre-upgrade backups.
// Serving processes still require Check's exact current version.
func CheckBackup(ctx context.Context, conn Querier) error { return checkLedger(ctx, conn, true) }
func checkLedger(ctx context.Context, conn Querier, allowPending bool) error {
	var versions []int64
	var checksums []string
	if err := conn.QueryRow(ctx, "SELECT coalesce(array_agg(version ORDER BY version),'{}'::bigint[]),coalesce(array_agg(checksum ORDER BY version),'{}'::text[]) FROM hc_schema_migrations").Scan(&versions, &checksums); err != nil {
		return errors.New("database migration is required")
	}
	expected := migrations()
	if len(versions) == 0 || len(versions) > len(expected) || (!allowPending && len(versions) != len(expected)) {
		return errors.New("schema version or migration checksum mismatch")
	}
	for i, version := range versions {
		if version != expected[i].version || checksums[i] != checksumOf(expected[i].sql) {
			return errors.New("schema version or migration checksum mismatch")
		}
	}
	return nil
}
