package worker

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type AssetProof struct {
	FilesystemID string `json:"filesystem_id"`
	Capacity     int64  `json:"capacity_bytes"`
	ResourceName string `json:"resource_name"`
	Device       string `json:"device"`
}
type storageRequest struct {
	SchemaVersion  int    `json:"schema_version"`
	AssetID        string `json:"asset_id"`
	InstanceID     string `json:"instance_id"`
	OrganizationID string `json:"org_id"`
	OwnerID        string `json:"owner_id"`
	Capacity       int64  `json:"capacity_bytes"`
}

func storageIdentity(p ExecutionPlan) (storageRequest, error) {
	for _, id := range []string{p.AssetID, p.InstanceID, p.OrganizationID, p.OwnerID} {
		if !uuidPattern.MatchString(id) {
			return storageRequest{}, ErrHostIdentity
		}
	}
	if !runtimeapproval.SupportedStorage(p.Capacity) || p.Snapshot.Spec.Storage != p.Capacity || p.ResourceName != "hc-"+p.AssetID {
		return storageRequest{}, ErrHostIdentity
	}
	return storageRequest{1, p.AssetID, p.InstanceID, p.OrganizationID, p.OwnerID, p.Capacity}, nil
}
func (h *LocalHost) inspectStorage(ctx context.Context, p ExecutionPlan, action string) (AssetProof, error) {
	request, err := storageIdentity(p)
	if err != nil {
		return AssetProof{}, err
	}
	raw, err := h.helper(ctx, "storage", action, request)
	if err != nil {
		return AssetProof{}, ErrStorageUnavailable
	}
	var proof AssetProof
	if webapi.StrictUnmarshal(raw, &proof) != nil || proof.FilesystemID != p.AssetID || proof.Capacity != p.Capacity || proof.ResourceName != p.ResourceName || proof.Device != "/dev/hc-cloud/"+p.AssetID || (p.FilesystemID != "" && proof.FilesystemID != p.FilesystemID) {
		return AssetProof{}, ErrHostIdentity
	}
	return proof, nil
}
func (h *LocalHost) volume(ctx context.Context, p ExecutionPlan, proof AssetProof, create bool) error {
	raw, err := dockerCommand(ctx, nil, "volume", "ls", "--filter", "name=^hc-"+p.AssetID+"$", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(string(raw))
	if name == "" {
		if !create {
			return ErrStorageUnavailable
		}
		args := []string{"volume", "create", "--driver", "local", "--opt", "type=ext4", "--opt", "o=nosuid,nodev", "--opt", "device=" + proof.Device}
		for _, label := range []struct{ key, value string }{{"hc.asset_id", p.AssetID}, {"hc.instance_id", p.InstanceID}, {"hc.org_id", p.OrganizationID}, {"hc.owner_id", p.OwnerID}} {
			args = append(args, "--label", label.key+"="+label.value)
		}
		args = append(args, p.ResourceName)
		if _, err = dockerCommand(ctx, nil, args...); err != nil {
			return err
		}
	} else if name != p.ResourceName {
		return ErrHostIdentity
	}
	raw, err = dockerCommand(ctx, nil, "volume", "inspect", p.ResourceName)
	if err != nil {
		return err
	}
	var values []struct {
		Name, Driver    string
		Options, Labels map[string]string
	}
	if json.Unmarshal(raw, &values) != nil || len(values) != 1 {
		return ErrHostIdentity
	}
	value := values[0]
	if value.Name != p.ResourceName || value.Driver != "local" || len(value.Options) != 3 || value.Options["device"] != proof.Device || value.Options["type"] != "ext4" || value.Options["o"] != "nosuid,nodev" {
		return ErrHostIdentity
	}
	for key, expected := range map[string]string{"hc.asset_id": p.AssetID, "hc.instance_id": p.InstanceID, "hc.org_id": p.OrganizationID, "hc.owner_id": p.OwnerID} {
		if value.Labels[key] != expected {
			return ErrHostIdentity
		}
	}
	return nil
}

// Prepare may initialize only a registered CREATE's original PREPARING asset.
// Recovery inspects an existing manifest; START never creates a missing volume.
func (h *LocalHost) Prepare(ctx context.Context, p ExecutionPlan) (AssetProof, error) {
	if _, err := storageIdentity(p); err != nil {
		return AssetProof{}, err
	}
	initialize := p.Action == "CREATE" && p.AssetStatus == "PREPARING" && p.FilesystemID == ""
	action := "inspect"
	info, err := os.Lstat(filepath.Join(h.options.AssetRoot, p.AssetID))
	if errors.Is(err, os.ErrNotExist) {
		if !initialize {
			return AssetProof{}, ErrStorageUnavailable
		}
		if err = h.Capacity(); err != nil {
			return AssetProof{}, err
		}
		action = "prepare"
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return AssetProof{}, ErrHostIdentity
	}
	proof, err := h.inspectStorage(ctx, p, action)
	if err != nil {
		return AssetProof{}, err
	}
	if err = h.volume(ctx, p, proof, initialize); err != nil {
		return AssetProof{}, err
	}
	return proof, nil
}
func (h *LocalHost) network(ctx context.Context, id, action string) error {
	if !uuidPattern.MatchString(id) {
		return ErrHostIdentity
	}
	payload := map[string]any{"schema_version": 1, "instance_id": id}
	raw, readErr := config.ReadPrivateFile(filepath.Join(h.options.RuntimeRoot, id, "config.json"))
	if readErr == nil {
		var runtime runtimeConfiguration
		if webapi.StrictUnmarshal(raw, &runtime) != nil || runtime.InstanceID != id {
			return ErrHostIdentity
		}
		if runtime.SchemaVersion == 2 {
			endpoint, parseErr := url.Parse(runtime.Endpoint)
			if parseErr != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.Path != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
				return ErrHostIdentity
			}
			port := 443
			if endpoint.Port() != "" {
				port, parseErr = strconv.Atoi(endpoint.Port())
				if parseErr != nil {
					return ErrHostIdentity
				}
			}
			payload = map[string]any{"schema_version": 2, "instance_id": id, "gateway": map[string]any{"addresses": runtime.ApprovedNetworks, "port": port}}
		} else if runtime.SchemaVersion != 1 {
			return ErrHostIdentity
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ErrHostIdentity
	}
	data, err := h.helper(ctx, "network", action, payload)
	if err != nil {
		return err
	}
	var proof struct {
		SchemaVersion int    `json:"schema_version"`
		InstanceID    string `json:"instance_id"`
		Isolated      bool   `json:"isolated"`
	}
	if webapi.StrictUnmarshal(data, &proof) != nil || proof.SchemaVersion != 1 || proof.InstanceID != id || proof.Isolated != (action != "remove") {
		return ErrHostIdentity
	}
	return nil
}

// ConfirmAbsent is a physical observation, not a workspace status inference. It is
// the only safe source for compute release. Missing storage never gets replaced.
func (h *LocalHost) ConfirmAbsent(ctx context.Context, p ExecutionPlan, workspaceAbsent bool) (Completion, error) {
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return Completion{}, err
	}
	if container != nil {
		return Completion{}, ErrHostIdentity
	}
	if err = h.network(ctx, p.InstanceID, "remove"); err != nil {
		return Completion{}, err
	}
	result := Completion{ComputeAbsent: true, WorkspaceAbsent: workspaceAbsent}
	if p.AssetStatus == "RESTORING" || p.AssetStatus == "RECLAIM_PENDING" {
		return result, nil
	}
	proof, err := h.inspectStorage(ctx, p, "inspect")
	if err == nil && h.volume(ctx, p, proof, false) == nil {
		result.AssetReady = true
		result.FilesystemID = proof.FilesystemID
	}
	// Cleanup remains possible with broken storage. Its quota stays conservative.
	return result, nil
}
