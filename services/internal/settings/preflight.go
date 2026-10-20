package settings

import (
	"context"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

type Check struct {
	Name   string `json:"name"`
	Health Health `json:"health"`
}
type Preflight struct {
	Quota       *ResourceQuota       `json:"quota,omitempty"`
	CanCreate   bool                 `json:"can_create"`
	Checks      []Check              `json:"checks"`
	Models      []string             `json:"approved_models,omitempty"`
	Endpoints   []string             `json:"approved_endpoints,omitempty"`
	Connections []ApprovedConnection `json:"approved_connections,omitempty"`
}
type ApprovedConnection struct {
	Provider string          `json:"provider"`
	Protocol string          `json:"protocol"`
	Endpoint string          `json:"endpoint"`
	Models   []ModelManifest `json:"models"`
}

type PlatformChecks struct {
	Ready  bool    `json:"ready"`
	Checks []Check `json:"checks"`
}

func (s *Service) PlatformChecks(ctx context.Context, p identity.Principal, redisProbe func(context.Context) error) (PlatformChecks, error) {
	result, err := s.preflight(ctx, p, redisProbe, "", 0, true)
	ready := err == nil
	for _, check := range result.Checks {
		ready = ready && check.Health.Status == "HEALTHY"
	}
	return PlatformChecks{Ready: ready, Checks: result.Checks}, err
}

func (s *Service) Preflight(ctx context.Context, p identity.Principal, redisProbe func(context.Context) error) (Preflight, error) {
	return s.PreflightForModel(ctx, p, redisProbe, "", 0)
}
func (s *Service) PreflightForModel(ctx context.Context, p identity.Principal, redisProbe func(context.Context) error, selectedID string, selectedVersion int64, specIDs ...string) (Preflight, error) {
	return s.preflight(ctx, p, redisProbe, selectedID, selectedVersion, false, specIDs...)
}

func (s *Service) preflight(ctx context.Context, p identity.Principal, redisProbe func(context.Context) error, selectedID string, selectedVersion int64, platform bool, specIDs ...string) (Preflight, error) {
	result := Preflight{Checks: []Check{}}
	tx, err := s.begin(ctx, p, "configuration:read", false)
	if err != nil {
		return result, err
	}
	var admin, canCreate bool
	if err := tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,'model:write') OR public.hc_has_permission($1,$2,'model:validate'),public.hc_has_permission($1,$2,'instances:create') AND public.hc_has_permission($1,$2,'instances:start')`, p.OrganizationID, p.UserID).Scan(&admin, &canCreate); err != nil {
		_ = tx.Rollback(context.Background())
		return result, err
	}
	var quotaConfigured bool
	if platform {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quota_policies WHERE org_id=$1)`, p.OrganizationID).Scan(&quotaConfigured); err != nil {
			_ = tx.Rollback(context.Background())
			return result, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	add := func(name, status, reason string) {
		now := time.Now().UTC()
		expiry := now.Add(15 * time.Second)
		result.Checks = append(result.Checks, Check{Name: name, Health: Health{Status: status, CheckedAt: &now, ExpiresAt: &expiry, Reason: reason}})
	}
	add("identity", "HEALTHY", "")
	add("database", "HEALTHY", "")
	redisCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if redisProbe != nil && redisProbe(redisCtx) == nil {
		add("redis", "HEALTHY", "")
	} else {
		add("redis", "UNHEALTHY", "DEPENDENCY_UNAVAILABLE")
	}
	items, err := s.Templates(ctx, p)
	if err != nil {
		return result, err
	}
	d, defaultsErr := s.GetDefaults(ctx, p)
	if defaultsErr == nil && len(specIDs) > 0 && specIDs[0] != "" {
		for _, item := range items {
			if item.ID == d.TemplateID && item.Version == d.TemplateVersion && item.Spec.ID == specIDs[0] {
				d.Spec = item.Spec
				break
			}
		}
	}
	if s.runtime == nil {
		for _, name := range []string{"workspace", "docker", "runtime_manifest", "storage", "capacity", "access"} {
			add(name, "UNKNOWN", "RUNTIME_NOT_CONFIGURED")
		}
	} else {
		var checks []Check
		var err error
		if platform {
			checks, err = s.runtime.PlatformChecks(ctx, p)
		} else if resources, ok := s.runtime.(RuntimeResourceChecks); ok && defaultsErr == nil {
			checks, err = resources.ChecksForResources(ctx, p, d.Spec)
		} else {
			checks, err = s.runtime.Checks(ctx, p)
		}
		if err != nil {
			return result, err
		}
		result.Checks = append(result.Checks, checks...)
	}
	if platform {
		if quotaConfigured {
			add("organization_quota", "HEALTHY", "")
		} else {
			add("organization_quota", "UNHEALTHY", "QUOTA_NOT_CONFIGURED")
		}
	}
	if defaultsErr == nil {
		add("defaults", "HEALTHY", "")
		chosenSpec := d.SpecID
		if len(specIDs) > 0 && specIDs[0] != "" {
			chosenSpec = specIDs[0]
		}
		templateOK, specOK := false, false
		for _, item := range items {
			if item.ID == d.TemplateID && item.Version == d.TemplateVersion {
				templateOK = true
				if item.Spec.ID == chosenSpec && runtimeapproval.SupportedResources(item.Spec.CPU, item.Spec.Memory, item.Spec.Storage) {
					d.Spec = item.Spec
					specOK = true
				}
			}
		}
		if templateOK {
			add("template", "HEALTHY", "")
		} else {
			add("template", "UNHEALTHY", "TEMPLATE_NOT_CONFIGURED")
		}
		if specOK {
			add("spec", "HEALTHY", "")
		} else {
			add("spec", "UNHEALTHY", "SPEC_NOT_CONFIGURED")
		}

		if !platform {
			quota, reason, e := s.quotaPreview(ctx, p, d.Spec)
			if e != nil {
				return result, e
			}
			result.Quota = quota
			if reason == "" {
				add("quota", "HEALTHY", "")
			} else {
				add("quota", "UNHEALTHY", reason)
			}
			modelID, modelVersion := d.ModelID, d.ModelVersion
			if selectedID != "" {
				modelID, modelVersion = selectedID, selectedVersion
			} else {
				personal, e := s.GetPersonalDefault(ctx, p)
				if e == nil && personal != nil && personal.ConnectionID != nil && personal.ConnectionVersion != nil {
					modelID, modelVersion = *personal.ConnectionID, *personal.ConnectionVersion
				}
				if e != nil && !errors.Is(e, identity.ErrForbidden) {
					return result, e
				}
			}
			modelTx, e := s.begin(ctx, p, "configuration:read", false)
			if e != nil {
				return result, e
			}
			m, e := readModelVersion(ctx, modelTx, p.OrganizationID, modelID, modelVersion)
			var usable bool
			if e == nil {
				e = modelTx.QueryRow(ctx, `SELECT public.hc_model_available($1,$2,$3,$4)`, p.OrganizationID, p.UserID, modelID, modelVersion).Scan(&usable)
			}
			approved := e == nil && (s.policy.AllowsModel(m) || s.personalApproved(ctx, modelTx, p.OrganizationID, m))
			if e == nil && m.Provider == "platform-models" {
				var adapter bool
				e = modelTx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approved_templates WHERE id=$1 AND version=$2 AND enabled AND runtime_manifest->>'external_model_adapter'='platform-messages-v1')`, d.TemplateID, d.TemplateVersion).Scan(&adapter)
				if e == nil && adapter {
					add("model_adapter", "HEALTHY", "")
				} else {
					add("model_adapter", "UNHEALTHY", "RUNTIME_NOT_APPROVED")
				}
			}
			_ = modelTx.Rollback(context.Background())
			if e == nil && usable && m.Validation.Status == "HEALTHY" && m.CredentialConfigured && approved {
				add("model", "HEALTHY", "")
			} else {
				add("model", "UNHEALTHY", "VALIDATION_REQUIRED")
			}
		}
	} else {
		add("template", "UNKNOWN", "DEFAULTS_NOT_CONFIGURED")
		add("spec", "UNKNOWN", "DEFAULTS_NOT_CONFIGURED")
		add("defaults", "UNHEALTHY", "DEFAULTS_NOT_CONFIGURED")
		if !platform {
			add("quota", "UNKNOWN", "DEFAULTS_NOT_CONFIGURED")
			add("model", "UNKNOWN", "DEFAULTS_NOT_CONFIGURED")
		}
	}
	if s.maintenance {
		add("maintenance", "UNHEALTHY", "MAINTENANCE")
	} else {
		add("maintenance", "HEALTHY", "")
	}
	if !platform && admin && s.policy != nil {
		for _, gateway := range s.policy.Gateways {
			connection := ApprovedConnection{Provider: "platform-models", Protocol: "anthropic-messages", Endpoint: gateway.Endpoint, Models: []ModelManifest{}}
			for _, model := range gateway.Models {
				manifest, _ := s.policy.Manifest(gateway.Endpoint, model.ID)
				connection.Models = append(connection.Models, manifest)
			}
			result.Connections = append(result.Connections, connection)
		}
		result.Models = append([]string{}, s.policy.Models...)
		result.Endpoints = append([]string{}, s.policy.Endpoints...)
	}
	result.CanCreate = canCreate
	for _, check := range result.Checks {
		if check.Health.Status != "HEALTHY" {
			result.CanCreate = false
		}
	}
	return result, nil
}
