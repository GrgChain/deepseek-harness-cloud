package worker

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

//go:embed asset_operations.py
var assetOperationsProgram string

//go:embed storage_loop.py
var storageLoopProgram string
var backupDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var liveBrowseFailurePattern = regexp.MustCompile(`^harness-cloud: storage browse failed \[([A-Z_]{1,64})\]; read-only data retained\s*$`)

func (h *LocalHost) AssertAssetIdle(ctx context.Context, p ExecutionPlan) error {
	if _, err := storageIdentity(p); err != nil {
		return err
	}
	if err := h.EnsureHelpersAbsent(ctx, p.InstanceID); err != nil {
		return err
	}
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return err
	}
	if container != nil {
		return ErrHostIdentity
	}
	raw, err := dockerCommand(ctx, nil, "ps", "-a", "--filter", "volume="+p.ResourceName, "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return ErrHostIdentity
	}
	return nil
}
func (h *LocalHost) InspectAsset(ctx context.Context, p ExecutionPlan) error {
	proof, err := h.inspectStorage(ctx, p, "inspect")
	if err != nil {
		return err
	}
	return h.volume(ctx, p, proof, false)
}
func (h *LocalHost) ArchiveAsset(ctx context.Context, p ExecutionPlan, output io.Writer) (err error) {
	if output == nil {
		return ErrHostIdentity
	}
	if err := h.AssertAssetIdle(ctx, p); err != nil {
		return err
	}
	request, err := storageIdentity(p)
	if err != nil {
		return err
	}
	remount, err := h.offlineVolume(ctx, p)
	if err != nil {
		return err
	}
	defer func() {
		if restoreErr := remount(); restoreErr != nil && err == nil {
			err = restoreErr
		}
	}()
	return h.assetOperation(ctx, p.InstanceID, "archive", map[string]any{"target": request}, nil, output)
}
func (h *LocalHost) RestoreAsset(ctx context.Context, target, source ExecutionPlan, sha string, input io.Reader) (err error) {
	t, err := storageIdentity(target)
	if err != nil {
		return err
	}
	s, err := storageIdentity(source)
	if err != nil {
		return err
	}
	if input == nil || !backupDigest.MatchString(sha) || t.AssetID == s.AssetID || t.InstanceID == s.InstanceID || t.OwnerID != s.OwnerID || t.OrganizationID != s.OrganizationID {
		return ErrHostIdentity
	}
	if err = h.AssertAssetIdle(ctx, target); err != nil {
		return err
	}
	if err = h.Capacity(); err != nil {
		return err
	}
	remount, err := h.offlineVolume(ctx, target)
	if err != nil {
		return err
	}
	defer func() {
		if restoreErr := remount(); restoreErr != nil && err == nil {
			err = restoreErr
		}
	}()
	return h.assetOperation(ctx, target.InstanceID, "restore", map[string]any{"target": t, "source": s, "backup_sha256": sha}, input, io.Discard)
}

// Docker may retain its local volume mount in the daemon's mount namespace
// after the final container exits. Removing only the validated volume metadata
// forces an offline flush; the original independent disk remains untouched.
func (h *LocalHost) offlineVolume(ctx context.Context, p ExecutionPlan) (func() error, error) {
	proof, err := h.inspectStorage(ctx, p, "inspect")
	if err != nil {
		return nil, err
	}
	raw, err := dockerCommand(ctx, nil, "volume", "ls", "--filter", "name=^"+p.ResourceName+"$", "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) != "" {
		if err = h.volume(ctx, p, proof, false); err != nil {
			return nil, err
		}
		if _, err = dockerCommand(ctx, nil, "volume", "rm", p.ResourceName); err != nil {
			return nil, err
		}
	}
	return func() error {
		bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		proof, err := h.inspectStorage(bounded, p, "inspect")
		if err != nil {
			return err
		}
		return h.volume(bounded, p, proof, true)
	}, nil
}
func (h *LocalHost) ReclaimAsset(ctx context.Context, p ExecutionPlan) error {
	request, err := storageIdentity(p)
	if err != nil {
		return err
	}
	if err = h.AssertAssetIdle(ctx, p); err != nil {
		return err
	}
	raw, err := dockerCommand(ctx, nil, "volume", "ls", "--filter", "name=^"+p.ResourceName+"$", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) != "" {
		if err = h.InspectAsset(ctx, p); err != nil {
			return err
		}
		if _, err = dockerCommand(ctx, nil, "volume", "rm", p.ResourceName); err != nil {
			return err
		}
	}
	return h.assetOperation(ctx, p.InstanceID, "reclaim", map[string]any{"target": request}, nil, io.Discard)
}

func (h *LocalHost) BrowseStorage(ctx context.Context, p ExecutionPlan, input instances.StorageBrowseInput) (directory instances.StorageDirectory, file instances.StorageFile, err error) {
	if instances.ValidateStorageBrowse(input) != nil {
		return directory, file, ErrHostIdentity
	}
	request, err := storageIdentity(p)
	if err != nil {
		return directory, file, err
	}
	inputMap := map[string]any{"target": request, "mode": input.Action, "root": input.Root, "path": input.Path}
	if input.After != "" || input.Action == "LIST" {
		inputMap["after"] = input.After
	}
	out := &boundedBuffer{limit: instances.StorageDownloadMaxBytes}
	if p.Observed == "RUNNING" && p.Desired == "RUNNING" {
		if p.AssetStatus != "READY" {
			return directory, file, instances.ErrStorageBrowseUnavailable
		}
		if err = h.browseRunningVolume(ctx, p, inputMap, out); err != nil {
			return directory, file, err
		}
	} else {
		if p.Observed != "STOPPED" && p.Observed != "DELETED" || p.Observed == "STOPPED" && (p.Desired != "STOPPED" || p.AssetStatus != "READY") || p.Observed == "DELETED" && (p.Desired != "DELETED" || p.AssetStatus != "RETAINED") {
			return directory, file, instances.ErrStorageBrowseUnavailable
		}
		container, inspectErr := h.container(ctx, p.InstanceID)
		if inspectErr != nil {
			return directory, file, inspectErr
		}
		if container != nil {
			return directory, file, instances.ErrStorageBrowseUnavailable
		}
		if err = h.AssertAssetIdle(ctx, p); err != nil {
			return directory, file, instances.ErrStorageBrowseBusy
		}
		remount, mountErr := h.offlineVolume(ctx, p)
		if mountErr != nil {
			return directory, file, mountErr
		}
		defer func() {
			if restoreErr := remount(); restoreErr != nil {
				err = instances.ErrStorageBrowseUnavailable
			}
		}()
		if err = h.assetOperation(ctx, p.InstanceID, "browse", inputMap, nil, out); err != nil {
			return directory, file, err
		}
	}
	if input.Action == "LIST" {
		if webapi.StrictUnmarshal(out.Bytes(), &directory) != nil || directory.Root != input.Root || directory.Path != input.Path || len(directory.Entries) > instances.StorageDirectoryPageSize {
			return instances.StorageDirectory{}, file, instances.ErrStorageBrowseUnavailable
		}
		return directory, file, nil
	}
	content := append([]byte(nil), out.Bytes()...)
	if input.Action == "PREVIEW" && (!utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0) {
		return directory, file, instances.ErrStoragePreviewUnavailable
	}
	file = instances.StorageFile{Name: path.Base(input.Path), Content: content}
	return directory, file, nil
}

func (h *LocalHost) browseRunningVolume(ctx context.Context, p ExecutionPlan, request any, output io.Writer) error {
	if !h.supportsRuntimeImage(p.Snapshot.ImageDigest) || p.WorkspaceID == nil || p.AssetStatus != "READY" {
		return fmt.Errorf("live browse plan validation failed: %w", ErrHostIdentity)
	}
	if err := h.EnsureHelpersAbsent(ctx, p.InstanceID); err != nil {
		return fmt.Errorf("live browse helper guard failed: %w", err)
	}
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return fmt.Errorf("live browse runtime inspect failed: %w", err)
	}
	if container == nil {
		return fmt.Errorf("live browse runtime container is absent: %w", ErrHostIdentity)
	}
	if !container.State.Running || container.State.Status != "running" || validateContainer(*container, p, h.options.RuntimeRoot, false) != nil {
		return fmt.Errorf("live browse runtime identity validation failed: %w", ErrHostIdentity)
	}
	proof := AssetProof{FilesystemID: p.AssetID, Capacity: p.Capacity, ResourceName: p.ResourceName, Device: "/dev/hc-cloud/" + p.AssetID}
	if err = h.volume(ctx, p, proof, false); err != nil {
		return fmt.Errorf("live browse volume validation failed: %w", err)
	}
	users, err := dockerCommand(ctx, nil, "ps", "-a", "--filter", "volume="+p.ResourceName, "--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("live browse volume user check failed: %w", err)
	}
	if names := strings.Fields(string(users)); len(names) != 1 || names[0] != "hc-"+p.InstanceID {
		return fmt.Errorf("live browse volume has unexpected users: %w", ErrHostIdentity)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("live browse request encoding failed: %w", ErrHostIdentity)
	}
	name := "hc-asset-browse-" + randomHelperID()
	args := []string{"--host", "unix:///var/run/docker.sock", "run", "--rm", "--name", name, "--label", "hc.worker-helper=1", "--label", "hc.instance_id=" + p.InstanceID, "-i", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--pids-limit", "32", "--memory", "128m", "--cpus", "0.25", "--user", "1000:1000", "--mount", "type=volume,src=" + p.ResourceName + ",dst=/data,readonly,volume-nocopy", "--entrypoint", "python3", h.options.StorageImage, "-B", "-c", storageLoopProgram + "\n" + assetOperationsProgram, "browse-live", string(data)}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	command.WaitDelay = 2 * time.Second
	command.Stdout = output
	diagnostic := &boundedBuffer{limit: 8192}
	command.Stderr = diagnostic
	if command.Run() == nil {
		return nil
	}
	phase := ""
	if match := liveBrowseFailurePattern.FindStringSubmatch(diagnostic.String()); len(match) == 2 {
		phase = match[1]
		slog.Warn("live read-only storage browse failed", "phase", phase)
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = dockerCommand(cleanup, nil, "rm", "-f", name)
	switch phase {
	case "BROWSE_FILE_UNAVAILABLE":
		return instances.ErrStorageFileUnavailable
	case "BROWSE_PREVIEW_UNAVAILABLE":
		return instances.ErrStoragePreviewUnavailable
	case "BROWSE_TOO_LARGE":
		return instances.ErrStorageFileTooLarge
	case "BROWSE_DIRECTORY_TOO_LARGE":
		return instances.ErrStorageDirectoryTooLarge
	default:
		return fmt.Errorf("live browse helper failed: %s: %w", strings.TrimSpace(diagnostic.String()), instances.ErrStorageBrowseUnavailable)
	}
}

func (h *LocalHost) assetOperation(ctx context.Context, id, action string, request any, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	raw, err := json.Marshal(request)
	if err != nil {
		return ErrHostIdentity
	}
	name := "hc-asset-op-" + randomHelperID()
	args := []string{"--host", "unix:///var/run/docker.sock", "run", "--rm", "--name", name, "--label", "hc.worker-helper=1", "--label", "hc.instance_id=" + id, "-i", "--network", "none", "--pid", "host", "--privileged", "--read-only", "--pids-limit", "64", "--memory", "512m", "--tmpfs", "/mnt:rw,nosuid,nodev,size=1m", "--mount", "type=bind,src=" + h.options.AssetRoot + ",dst=/assets", "--mount", "type=bind,src=/dev,dst=/host-dev", "--entrypoint", "python3", h.options.StorageImage, "-c", storageLoopProgram + "\n" + assetOperationsProgram, action, string(raw)}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	command.WaitDelay = 2 * time.Second
	command.Stdin = input
	command.Stdout = output
	diagnostic := &boundedBuffer{limit: 8192}
	command.Stderr = diagnostic
	if command.Run() != nil {
		phase := ""
		failurePattern := regexp.MustCompile(`^harness-cloud: offline asset operation failed \[([A-Z_]{1,64})\]; maintenance intent retained\s*$`)
		if action == "browse" {
			failurePattern = regexp.MustCompile(`^harness-cloud: offline asset operation failed \[([A-Z_]{1,64})\]; read-only data retained\s*$`)
		}
		if match := failurePattern.FindStringSubmatch(diagnostic.String()); len(match) == 2 {
			phase = match[1]
			if action == "browse" {
				slog.Warn("read-only storage browse failed", "phase", phase)
			} else {
				slog.Warn("offline asset operation retained maintenance intent", "phase", phase)
			}
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = dockerCommand(cleanup, nil, "rm", "-f", name)
		if action == "browse" {
			switch phase {
			case "BROWSE_FILE_UNAVAILABLE":
				return instances.ErrStorageFileUnavailable
			case "BROWSE_PREVIEW_UNAVAILABLE":
				return instances.ErrStoragePreviewUnavailable
			case "BROWSE_TOO_LARGE":
				return instances.ErrStorageFileTooLarge
			case "BROWSE_DIRECTORY_TOO_LARGE":
				return instances.ErrStorageDirectoryTooLarge
			}
			return instances.ErrStorageBrowseUnavailable
		}
		return ErrStorageUnavailable
	}
	return nil
}

// The immutable helper still owns parsing, initialization, markers and quota.
// The host adapter only makes kernel-selected loop nodes available reliably.
const storageHelperMain = `
import importlib.util,sys
spec=importlib.util.spec_from_file_location('storage','/opt/harness-storage/helper.py')
storage=importlib.util.module_from_spec(spec)
spec.loader.exec_module(storage)
patch_storage(storage)
try:
    if len(sys.argv)!=2: raise ValueError('One action required')
    data=sys.stdin.buffer.read(65537)
    if len(data)>65536: raise ValueError('Request too large')
    print(json.dumps(storage.execute(sys.argv[1],storage.parse(data))))
except Exception:
    print('harness-cloud: storage operation failed ['+storage.PHASE+']; original data retained',file=sys.stderr)
    sys.exit(1)
`
