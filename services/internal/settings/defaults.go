package settings

import (
	"context"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Spec struct {
	ID      string `json:"id"`
	CPU     int64  `json:"cpu_millicores"`
	Memory  int64  `json:"memory_bytes"`
	Storage int64  `json:"storage_bytes"`
}
type Template struct {
	ID      string `json:"id"`
	Version string `json:"template_version"`
	Name    string `json:"name"`
	Backend string `json:"backend_type"`
	Digest  string `json:"image_digest"`
	Spec    Spec   `json:"spec"`
}
type DefaultsInput struct {
	Version          *int64 `json:"version"`
	TemplateID       string `json:"template_id"`
	TemplateVersion  string `json:"template_version"`
	SpecID           string `json:"spec_id"`
	ModelID          string `json:"model_configuration_id"`
	ModelVersion     int64  `json:"model_configuration_version"`
	RetentionDays    *int   `json:"retention_days,omitempty"`
	MaxUserInstances *int   `json:"max_user_instances,omitempty"`
}
type Defaults struct {
	Version          int64  `json:"version"`
	TemplateID       string `json:"template_id"`
	TemplateVersion  string `json:"template_version"`
	SpecID           string `json:"spec_id"`
	ModelID          string `json:"model_configuration_id"`
	ModelVersion     int64  `json:"model_configuration_version"`
	Spec             Spec   `json:"spec"`
	RetentionDays    int    `json:"retention_days"`
	MaxUserInstances int    `json:"max_user_instances"`
}

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func readDefaults(ctx context.Context, q dbQuery, org string) (Defaults, error) {
	var d Defaults
	d.RetentionDays = 7
	err := q.QueryRow(ctx, `SELECT d.version,d.template_id::text,d.template_version::text,d.spec_id::text,COALESCE(d.model_config_id::text,''),COALESCE(d.model_config_version,0),s.id::text,s.cpu_millicores,s.memory_bytes,s.storage_bytes,q.max_user_instances,d.retention_days FROM organization_defaults d JOIN approved_specs s ON s.id=d.spec_id JOIN quota_policies q ON q.org_id=d.org_id WHERE d.org_id=$1`, org).Scan(&d.Version, &d.TemplateID, &d.TemplateVersion, &d.SpecID, &d.ModelID, &d.ModelVersion, &d.Spec.ID, &d.Spec.CPU, &d.Spec.Memory, &d.Spec.Storage, &d.MaxUserInstances, &d.RetentionDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, webapi.Fail(404, "DEFAULTS_NOT_CONFIGURED", "组织默认配置尚未初始化")
	}
	return d, err
}
func (s *Service) GetDefaults(ctx context.Context, p identity.Principal) (Defaults, error) {
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return Defaults{}, err
	}
	defer tx.Rollback(context.Background())
	d, err := readDefaults(ctx, tx, p.OrganizationID)
	if err != nil {
		return d, err
	}
	return d, tx.Commit(ctx)
}
func (s *Service) SaveDefaults(ctx context.Context, p identity.Principal, in DefaultsInput, trace string) (Defaults, error) {
	tx, err := s.begin(ctx, p, "defaults:write", true)
	if err != nil {
		return Defaults{}, err
	}
	defer tx.Rollback(context.Background())
	if in.Version == nil || *in.Version < 0 || !uuid.MatchString(in.TemplateID) || !uuid.MatchString(in.TemplateVersion) || !uuid.MatchString(in.SpecID) {
		return Defaults{}, invalid()
	}
	if in.ModelID != "" {
		if !uuid.MatchString(in.ModelID) || in.ModelVersion < 1 {
			return Defaults{}, invalid()
		}
		model, err := readModel(ctx, tx, p.OrganizationID, in.ModelVersion)
		if err != nil {
			return Defaults{}, err
		}
		if !s.policy.AllowsModel(model) || (model.Provider == "platform-models" && (!model.Manifest.Streaming || !model.Manifest.ToolUse)) {
			return Defaults{}, invalid()
		}
		var modelID string
		err = tx.QueryRow(ctx, `SELECT h.id::text FROM model_configuration_heads h JOIN model_configurations c ON c.id=h.id JOIN secrets sec ON sec.id=c.credential_ref JOIN model_validations v ON v.config_id=c.id AND v.config_version=c.version
 WHERE h.org_id=$1 AND c.id=$2 AND c.version=$3 AND c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND v.status='HEALTHY' AND v.checked_at<=clock_timestamp() AND v.expires_at>clock_timestamp() FOR UPDATE OF h`, p.OrganizationID, in.ModelID, in.ModelVersion).Scan(&modelID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Defaults{}, invalid()
		}
		if err != nil {
			return Defaults{}, err
		}
	} else if in.ModelVersion != 0 {
		return Defaults{}, invalid()
	}
	var approved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approved_templates t CROSS JOIN approved_specs s JOIN quota_policies q ON q.org_id=$1 WHERE t.id=$2 AND t.version=$3 AND t.enabled AND s.id=$4 AND s.enabled AND s.cpu_millicores IN (1000,2000,4000,8000,16000) AND s.memory_bytes IN (2147483648,4294967296,8589934592,17179869184,34359738368,68719476736) AND s.storage_bytes BETWEEN 1073741824 AND 107374182400 AND s.storage_bytes%1073741824=0 AND (s.storage_bytes=10737418240 OR t.runtime_manifest->>'storage_adapter'='bounded-ext4-v1'))`, p.OrganizationID, in.TemplateID, in.TemplateVersion, in.SpecID).Scan(&approved); err != nil {
		return Defaults{}, err
	}
	if !approved {
		return Defaults{}, invalid()
	}
	if in.ModelID != "" {
		model, e := readModel(ctx, tx, p.OrganizationID, in.ModelVersion)
		if e != nil {
			return Defaults{}, e
		}
		if model.Provider == "platform-models" {
			var raw []byte
			if err = tx.QueryRow(ctx, `SELECT runtime_manifest FROM approved_templates WHERE id=$1 AND version=$2`, in.TemplateID, in.TemplateVersion).Scan(&raw); err != nil {
				return Defaults{}, err
			}
			manifest, err := runtimeapproval.Parse(raw)
			if err != nil || manifest.ExternalModels != runtimeapproval.ExternalModelAdapter {
				return Defaults{}, invalid()
			}
		}
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "runtime-defaults:"+p.OrganizationID); err != nil {
		return Defaults{}, err
	}
	var current int64
	err = tx.QueryRow(ctx, `SELECT version FROM organization_defaults WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Defaults{}, err
	}
	if current != *in.Version {
		return Defaults{}, versionConflict()
	}
	var quotaMax, quotaDefault int
	if err = tx.QueryRow(ctx, `SELECT max_instances,max_user_instances FROM quota_policies WHERE org_id=$1 FOR UPDATE`, p.OrganizationID).Scan(&quotaMax, &quotaDefault); errors.Is(err, pgx.ErrNoRows) {
		return Defaults{}, webapi.Fail(422, "QUOTA_NOT_CONFIGURED", "请先初始化组织运行额度")
	} else if err != nil {
		return Defaults{}, err
	}
	retention := 7
	if current > 0 {
		if err = tx.QueryRow(ctx, `SELECT retention_days FROM organization_defaults WHERE org_id=$1`, p.OrganizationID).Scan(&retention); err != nil {
			return Defaults{}, err
		}
	}
	if in.RetentionDays != nil {
		retention = *in.RetentionDays
	}
	if in.MaxUserInstances != nil {
		quotaDefault = *in.MaxUserInstances
	}
	if retention < 1 || retention > 365 || quotaDefault < 1 || quotaDefault > quotaMax {
		return Defaults{}, webapi.Fail(422, "INVALID_SPEC", "运行默认值超出允许范围")
	}
	var belowUsage bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quota_commitments c WHERE c.org_id=$1 AND c.resource_type='INSTANCE' AND NOT EXISTS(SELECT 1 FROM user_quota_policies u WHERE u.org_id=c.org_id AND u.owner_id=c.owner_id) GROUP BY c.owner_id HAVING sum(c.amount)>$2)`, p.OrganizationID, quotaDefault).Scan(&belowUsage); err != nil {
		return Defaults{}, err
	}
	if belowUsage {
		return Defaults{}, webapi.Fail(409, "QUOTA_BELOW_USAGE", "默认实例上限不能低于继承用户的已占用数量")
	}
	if _, err = tx.Exec(ctx, `UPDATE quota_policies SET max_user_instances=$2,version=version+1 WHERE org_id=$1 AND max_user_instances<>$2`, p.OrganizationID, quotaDefault); err != nil {
		return Defaults{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_defaults(org_id,version,template_id,template_version,spec_id,model_config_id,model_config_version,retention_days) VALUES($1,$2,$3,$4,$5,NULLIF($6::text,'')::uuid,NULLIF($7::bigint,0),$8)
 ON CONFLICT(org_id) DO UPDATE SET version=EXCLUDED.version,template_id=EXCLUDED.template_id,template_version=EXCLUDED.template_version,spec_id=EXCLUDED.spec_id,model_config_id=COALESCE(EXCLUDED.model_config_id,organization_defaults.model_config_id),model_config_version=COALESCE(EXCLUDED.model_config_version,organization_defaults.model_config_version),retention_days=EXCLUDED.retention_days`, p.OrganizationID, current+1, in.TemplateID, in.TemplateVersion, in.SpecID, in.ModelID, in.ModelVersion, retention)
	if err != nil {
		return Defaults{}, err
	}
	if err := s.audit(ctx, tx, p, p.OrganizationID, "DEFAULTS_SAVE", trace); err != nil {
		return Defaults{}, err
	}
	d, err := readDefaults(ctx, tx, p.OrganizationID)
	if err != nil {
		return d, err
	}
	return d, tx.Commit(ctx)
}
func (s *Service) Templates(ctx context.Context, p identity.Principal) ([]Template, error) {
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT t.id::text,t.version::text,t.name,t.image_digest,s.id::text,s.cpu_millicores,s.memory_bytes,s.storage_bytes FROM approved_templates t CROSS JOIN approved_specs s WHERE t.enabled AND s.enabled`)
	if err != nil {
		return nil, err
	}
	items := []Template{}
	for rows.Next() {
		item := Template{Backend: "docker"}
		if err := rows.Scan(&item.ID, &item.Version, &item.Name, &item.Digest, &item.Spec.ID, &item.Spec.CPU, &item.Spec.Memory, &item.Spec.Storage); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}
