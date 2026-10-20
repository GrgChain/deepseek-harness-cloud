package worker

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type HealthPlan struct {
	ExecutionPlan
	Host, IP        string
	Started         time.Time
	Cookie          []byte
	NativeExpiresAt time.Time
}

func (p *HealthPlan) Destroy() { p.ExecutionPlan.Destroy(); clear(p.Cookie); p.Cookie = nil }

type HealthObservation struct {
	Status instances.Status
	Reason string
}
type HealthResult struct {
	Checks  map[instances.Dimension]HealthObservation
	cookie  []byte
	expires time.Time
}

func (r HealthResult) String() string   { return "[private health result]" }
func (r HealthResult) GoString() string { return r.String() }
func (r *HealthResult) Destroy()        { clear(r.cookie); r.cookie = nil }
func (r HealthResult) valid() bool {
	if len(r.Checks) != 4 {
		return false
	}
	for _, d := range []instances.Dimension{instances.Application, instances.Storage, instances.Configuration, instances.Access} {
		v, ok := r.Checks[d]
		if !ok {
			return false
		}
		if v.Status == instances.Healthy {
			if v.Reason != "" {
				return false
			}
			continue
		}
		if v.Status != instances.Unhealthy {
			return false
		}
		switch v.Reason {
		case "APPLICATION_UNAVAILABLE", "STORAGE_MISSING", "CONFIGURATION_INVALID", "ACCESS_UNAVAILABLE", "WORKSPACE_UNAVAILABLE", "AUTH_REVOKED", "DEPENDENCY_UNAVAILABLE", "HEALTH_TIMEOUT":
		default:
			return false
		}
	}
	return true
}

type HealthStore struct {
	pool        *pgxpool.Pool
	cipher      *security.SecretCipher
	environment string
	publicPort  int
}

func NewHealthStore(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string, publicPort int) *HealthStore {
	return &HealthStore{pool, cipher, environment, publicPort}
}
func (s *HealthStore) Due(ctx context.Context, slots int) ([]string, error) {
	if slots < 1 || slots > 2 {
		return nil, ErrInvalidEvidence
	}
	rows, err := s.pool.Query(ctx, `SELECT i.id::text FROM instances i LEFT JOIN instance_health h ON h.instance_id=i.id AND h.dimension='application' WHERE i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND i.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) AND (h.checked_at IS NULL OR h.checked_at<=clock_timestamp()-make_interval(secs=>30+get_byte(decode(replace(i.id::text,'-',''),'hex'),0)%5)) ORDER BY i.id LIMIT $1`, slots)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *HealthStore) Load(ctx context.Context, id string) (HealthPlan, error) {
	if !uuidPattern.MatchString(id) || s.cipher == nil {
		return HealthPlan{}, ErrInvalidEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return HealthPlan{}, err
	}
	defer tx.Rollback(context.Background())
	var p HealthPlan
	err = tx.QueryRow(ctx, `SELECT org_id::text,owner_id::text FROM instances WHERE id=$1 AND observed_state='RUNNING' AND desired_state='RUNNING' AND deleted_at IS NULL`, id).Scan(&p.OrganizationID, &p.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return HealthPlan{}, ErrSuperseded
	}
	if err != nil {
		return HealthPlan{}, err
	}
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_authority($1,$2)`, p.OrganizationID, p.OwnerID).Scan(&authorized); err != nil {
		return HealthPlan{}, err
	}
	if !authorized {
		return HealthPlan{}, ErrExecutionRevoked
	}
	var raw, manifest []byte
	var modelID, nativeGeneration string
	var modelVersion int64
	var enabled bool
	var nativeExpiry *time.Time
	var native, model security.Envelope
	var credential string
	err = tx.QueryRow(ctx, `SELECT i.version,i.config_snapshot,i.workspace_id::text,i.workspace_name,i.storage_asset_id::text,a.status,a.resource_name,COALESCE(a.filesystem_id,''),a.capacity_bytes,u.workspace_owner_id::text,c.endpoint,i.model_config_id::text,i.model_config_version,r.host,host(r.upstream_ip),r.runtime_generation::text,r.enabled,COALESCE(n.runtime_generation::text,''),COALESCE(n.key_id,''),COALESCE(n.nonce,''::bytea),COALESCE(n.cookie_ciphertext,''::bytea),n.expires_at,sec.id::text,sec.key_id,sec.nonce,sec.ciphertext,(SELECT op.build_id::text FROM operations op WHERE op.id=r.runtime_generation AND op.instance_id=i.id AND op.status='SUCCEEDED'),clock_timestamp(),c.manifest FROM instances i JOIN storage_assets a ON a.id=i.storage_asset_id JOIN users u ON u.id=i.owner_id JOIN model_configurations c ON c.id=i.model_config_id AND c.version=i.model_config_version AND c.org_id=i.org_id JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id JOIN access_routes r ON r.instance_id=i.id LEFT JOIN native_sessions n ON n.instance_id=i.id WHERE i.id=$1 AND i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND i.deleted_at IS NULL AND c.revoked_at IS NULL AND c.status<>'REVOKED' AND sec.revoked_at IS NULL AND NOT EXISTS(SELECT 1 FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) FOR UPDATE OF i`, id).Scan(&p.Version, &raw, &p.WorkspaceID, &p.WorkspaceName, &p.AssetID, &p.AssetStatus, &p.ResourceName, &p.FilesystemID, &p.Capacity, &p.WorkspaceOwner, &p.Endpoint, &modelID, &modelVersion, &p.Host, &p.IP, &p.RuntimeGeneration, &enabled, &nativeGeneration, &native.KeyID, &native.Nonce, &native.Ciphertext, &nativeExpiry, &credential, &model.KeyID, &model.Nonce, &model.Ciphertext, &p.BuildID, &p.Started, &manifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return HealthPlan{}, ErrExecutionRevoked
	}
	if err != nil {
		return HealthPlan{}, err
	}
	p.InstanceID = id
	p.Action = "HEALTH"
	p.Desired = "RUNNING"
	p.Snapshot, err = instances.DecodeSnapshot(raw)
	ip := net.ParseIP(p.IP)
	if err != nil || p.Snapshot.ModelID != modelID || p.Snapshot.ModelVersion != modelVersion || !uuidPattern.MatchString(p.RuntimeGeneration) || p.WorkspaceID == nil || p.BuildID == nil || !hostPattern.MatchString(p.Host) || !strings.HasPrefix(p.Host, "i-"+strings.ReplaceAll(id, "-", "")+".") || ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return HealthPlan{}, ErrInvalidEvidence
	}
	if !enabled {
		return HealthPlan{}, ErrSuperseded
	}
	// Periodic probes must reconstruct the same pinned gateway configuration as
	// CREATE/START. Private aliases are absent from the shared static policy.
	if p.Snapshot.Provider == "platform-models" {
		p.modelManifest = &settings.ModelManifest{}
		if webapi.StrictUnmarshal(manifest, p.modelManifest) != nil || p.modelManifest.ID != p.Snapshot.Model || p.modelManifest.Digest != p.Snapshot.ApprovalDigest {
			return HealthPlan{}, ErrInvalidEvidence
		}
	}
	p.modelKey, err = s.cipher.Decrypt(model, security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: credential, Purpose: "model-key"})
	if err != nil {
		return HealthPlan{}, err
	}
	if nativeGeneration == p.RuntimeGeneration && nativeExpiry != nil && nativeExpiry.After(p.Started) {
		p.Cookie, err = s.cipher.Decrypt(native, security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: id + ":" + p.RuntimeGeneration, Purpose: "native-cookie"})
		if err != nil {
			p.Destroy()
			return HealthPlan{}, err
		}
		p.NativeExpiresAt = *nativeExpiry
	}
	if err = tx.Commit(ctx); err != nil {
		p.Destroy()
		return HealthPlan{}, err
	}
	return p, nil
}
func (s *HealthStore) Publish(ctx context.Context, p HealthPlan, result HealthResult) (bool, error) {
	if !result.valid() || !uuidPattern.MatchString(p.InstanceID) || p.Started.IsZero() {
		return false, ErrInvalidEvidence
	}
	if len(result.cookie) > 0 && (!validNativeHeader(publicAuthority(p.Host, s.publicPort), result.cookie) || !result.expires.After(p.Started) || result.expires.After(p.Started.Add(24*time.Hour))) {
		return false, ErrInvalidEvidence
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT public.hc_worker_lock_authority($1,$2)`, p.OrganizationID, p.OwnerID).Scan(&authorized); err != nil {
		return false, err
	}
	if !authorized {
		return false, nil
	}
	var raw []byte
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT i.config_snapshot,clock_timestamp() FROM instances i JOIN access_routes r ON r.instance_id=i.id JOIN model_configurations c ON c.id=i.model_config_id AND c.version=i.model_config_version AND c.org_id=i.org_id JOIN secrets sec ON sec.id=c.credential_ref AND sec.org_id=c.org_id WHERE i.id=$1 AND i.org_id=$2 AND i.owner_id=$3 AND i.version=$4 AND i.observed_state='RUNNING' AND i.desired_state='RUNNING' AND i.deleted_at IS NULL AND i.model_config_id=$5 AND i.model_config_version=$6 AND c.revoked_at IS NULL AND c.status<>'REVOKED' AND sec.revoked_at IS NULL AND r.enabled AND r.runtime_generation=$7 AND r.host=$8 AND r.upstream_ip=$9::inet AND NOT EXISTS(SELECT 1 FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN')) FOR UPDATE OF i`, p.InstanceID, p.OrganizationID, p.OwnerID, p.Version, p.Snapshot.ModelID, p.Snapshot.ModelVersion, p.RuntimeGeneration, p.Host, p.IP).Scan(&raw, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	snapshot, err := instances.DecodeSnapshot(raw)
	if err != nil {
		return false, err
	}
	if snapshot != p.Snapshot || p.Started.After(now) || !p.Started.Add(60*time.Second).After(now) {
		return false, nil
	}
	var newer bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instance_health WHERE instance_id=$1 AND dimension IN ('application','storage','configuration','access') AND checked_at>$2)`, p.InstanceID, p.Started).Scan(&newer); err != nil {
		return false, err
	}
	if newer {
		return false, nil
	}
	allHealthy := true
	for _, d := range []instances.Dimension{instances.Application, instances.Storage, instances.Configuration, instances.Access} {
		v := result.Checks[d]
		allHealthy = allHealthy && v.Status == instances.Healthy
		if _, err = tx.Exec(ctx, `INSERT INTO instance_health(instance_id,dimension,status,checked_at,expires_at,reason_code) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(instance_id,dimension) DO UPDATE SET status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at,reason_code=EXCLUDED.reason_code`, p.InstanceID, string(d), string(v.Status), p.Started, p.Started.Add(60*time.Second), v.Reason); err != nil {
			return false, err
		}
	}
	// The shared version's original timestamp/TTL is copied, never extended by
	// per-instance health. This does not perform an inference request.
	if _, err = tx.Exec(ctx, `INSERT INTO instance_health(instance_id,dimension,status,checked_at,expires_at,reason_code) SELECT $1,'model',status,checked_at,expires_at,reason_code FROM model_validations WHERE config_id=$2 AND config_version=$3 ON CONFLICT(instance_id,dimension) DO UPDATE SET status=EXCLUDED.status,checked_at=EXCLUDED.checked_at,expires_at=EXCLUDED.expires_at,reason_code=EXCLUDED.reason_code`, p.InstanceID, p.Snapshot.ModelID, p.Snapshot.ModelVersion); err != nil {
		return false, err
	}
	if len(result.cookie) > 0 && allHealthy {
		envelope, err := s.cipher.Encrypt(result.cookie, security.Context{Environment: s.environment, OrganizationID: p.OrganizationID, RecordID: p.InstanceID + ":" + p.RuntimeGeneration, Purpose: "native-cookie"})
		if err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO native_sessions(instance_id,runtime_generation,key_id,nonce,cookie_ciphertext,expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(instance_id) DO UPDATE SET runtime_generation=EXCLUDED.runtime_generation,key_id=EXCLUDED.key_id,nonce=EXCLUDED.nonce,cookie_ciphertext=EXCLUDED.cookie_ciphertext,expires_at=EXCLUDED.expires_at`, p.InstanceID, p.RuntimeGeneration, envelope.KeyID, envelope.Nonce, envelope.Ciphertext, result.expires); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
