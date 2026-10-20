package worker

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// ResourceObservation contains identities and allocation metadata only. It
// excludes environment variables, credentials, IPs and user file contents.
type ResourceObservation struct {
	Kind            string `json:"kind"`
	ExternalID      string `json:"external_id"`
	InstanceID      string `json:"instance_id,omitempty"`
	AssetID         string `json:"asset_id,omitempty"`
	OrganizationID  string `json:"organization_id,omitempty"`
	OwnerID         string `json:"owner_id,omitempty"`
	WorkspaceOwner  string `json:"workspace_owner,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	TemplateVersion string `json:"template_version,omitempty"`
	Status          string `json:"status,omitempty"`
	Capacity        int64  `json:"capacity_bytes,omitempty"`
	CPU             int64  `json:"cpu_millicores,omitempty"`
	Memory          int64  `json:"memory_bytes,omitempty"`
	IdentityValid   bool   `json:"identity_valid"`
}

//go:embed inventory.py
var inventoryProgram string

var dockerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
var dockerVolumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

func safeResourceID(raw string) string {
	if uuidPattern.MatchString(raw) || (strings.HasPrefix(raw, "hc-") && uuidPattern.MatchString(strings.TrimPrefix(raw, "hc-"))) || dockerID.MatchString(raw) {
		return raw
	}
	sum := sha256.Sum256([]byte(raw))
	return "unknown-" + hex.EncodeToString(sum[:])
}

// Inventory never mounts or repairs a disk, creates a Docker volume, or reads
// runtime secret channels. The short-lived scanner has only a read-only bind.
func (h *LocalHost) Inventory(ctx context.Context) ([]ResourceObservation, error) {
	name := "hc-inventory-" + randomHelperID()
	raw, err := dockerCommand(ctx, nil, "run", "--rm", "--name", name, "--network", "none", "--read-only", "--security-opt", "no-new-privileges", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--pids-limit", "32", "--memory", "128m", "--cpus", "0.5", "--mount", "type=bind,src="+h.options.AssetRoot+",dst=/assets,readonly", "--entrypoint", "python3", h.options.StorageImage, "-c", inventoryProgram)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = dockerCommand(cleanup, nil, "rm", "-f", name)
		return nil, err
	}
	var out []ResourceObservation
	if json.Unmarshal(raw, &out) != nil || len(out) > 1000 {
		return nil, ErrHostIdentity
	}
	assets := map[string]ResourceObservation{}
	for _, asset := range out {
		assets[asset.AssetID] = asset
	}
	for _, kind := range []string{"volume", "container", "network"} {
		var args []string
		switch kind {
		case "container":
			args = []string{"ps", "-a", "--filter", "name=^/hc-", "--format", "{{.ID}}"}
		case "volume":
			args = []string{"volume", "ls", "--filter", "name=^hc-", "--format", "{{.Name}}"}
		case "network":
			args = []string{"network", "ls", "--filter", "name=^hc-", "--format", "{{.ID}}"}
		}
		listed, err := dockerCommand(ctx, nil, args...)
		if err != nil {
			return nil, err
		}
		ids := strings.Fields(string(listed))
		if len(ids) > 1000 {
			return nil, ErrHostIdentity
		}
		for _, id := range ids {
			if (kind == "volume" && !dockerVolumeName.MatchString(id)) || (kind != "volume" && !dockerID.MatchString(id)) {
				return nil, ErrHostIdentity
			}
			var format string
			switch kind {
			case "container":
				// A selective inspect prevents Docker's full Config.Env from ever
				// entering this process or a persisted recovery report.
				format = `{"ID":{{json .Id}},"Name":{{json .Name}},"Labels":{{json .Config.Labels}},"State":{{json .State.Status}},"Memory":{{json .HostConfig.Memory}},"NanoCPU":{{json .HostConfig.NanoCpus}}}`
			case "volume":
				format = `{"ID":{{json .Name}},"Name":{{json .Name}},"Labels":{{json .Labels}},"Driver":{{json .Driver}},"Options":{{json .Options}}}`
			case "network":
				format = `{"ID":{{json .Id}},"Name":{{json .Name}},"Labels":{{json .Labels}}}`
			}
			args = []string{kind, "inspect", "--format", format, id}
			raw, err := dockerCommand(ctx, nil, args...)
			if err != nil {
				return nil, err
			}
			var v struct {
				ID, Name, State, Driver string
				Labels, Options         map[string]string
				Memory, NanoCPU         int64
			}
			if json.Unmarshal(raw, &v) != nil {
				return nil, ErrHostIdentity
			}
			resourceName := strings.TrimPrefix(v.Name, "/")
			// The local control plane uses hc-local-*; it is not a DSH
			// resource. Every instance/helper with the platform label is kept.
			if !uuidPattern.MatchString(strings.TrimPrefix(resourceName, "hc-")) && v.Labels["hc.instance_id"] == "" {
				continue
			}
			r := ResourceObservation{Kind: strings.ToUpper(kind), ExternalID: safeResourceID(v.ID), InstanceID: v.Labels["hc.instance_id"], AssetID: v.Labels["hc.asset_id"], OrganizationID: v.Labels["hc.org_id"], OwnerID: v.Labels["hc.owner_id"], WorkspaceOwner: v.Labels["hc.workspace_owner_id"], WorkspaceID: v.Labels["hc.workspace_id"]}
			r.IdentityValid = uuidPattern.MatchString(r.InstanceID)
			switch kind {
			case "volume":
				r.Capacity = 100 << 30
				asset, found := assets[r.AssetID]
				if found && asset.IdentityValid && asset.InstanceID == r.InstanceID && asset.OrganizationID == r.OrganizationID && asset.OwnerID == r.OwnerID {
					r.Capacity = asset.Capacity
				} else {
					r.IdentityValid = false
				}
				r.IdentityValid = r.IdentityValid && uuidPattern.MatchString(r.AssetID) && uuidPattern.MatchString(r.OrganizationID) && uuidPattern.MatchString(r.OwnerID) && resourceName == "hc-"+r.AssetID && v.Driver == "local" && len(v.Options) == 3 && v.Options["device"] == "/dev/hc-cloud/"+r.AssetID && v.Options["type"] == "ext4" && v.Options["o"] == "nosuid,nodev"
			case "container":
				r.Status, r.CPU, r.Memory = v.State, v.NanoCPU/1000000, v.Memory
				r.IdentityValid = r.IdentityValid && resourceName == "hc-"+r.InstanceID && uuidPattern.MatchString(r.AssetID) && uuidPattern.MatchString(r.WorkspaceOwner) && uuidPattern.MatchString(r.WorkspaceID)
			case "network":
				r.IdentityValid = r.IdentityValid && resourceName == "hc-"+r.InstanceID
			}
			// Invalid arbitrary labels must not be copied into diagnostics.
			for _, value := range []*string{&r.InstanceID, &r.AssetID, &r.OrganizationID, &r.OwnerID, &r.WorkspaceOwner, &r.WorkspaceID} {
				if *value != "" && !uuidPattern.MatchString(*value) {
					r.IdentityValid = false
					*value = ""
				}
			}
			out = append(out, r)
		}
	}
	return out, nil
}
