package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

// Options contains references to private files, never inline credentials.
// Worker does not require Portal/OIDC secrets or a Redis connection to execute
// an already accepted PostgreSQL command.
type Options struct {
	SchemaVersion           int                              `json:"schema_version"`
	WorkspaceEngine         string                           `json:"workspace_engine"`
	WorkspaceOrganizationID string                           `json:"workspace_organization_id"`
	ControlUserID           string                           `json:"control_user_id"`
	Environment             string                           `json:"environment"`
	DataKeyFile             string                           `json:"data_key_file"`
	DataKeyringFile         string                           `json:"data_keyring_file,omitempty"`
	DataKeyID               string                           `json:"data_key_id"`
	ModelControlKeyFile     string                           `json:"model_control_key_file,omitempty"`
	ModelAllowlistFile      string                           `json:"model_allowlist_file"`
	Host                    HostOptions                      `json:"host"`
	Terraform               workspaceengine.TerraformOptions `json:"terraform"`
}

var optionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var ErrWorkerConfiguration = errors.New("Worker requires valid private configuration in HC_WORKER_CONFIG_FILE")

func LoadOptions(path string) (Options, error) {
	raw, err := config.ReadPrivateFile(path)
	if err != nil {
		return Options{}, ErrWorkerConfiguration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var o Options
	if decoder.Decode(&o) != nil || decoder.Decode(new(any)) != io.EOF {
		return Options{}, ErrWorkerConfiguration
	}
	if o.SchemaVersion != 1 || !uuidPattern.MatchString(o.WorkspaceOrganizationID) || !uuidPattern.MatchString(o.ControlUserID) || !optionName.MatchString(o.Environment) || !optionName.MatchString(o.DataKeyID) {
		return Options{}, ErrWorkerConfiguration
	}
	if o.ModelControlKeyFile != "" && (!filepath.IsAbs(o.ModelControlKeyFile) || filepath.Clean(o.ModelControlKeyFile) != o.ModelControlKeyFile) {
		return Options{}, ErrWorkerConfiguration
	}
	if o.DataKeyringFile != "" && (!filepath.IsAbs(o.DataKeyringFile) || filepath.Clean(o.DataKeyringFile) != o.DataKeyringFile) {
		return Options{}, ErrWorkerConfiguration
	}
	for _, p := range []string{o.DataKeyFile, o.ModelAllowlistFile, o.Terraform.Binary, o.Terraform.Root, o.Terraform.CLIConfig} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return Options{}, ErrWorkerConfiguration
		}
	}
	if o.WorkspaceEngine != "embedded" || len(o.Terraform.BinarySHA256) != 64 {
		return Options{}, ErrWorkerConfiguration
	}
	return o, nil
}
