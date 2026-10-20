package access

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

var ErrConfiguration = errors.New("access requires valid private configuration in HC_ACCESS_CONFIG_FILE")
var configName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// This file contains deployment facts and references to private credentials.
// Gateway never loads an OIDC client secret or the plaintext model API key.
type Options struct {
	SchemaVersion           int    `json:"schema_version"`
	OrganizationID          string `json:"organization_id"`
	WorkspaceOrganizationID string `json:"workspace_organization_id"`
	ControlUserID           string `json:"control_user_id"`
	WorkspaceEngine         string `json:"workspace_engine"`
	Environment             string `json:"environment"`
	DataKeyID               string `json:"data_key_id"`
	DataKeyFile             string `json:"data_key_file"`
	DataKeyringFile         string `json:"data_keyring_file,omitempty"`
	PortalOrigin            string `json:"portal_origin"`
	Domain                  string `json:"instance_domain"`
}

func LoadOptions(path string) (Options, error) {
	raw, err := config.ReadPrivateFile(path)
	var o Options
	if err != nil || webapi.StrictUnmarshal(raw, &o) != nil || o.SchemaVersion != 1 || !uuid.MatchString(o.OrganizationID) || !uuid.MatchString(o.WorkspaceOrganizationID) || !uuid.MatchString(o.ControlUserID) || !configName.MatchString(o.Environment) || !configName.MatchString(o.DataKeyID) {
		return Options{}, ErrConfiguration
	}
	if o.DataKeyringFile != "" && (!filepath.IsAbs(o.DataKeyringFile) || filepath.Clean(o.DataKeyringFile) != o.DataKeyringFile) {
		return Options{}, ErrConfiguration
	}
	for _, p := range []string{o.DataKeyFile} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return Options{}, ErrConfiguration
		}
	}
	if _, err = NewRules(o.PortalOrigin, o.Domain); err != nil {
		return Options{}, ErrConfiguration
	}
	if o.WorkspaceEngine != "embedded" {
		return Options{}, ErrConfiguration
	}
	return o, nil
}

type Runtime struct {
	Options Options
	Rules   Rules
	Service *Service
	client  *workspaceengine.Client
	native  *HTTPNativeVerifier
}

func NewRuntime(ctx context.Context, pool *pgxpool.Pool, path string) (*Runtime, error) {
	o, err := LoadOptions(path)
	if err != nil {
		return nil, err
	}
	cipher, err := security.LoadSecretCipher(o.DataKeyID, o.DataKeyFile, o.DataKeyringFile)
	if err != nil {
		return nil, ErrConfiguration
	}
	client, err := workspaceengine.NewEmbedded(pool, o.WorkspaceOrganizationID, o.ControlUserID)
	if err != nil {
		return nil, ErrConfiguration
	}
	rules, _ := NewRules(o.PortalOrigin, o.Domain)
	native := NewNativeVerifier(rules)
	runtime := &Runtime{Options: o, Rules: rules, client: client, native: native}
	runtime.Service = NewService(NewStore(pool, cipher, o.Environment, o.OrganizationID, rules), NewWorkspaceOwnership(client, o.WorkspaceOrganizationID, o.ControlUserID), native)
	if err = runtime.Ready(ctx); err != nil {
		runtime.Close()
		return nil, ErrConfiguration
	}
	return runtime, nil
}
func (r *Runtime) Close() { r.native.Close() }
func (r *Runtime) CheckOwner(ctx context.Context, id string) error {
	return NewWorkspaceOwnership(r.client, r.Options.WorkspaceOrganizationID, r.Options.ControlUserID).CheckOwner(ctx, id)
}
func (r *Runtime) Ready(ctx context.Context) error {
	info, err := r.client.BuildInfo(ctx)
	if err != nil || info.Version != workspaceengine.Version || info.Telemetry || info.Engine != workspaceengine.EmbeddedEngine {
		return unavailable()
	}
	user, err := r.client.User(ctx, "me")
	if err != nil || user.ID != r.Options.ControlUserID || !workspaceengine.OwnerStatusUsable(user.Status) || len(user.OrganizationIDs) != 1 || user.OrganizationIDs[0] != r.Options.WorkspaceOrganizationID {
		return unavailable()
	}
	owner := false
	for _, role := range user.Roles {
		owner = owner || role.Name == "owner"
	}
	if !owner {
		return unavailable()
	}
	return nil
}
