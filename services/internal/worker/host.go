package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrHostIdentity       = errors.New("original host resource identity or isolation is invalid")
	ErrHostUnavailable    = errors.New("trusted host executor is unavailable")
	ErrStorageUnavailable = errors.New("original storage asset is unavailable")
	ErrHostCapacity       = errors.New("host resource capacity is insufficient")
)
var imageIDPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var controlledPath = regexp.MustCompile(`^/[A-Za-z0-9/_.-]+$`)

type HostOptions struct {
	RuntimeRoot, AssetRoot, RuntimeImage, StorageImage, NetworkImage, Domain string
	StorageBudget                                                            int64
	PublicPort                                                               int
	PreviousRuntimeImages                                                    []string `json:",omitempty"`
}
type LocalHost struct {
	options     HostOptions
	modelPolicy *settings.Policy
}

func (h *LocalHost) WithModelPolicy(policy *settings.Policy) *LocalHost {
	h.modelPolicy = policy
	return h
}

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return ErrHostIdentity
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return ErrHostIdentity
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != filepath.Clean(path) {
		return ErrHostIdentity
	}
	return nil
}
func NewLocalHost(o HostOptions) (*LocalHost, error) {
	for _, path := range []string{o.RuntimeRoot, o.AssetRoot} {
		if !controlledPath.MatchString(path) || filepath.Clean(path) != path || privateDirectory(path) != nil {
			return nil, ErrHostIdentity
		}
	}
	for _, image := range []string{o.RuntimeImage, o.StorageImage, o.NetworkImage} {
		if !imageIDPattern.MatchString(image) {
			return nil, ErrHostIdentity
		}
	}
	if len(o.PreviousRuntimeImages) > 4 {
		return nil, ErrHostIdentity
	}
	seen := map[string]bool{o.RuntimeImage: true}
	for _, image := range o.PreviousRuntimeImages {
		if !imageIDPattern.MatchString(image) || seen[image] {
			return nil, ErrHostIdentity
		}
		seen[image] = true
	}
	o.PreviousRuntimeImages = append([]string(nil), o.PreviousRuntimeImages...)
	if !hostPattern.MatchString(o.Domain) || strings.Contains(o.Domain, "..") || !strings.Contains(o.Domain, ".") || strings.HasPrefix(o.Domain, ".") || strings.HasSuffix(o.Domain, ".") || o.StorageBudget < 10<<30 || o.StorageBudget > 1<<50 || o.PublicPort < 0 || o.PublicPort > 65535 {
		return nil, ErrHostIdentity
	}
	lockRoot := filepath.Join(o.RuntimeRoot, ".locks")
	if err := os.Mkdir(lockRoot, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrHostIdentity
	}
	if err := privateDirectory(lockRoot); err != nil {
		return nil, err
	}
	return &LocalHost{options: o}, nil
}

func (h *LocalHost) supportsRuntimeImage(image string) bool {
	if image == h.options.RuntimeImage {
		return true
	}
	for _, previous := range h.options.PreviousRuntimeImages {
		if image == previous {
			return true
		}
	}
	return false
}
func (h *LocalHost) Acquire(ctx context.Context, id string) (func(), error) {
	if !uuidPattern.MatchString(id) {
		return nil, ErrHostIdentity
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(h.options.RuntimeRoot, ".locks", id)
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrHostIdentity
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, ErrHostIdentity
	}
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			var once sync.Once
			return func() { once.Do(func() { syscall.Flock(fd, syscall.LOCK_UN); file.Close() }) }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			file.Close()
			return nil, ErrHostUnavailable
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func (h *LocalHost) Capacity() error {
	var fs syscall.Statfs_t
	if syscall.Statfs(h.options.AssetRoot, &fs) != nil || fs.Bsize <= 0 || fs.Blocks == 0 {
		return ErrHostCapacity
	}
	if uint64(fs.Bavail) > math.MaxUint64/uint64(fs.Bsize) {
		return ErrHostCapacity
	}
	free := uint64(fs.Bavail) * uint64(fs.Bsize)
	required := uint64(h.options.StorageBudget) + uint64(h.options.StorageBudget)/5
	if free < required || fs.Bavail < fs.Blocks/5+(fs.Blocks%5+4)/5 {
		return ErrHostCapacity
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrHostUnavailable
	}
	return b.Buffer.Write(p)
}
func dockerCommand(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	command.Stdin = bytes.NewReader(input)
	command.WaitDelay = 2 * time.Second
	out := &boundedBuffer{limit: 1 << 20}
	command.Stdout = out
	diagnostic := &boundedBuffer{limit: 8192}
	command.Stderr = diagnostic
	if command.Run() != nil {
		if phase := storageFailurePhase(diagnostic.String()); phase != "" {
			slog.Warn("trusted storage operation failed; original asset retained", "phase", phase)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrHostUnavailable
	}
	return out.Bytes(), nil
}

var storagePhasePattern = regexp.MustCompile(`^harness-cloud: storage operation failed \[([A-Z0-9_]{1,64})\]; original data retained\s*$`)

func storageFailurePhase(raw string) string {
	match := storagePhasePattern.FindStringSubmatch(raw)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}
func (h *LocalHost) helper(ctx context.Context, kind, action string, input any) ([]byte, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, ErrHostIdentity
	}
	var identity struct {
		InstanceID string `json:"instance_id"`
	}
	if json.Unmarshal(data, &identity) != nil || !uuidPattern.MatchString(identity.InstanceID) {
		return nil, ErrHostIdentity
	}
	name := "hc-helper-" + randomHelperID()
	args := []string{"run", "--rm", "--name", name, "--label", "hc.worker-helper=1", "--label", "hc.instance_id=" + identity.InstanceID, "-i", "--read-only"}
	if kind == "storage" {
		args = append(args, "--network", "none", "--privileged", "--tmpfs", "/mnt:rw,nosuid,nodev,size=1m", "--mount", "type=bind,src="+h.options.AssetRoot+",dst=/assets", "--mount", "type=bind,src=/dev,dst=/host-dev", "--entrypoint", "python3", h.options.StorageImage, "-c", storageLoopProgram+"\n"+storageHelperMain, action)
	} else if kind == "network" {
		args = append(args, "--network", "host", "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", h.options.NetworkImage, action)
	} else {
		return nil, ErrHostIdentity
	}
	result, err := dockerCommand(ctx, data, args...)
	if err != nil {
		// Killing the CLI does not stop its daemon-owned helper. Stop only this
		// generated helper before releasing the local instance execution lock.
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = dockerCommand(cleanup, nil, "rm", "-f", name)
		return nil, ErrHostUnavailable
	}
	return result, nil
}
func randomHelperID() string { return strings.ReplaceAll(webapi.ID(), "-", "") }

// A daemon-owned helper can outlive its Worker process. A new lease must not
// start work while a prior helper still mutates this instance's host resources.
func (h *LocalHost) EnsureHelpersAbsent(ctx context.Context, id string) error {
	if !uuidPattern.MatchString(id) {
		return ErrHostIdentity
	}
	raw, err := dockerCommand(ctx, nil, "ps", "--filter", "label=hc.worker-helper=1", "--filter", "label=hc.instance_id="+id, "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return ErrHostUnavailable
	}
	return nil
}
