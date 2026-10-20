package worker

import (
	"context"
	"encoding/json"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"path/filepath"
	"strings"
)

type dockerContainer struct {
	ID, Image, Name string
	State           struct {
		Status  string
		Running bool
	}
	Config struct {
		User   string
		Labels map[string]string
	}
	HostConfig struct {
		Privileged, ReadonlyRootfs                              bool
		NanoCpus, Memory, MemorySwap, PidsLimit                 int64
		CapDrop, CapAdd, SecurityOpt, Binds, VolumesFrom, Links []string
		Devices, DeviceRequests                                 []json.RawMessage
		PortBindings                                            map[string]json.RawMessage
		NetworkMode                                             string
		RestartPolicy                                           struct{ Name string }
		Tmpfs                                                   map[string]string
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Networks map[string]struct{ IPAddress string }
	}
}

func validateContainer(c dockerContainer, p ExecutionPlan, root string, initial bool) error {
	if p.WorkspaceID == nil || c.Name != "/hc-"+p.InstanceID || c.Image != p.Snapshot.ImageDigest || c.Config.User != "1000:1000" || c.Config.Labels["hc.instance_id"] != p.InstanceID || c.Config.Labels["hc.asset_id"] != p.AssetID || c.Config.Labels["hc.workspace_owner_id"] != p.WorkspaceOwner || c.Config.Labels["hc.workspace_id"] != *p.WorkspaceID {
		return ErrHostIdentity
	}
	if !runtimeapproval.SupportedResources(p.Snapshot.Spec.CPU, p.Snapshot.Spec.Memory, p.Snapshot.Spec.Storage) {
		return ErrHostIdentity
	}
	host := c.HostConfig
	// Terraform creates a stopped baseline. Only this unstarted container may
	// use baseline limits before the Worker applies the frozen selected tuple.
	resourcesMatch := host.NanoCpus == p.Snapshot.Spec.CPU*1000000 && host.Memory == p.Snapshot.Spec.Memory && host.MemorySwap == p.Snapshot.Spec.Memory
	baseline := initial && !c.State.Running && (c.State.Status == "created" || c.State.Status == "exited") && host.PidsLimit == 0 && host.NanoCpus == 2000000000 && host.Memory == 4<<30 && host.MemorySwap == 4<<30
	if host.Privileged || !host.ReadonlyRootfs || (!resourcesMatch && !baseline) || len(host.CapDrop) != 1 || host.CapDrop[0] != "ALL" || len(host.CapAdd) > 0 || len(host.SecurityOpt) != 1 || host.SecurityOpt[0] != "no-new-privileges:true" || len(host.PortBindings) > 0 || len(host.Devices) > 0 || len(host.DeviceRequests) > 0 || len(host.Binds) > 0 || len(host.VolumesFrom) > 0 || len(host.Links) > 0 || host.RestartPolicy.Name != "no" {
		return ErrHostIdentity
	}
	if host.NetworkMode != "hc-"+p.InstanceID && host.NetworkMode != "default" && host.NetworkMode != "bridge" {
		return ErrHostIdentity
	}
	if host.PidsLimit != 256 && !(initial && !c.State.Running && (c.State.Status == "created" || c.State.Status == "exited") && host.PidsLimit == 0) {
		return ErrHostIdentity
	}
	if len(host.Tmpfs) != 1 {
		return ErrHostIdentity
	}
	tmp, ok := host.Tmpfs["/tmp"]
	if !ok {
		return ErrHostIdentity
	}
	options := map[string]bool{}
	for _, item := range strings.Split(tmp, ",") {
		if options[item] {
			return ErrHostIdentity
		}
		options[item] = true
	}
	for _, required := range []string{"rw", "noexec", "nosuid", "nodev", "size=256m", "uid=1000", "gid=1000"} {
		if !options[required] {
			return ErrHostIdentity
		}
	}
	if len(options) != 7 || len(c.Mounts) != 3 || len(c.NetworkSettings.Networks) != 1 {
		return ErrHostIdentity
	}
	if _, ok := c.NetworkSettings.Networks["hc-"+p.InstanceID]; !ok {
		return ErrHostIdentity
	}
	seen := map[string]bool{}
	channel := filepath.Join(root, p.InstanceID)
	for _, mount := range c.Mounts {
		if seen[mount.Destination] {
			return ErrHostIdentity
		}
		seen[mount.Destination] = true
		switch mount.Destination {
		case "/data":
			if mount.Type != "volume" || mount.Name != "hc-"+p.AssetID || !mount.RW {
				return ErrHostIdentity
			}
		case "/run/harness":
			if mount.Type != "bind" || mount.Source != channel || !mount.RW {
				return ErrHostIdentity
			}
		case "/run/secrets/model-key":
			if mount.Type != "bind" || mount.Source != filepath.Join(channel, "model-key") || mount.RW {
				return ErrHostIdentity
			}
		default:
			return ErrHostIdentity
		}
	}
	return nil
}
func (h *LocalHost) container(ctx context.Context, id string) (*dockerContainer, error) {
	if !uuidPattern.MatchString(id) {
		return nil, ErrHostIdentity
	}
	raw, err := dockerCommand(ctx, nil, "ps", "-a", "--filter", "name=^/hc-"+id+"$", "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	found := strings.TrimSpace(string(raw))
	if found == "" {
		return nil, nil
	}
	if strings.ContainsAny(found, "\r\n") {
		return nil, ErrHostIdentity
	}
	raw, err = dockerCommand(ctx, nil, "inspect", "hc-"+id)
	if err != nil {
		return nil, err
	}
	var rows []dockerContainer
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
		return nil, ErrHostIdentity
	}
	return &rows[0], nil
}
