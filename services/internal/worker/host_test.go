package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestPrivateModelRuntimeRequiresTrustedFrozenManifest(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	image := "sha256:" + strings.Repeat("a", 64)
	host, err := NewLocalHost(HostOptions{RuntimeRoot: root, AssetRoot: root, RuntimeImage: image, StorageImage: image, NetworkImage: image, Domain: "harness.test", StorageBudget: 100 << 30})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := settings.ParsePolicy([]byte(`{"schema_version":1,"gateways":[{"endpoint":"https://models.enterprise.example:14000","revision":"v1","addresses":["10.50.0.10/32"],"models":[],"probe_retries_disabled":true,"probe_fallbacks_disabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	host.WithModelPolicy(policy)
	gateway, _ := policy.Gateway(policy.Gateways[0].Endpoint)
	manifest := gateway.Manifest(settings.GatewayModel{ID: "private-alias", ResponseModels: []string{"kimi-k2.5"}, ContextWindow: 32768, MaxOutputTokens: 4096, Streaming: true, ToolUse: true})
	p := ExecutionPlan{InstanceID: webapi.ID(), OrganizationID: webapi.ID(), OwnerID: webapi.ID(), AssetID: webapi.ID(), RuntimeGeneration: webapi.ID(), Capacity: 10 << 30, Endpoint: gateway.Endpoint, modelManifest: &manifest}
	p.ResourceName = "hc-" + p.AssetID
	p.Snapshot = instances.Snapshot{ImageDigest: image, Spec: instances.ResourceSpec{CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}, ModelVersion: 1, Provider: "platform-models", Protocol: "anthropic-messages", Model: manifest.ID, ApprovalDigest: manifest.Digest}
	config, err := host.runtimeConfiguration(p)
	if err != nil || config.SchemaVersion != 2 || len(config.ApprovedIPs) != 1 || config.ApprovedIPs[0] != "10.50.0.10" {
		t.Fatal("database private model cannot configure runtime", err)
	}
	manifest.ResponseModels = []string{"other-user-model"}
	if _, err = host.runtimeConfiguration(p); err == nil {
		t.Fatal("tampered private manifest configured runtime")
	}
	p.modelManifest = nil
	if _, err = host.runtimeConfiguration(p); err == nil {
		t.Fatal("unapproved alias configured runtime")
	}
}

func TestHostRefusesForeignOrUnsafeComputeBeforeStarting(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	plan := ExecutionPlan{InstanceID: "00000000-0000-4000-8000-000000000001", AssetID: "00000000-0000-4000-8000-000000000002", OrganizationID: "00000000-0000-4000-8000-000000000003", OwnerID: "00000000-0000-4000-8000-000000000004", WorkspaceOwner: "00000000-0000-4000-8000-000000000005"}
	workspace := "00000000-0000-4000-8000-000000000006"
	plan.WorkspaceID = &workspace
	plan.Snapshot.ImageDigest = "sha256:" + strings.Repeat("a", 64)
	plan.Snapshot.Spec = instances.ResourceSpec{CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}
	fixture := func() map[string]any {
		return map[string]any{
			"Image": plan.Snapshot.ImageDigest, "Name": "/hc-" + plan.InstanceID, "State": map[string]any{"Status": "created", "Running": false},
			"Config":          map[string]any{"User": "1000:1000", "Labels": map[string]any{"hc.instance_id": plan.InstanceID, "hc.asset_id": plan.AssetID, "hc.workspace_owner_id": plan.WorkspaceOwner, "hc.workspace_id": workspace}},
			"HostConfig":      map[string]any{"Privileged": false, "ReadonlyRootfs": true, "NanoCpus": 2000000000, "Memory": int64(4 << 30), "MemorySwap": int64(4 << 30), "PidsLimit": 0, "CapDrop": []string{"ALL"}, "CapAdd": []string{}, "SecurityOpt": []string{"no-new-privileges:true"}, "PortBindings": map[string]any{}, "NetworkMode": "hc-" + plan.InstanceID, "RestartPolicy": map[string]any{"Name": "no"}, "Tmpfs": map[string]any{"/tmp": "rw,noexec,nosuid,nodev,size=256m,uid=1000,gid=1000"}},
			"Mounts":          []any{map[string]any{"Type": "volume", "Name": "hc-" + plan.AssetID, "Destination": "/data", "RW": true}, map[string]any{"Type": "bind", "Source": filepath.Join(root, plan.InstanceID), "Destination": "/run/harness", "RW": true}, map[string]any{"Type": "bind", "Source": filepath.Join(root, plan.InstanceID, "model-key"), "Destination": "/run/secrets/model-key", "RW": false}},
			"NetworkSettings": map[string]any{"Networks": map[string]any{"hc-" + plan.InstanceID: map[string]any{"IPAddress": ""}}},
		}
	}
	for _, name := range []string{"valid", "expanded", "owner", "workspace", "image", "root", "privileged", "socket", "rw-key", "capabilities", "memory", "ports", "host-network", "tmp-exec"} {
		t.Run(name, func(t *testing.T) {
			value := fixture()
			selectedPlan := plan
			config := value["Config"].(map[string]any)
			host := value["HostConfig"].(map[string]any)
			labels := config["Labels"].(map[string]any)
			switch name {
			case "expanded":
				selectedPlan.Snapshot.Spec.CPU = 16000
				selectedPlan.Snapshot.Spec.Memory = 64 << 30
				host["NanoCpus"], host["Memory"], host["MemorySwap"], host["PidsLimit"] = int64(16000000000), int64(64<<30), int64(64<<30), int64(256)
			case "owner":
				labels["hc.workspace_owner_id"] = "foreign"
			case "workspace":
				labels["hc.workspace_id"] = "foreign"
			case "image":
				value["Image"] = "other-image"
			case "root":
				config["User"] = "0"
			case "privileged":
				host["Privileged"] = true
			case "socket":
				value["Mounts"] = append(value["Mounts"].([]any), map[string]any{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock", "RW": true})
			case "rw-key":
				value["Mounts"].([]any)[2].(map[string]any)["RW"] = true
			case "capabilities":
				host["CapAdd"] = []string{"SYS_ADMIN"}
			case "memory":
				host["Memory"] = int64(8 << 30)
			case "ports":
				host["PortBindings"] = map[string]any{"3080/tcp": []any{map[string]any{"HostPort": "13080"}}}
			case "host-network":
				host["NetworkMode"] = "host"
			case "tmp-exec":
				host["Tmpfs"] = map[string]any{"/tmp": "rw,exec,nosuid,nodev,size=256m"}
			}
			raw, _ := json.Marshal(value)
			var observed dockerContainer
			if err := json.Unmarshal(raw, &observed); err != nil {
				t.Fatal(err)
			}
			err := validateContainer(observed, selectedPlan, root, true)
			if name == "valid" || name == "expanded" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrHostIdentity) {
				t.Fatalf("unsafe %s compute accepted: %v", name, err)
			}
		})
	}
	// The trusted Terraform baseline may exist only before host activation. Once
	// running, limits must exactly match the frozen instance resources.
	for _, name := range []string{"baseline-before-activation", "baseline-after-activation", "selected-running", "wrong-cpu", "wrong-memory-swap", "unsupported-spec"} {
		t.Run(name, func(t *testing.T) {
			selected := plan
			selected.Snapshot.Spec = instances.ResourceSpec{CPU: 1000, Memory: 2 << 30, Storage: 10 << 30}
			value := fixture()
			initial := name == "baseline-before-activation"
			host := value["HostConfig"].(map[string]any)
			if !initial {
				value["State"] = map[string]any{"Status": "running", "Running": true}
				host["PidsLimit"] = 256
			}
			if name != "baseline-before-activation" && name != "baseline-after-activation" {
				host["NanoCpus"] = int64(1000000000)
				host["Memory"] = int64(2 << 30)
				host["MemorySwap"] = int64(2 << 30)
			}
			switch name {
			case "wrong-cpu":
				host["NanoCpus"] = int64(2000000000)
			case "wrong-memory-swap":
				host["MemorySwap"] = int64(4 << 30)
			case "unsupported-spec":
				selected.Snapshot.Spec.Memory = 3 << 30
				host["Memory"] = int64(3 << 30)
				host["MemorySwap"] = int64(3 << 30)
			}
			raw, _ := json.Marshal(value)
			var observed dockerContainer
			if err := json.Unmarshal(raw, &observed); err != nil {
				t.Fatal(err)
			}
			err := validateContainer(observed, selected, root, initial)
			if name == "baseline-before-activation" || name == "selected-running" {
				if err != nil {
					t.Fatal("valid selected resource lifecycle rejected", err)
				}
			} else if !errors.Is(err, ErrHostIdentity) {
				t.Fatal("mismatched resource limits accepted", err)
			}
		})
	}
}

func TestHostUpgradeAcceptsOnlyExplicitHistoricalImages(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	current, previous, foreign := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)
	o := HostOptions{RuntimeRoot: root, AssetRoot: root, RuntimeImage: current, StorageImage: current, NetworkImage: current, Domain: "harness.test", StorageBudget: 100 << 30, PreviousRuntimeImages: []string{previous}}
	h, err := NewLocalHost(o)
	if err != nil {
		t.Fatal(err)
	}
	p := ExecutionPlan{InstanceID: webapi.ID(), OrganizationID: webapi.ID(), OwnerID: webapi.ID(), AssetID: webapi.ID(), RuntimeGeneration: webapi.ID(), Capacity: 10 << 30, Endpoint: "https://api.deepseek.com/anthropic"}
	p.ResourceName = "hc-" + p.AssetID
	p.Snapshot = instances.Snapshot{ImageDigest: previous, Spec: instances.ResourceSpec{CPU: 2000, Memory: 4 << 30, Storage: 10 << 30}, ModelVersion: 1, Provider: "deepseek-official", Protocol: "deepseek-messages", Model: "fixture"}
	if _, err = h.runtimeConfiguration(p); err != nil {
		t.Fatal("frozen previous image rejected", err)
	}
	p.Snapshot.ImageDigest = foreign
	if _, err = h.runtimeConfiguration(p); err == nil {
		t.Fatal("unapproved historical image accepted")
	}
	for _, images := range [][]string{{"latest"}, {current}, {previous, previous}, {previous, foreign, current, previous, foreign}} {
		o.PreviousRuntimeImages = images
		if _, err = NewLocalHost(o); err == nil {
			t.Fatal("unsafe historical image list accepted")
		}
	}
}
func TestHostLockFencesConcurrentExecutionAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	data := t.TempDir()
	os.Chmod(data, 0700)
	image := "sha256:" + strings.Repeat("a", 64)
	host, err := NewLocalHost(HostOptions{RuntimeRoot: root, AssetRoot: data, RuntimeImage: image, StorageImage: image, NetworkImage: image, Domain: "harness.test", StorageBudget: 100 << 30})
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000001"
	release, err := host.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := host.Acquire(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("concurrent lock ignored cancellation: %v", err)
	}
	release()
	if release, err = host.Acquire(context.Background(), id); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
	if _, err := host.Acquire(context.Background(), "../../foreign"); !errors.Is(err, ErrHostIdentity) {
		t.Fatalf("untrusted path entered lock: %v", err)
	}
	if err := os.Symlink(data, filepath.Join(root, "00000000-0000-4000-8000-000000000002")); err != nil {
		t.Fatal(err)
	}
	plan := ExecutionPlan{InstanceID: "00000000-0000-4000-8000-000000000002"}
	if err := host.Configure(context.Background(), plan); err == nil {
		t.Fatal("symlinked runtime channel was accepted")
	}
}
