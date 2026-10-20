package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/hostcapacity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var runtimeModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

type runtimeConfiguration struct {
	SchemaVersion    int      `json:"schema_version"`
	InstanceID       string   `json:"instance_id"`
	OrganizationID   string   `json:"org_id"`
	OwnerID          string   `json:"owner_id"`
	AssetID          string   `json:"asset_id"`
	Generation       string   `json:"runtime_generation"`
	ModelVersion     int64    `json:"configuration_version"`
	PublicURL        string   `json:"public_url"`
	Endpoint         string   `json:"endpoint"`
	Model            string   `json:"model"`
	Provider         string   `json:"provider,omitempty"`
	Protocol         string   `json:"protocol,omitempty"`
	RouteRevision    string   `json:"route_revision,omitempty"`
	ApprovalDigest   string   `json:"approval_digest,omitempty"`
	ContextWindow    int64    `json:"context_window,omitempty"`
	MaxOutputTokens  int64    `json:"max_output_tokens,omitempty"`
	ResponseModels   []string `json:"response_models,omitempty"`
	ApprovedIPs      []string `json:"approved_ips,omitempty"`
	ApprovedNetworks []string `json:"approved_networks,omitempty"`
	CAPEM            string   `json:"ca_pem,omitempty"`
}

func (h *LocalHost) runtimeConfiguration(p ExecutionPlan) (runtimeConfiguration, error) {
	if _, err := storageIdentity(p); err != nil {
		return runtimeConfiguration{}, err
	}
	if !uuidPattern.MatchString(p.RuntimeGeneration) || !h.supportsRuntimeImage(p.Snapshot.ImageDigest) || !runtimeapproval.SupportedResources(p.Snapshot.Spec.CPU, p.Snapshot.Spec.Memory, p.Snapshot.Spec.Storage) || p.Snapshot.ModelVersion < 1 || !runtimeModel.MatchString(p.Snapshot.Model) {
		return runtimeConfiguration{}, ErrHostIdentity
	}
	result := runtimeConfiguration{SchemaVersion: 1, InstanceID: p.InstanceID, OrganizationID: p.OrganizationID, OwnerID: p.OwnerID, AssetID: p.AssetID, Generation: p.RuntimeGeneration, ModelVersion: p.Snapshot.ModelVersion, PublicURL: "https://" + h.authority(p.InstanceID), Endpoint: p.Endpoint, Model: p.Snapshot.Model}
	if p.Snapshot.Provider == "deepseek-official" && p.Snapshot.Protocol == "deepseek-messages" && p.Endpoint == "https://api.deepseek.com/anthropic" && p.Snapshot.ApprovalDigest == "" {
		return result, nil
	}
	if p.Snapshot.Provider != "platform-models" || p.Snapshot.Protocol != "anthropic-messages" {
		return runtimeConfiguration{}, ErrHostIdentity
	}
	manifest, ok := h.modelPolicy.Manifest(p.Endpoint, p.Snapshot.Model)
	if !ok && p.modelManifest != nil {
		gateway, approved := h.modelPolicy.Gateway(p.Endpoint)
		if approved && p.modelManifest.ID == p.Snapshot.Model && p.modelManifest.Revision == gateway.Revision {
			compiled := gateway.Manifest(p.modelManifest.GatewayModel)
			if compiled.Digest == p.modelManifest.Digest {
				manifest, ok = compiled, true
			}
		}
	}
	if !ok || manifest.Digest != p.Snapshot.ApprovalDigest || !manifest.Streaming || !manifest.ToolUse {
		return runtimeConfiguration{}, ErrHostIdentity
	}
	gateway, _ := h.modelPolicy.Gateway(p.Endpoint)
	result.SchemaVersion = 2
	result.Provider = "platform-models"
	result.Protocol = "anthropic-messages"
	result.RouteRevision = manifest.Revision
	result.ApprovalDigest = manifest.Digest
	result.ContextWindow = manifest.ContextWindow
	result.MaxOutputTokens = manifest.MaxOutputTokens
	result.ResponseModels = append([]string{}, manifest.ResponseModels...)
	result.ApprovedNetworks = append([]string{}, gateway.Addresses...)
	result.CAPEM = gateway.CertificatePEM()
	for _, address := range gateway.Addresses {
		prefix, err := netip.ParsePrefix(address)
		if err != nil || !prefix.Addr().Is4() {
			return runtimeConfiguration{}, ErrHostIdentity
		}
		for ip := prefix.Addr(); prefix.Contains(ip); ip = ip.Next() {
			result.ApprovedIPs = append(result.ApprovedIPs, ip.String())
			if len(result.ApprovedIPs) > 256 {
				return runtimeConfiguration{}, ErrHostIdentity
			}
		}
	}
	return result, nil
}
func (h *LocalHost) host(id string) string {
	return "i-" + strings.ReplaceAll(id, "-", "") + "." + h.options.Domain
}
func publicAuthority(host string, port int) string {
	if port == 0 || port == 443 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
func (h *LocalHost) authority(id string) string {
	return publicAuthority(h.host(id), h.options.PublicPort)
}
func writeRuntimePrivate(path string, data []byte) error {
	parent := filepath.Dir(path)
	if privateDirectory(parent) != nil {
		return ErrHostIdentity
	}
	file, err := os.CreateTemp(parent, ".hc-worker-")
	if err != nil {
		return ErrHostUnavailable
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrHostUnavailable
	}
	if os.Rename(file.Name(), path) != nil {
		return ErrHostUnavailable
	}
	dir, err := os.Open(parent)
	if err != nil {
		return ErrHostUnavailable
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return ErrHostUnavailable
	}
	return nil
}
func (h *LocalHost) Configure(ctx context.Context, p ExecutionPlan) error {
	if !uuidPattern.MatchString(p.InstanceID) {
		return ErrHostIdentity
	}
	channel := filepath.Join(h.options.RuntimeRoot, p.InstanceID)
	if err := os.Mkdir(channel, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return ErrHostIdentity
	}
	if privateDirectory(channel) != nil {
		return ErrHostIdentity
	}
	desired, err := h.runtimeConfiguration(p)
	if err != nil {
		return err
	}
	data, err := json.Marshal(desired)
	if err != nil {
		return ErrHostIdentity
	}
	key := p.ModelKey()
	if len(key) == 0 || len(key) > 4096 {
		return ErrHostIdentity
	}
	for _, b := range key {
		if b < 0x21 || b > 0x7e {
			return ErrHostIdentity
		}
	}
	existing, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return err
	}
	if existing != nil {
		if err = validateContainer(*existing, p, h.options.RuntimeRoot, true); err != nil {
			return err
		}
		// Bind-mounted files keep the old inode after rename. Recover a matching
		// configuration, rather than rewriting it beneath an existing container.
		raw, err := config.ReadPrivateFile(filepath.Join(channel, "config.json"))
		if err != nil {
			return ErrHostIdentity
		}
		var previous runtimeConfiguration
		if webapi.StrictUnmarshal(raw, &previous) != nil || !reflect.DeepEqual(previous, desired) {
			return ErrHostIdentity
		}
		actual, err := config.ReadPrivateFile(filepath.Join(channel, "model-key"))
		if err != nil {
			return ErrHostIdentity
		}
		defer clear(actual)
		if !bytes.Equal(actual, key) {
			return ErrHostIdentity
		}
		return nil
	}
	if err = writeRuntimePrivate(filepath.Join(channel, "config.json"), data); err != nil {
		return err
	}
	return writeRuntimePrivate(filepath.Join(channel, "model-key"), key)
}
func (h *LocalHost) Start(ctx context.Context, p ExecutionPlan, buildID string) (Completion, error) {
	if p.WorkspaceID == nil || !uuidPattern.MatchString(buildID) {
		return Completion{}, ErrHostIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	proof, err := h.inspectStorage(ctx, p, "inspect")
	if err != nil {
		return Completion{}, err
	}
	if err = h.volume(ctx, p, proof, false); err != nil {
		return Completion{}, err
	}
	container, err := h.container(ctx, p.InstanceID)
	if err != nil {
		return Completion{}, err
	}
	if container == nil {
		return Completion{}, ErrHostIdentity
	}
	if err = validateContainer(*container, p, h.options.RuntimeRoot, true); err != nil {
		return Completion{}, err
	}
	if container.State.Running {
		if err = h.network(ctx, p.InstanceID, "inspect"); err != nil {
			return Completion{}, err
		}
	} else {
		if container.State.Status != "created" && container.State.Status != "exited" {
			return Completion{}, ErrHostIdentity
		}
		compute, computeErr := hostcapacity.Read()
		if computeErr != nil || !compute.Fits(p.Snapshot.Spec.CPU, p.Snapshot.Spec.Memory) {
			return Completion{}, ErrHostCapacity
		}
		if err = h.network(ctx, p.InstanceID, "install"); err != nil {
			return Completion{}, err
		}
		if _, err = dockerCommand(ctx, nil, "update", "--pids-limit", "256", "--cpus", strconv.FormatFloat(float64(p.Snapshot.Spec.CPU)/1000, 'f', -1, 64), "--memory", strconv.FormatInt(p.Snapshot.Spec.Memory, 10), "--memory-swap", strconv.FormatInt(p.Snapshot.Spec.Memory, 10), "hc-"+p.InstanceID); err != nil {
			return Completion{}, err
		}
		container, err = h.container(ctx, p.InstanceID)
		if err != nil || container == nil {
			return Completion{}, ErrHostUnavailable
		}
		if err = validateContainer(*container, p, h.options.RuntimeRoot, false); err != nil {
			return Completion{}, err
		}
		if _, err = dockerCommand(ctx, nil, "start", "hc-"+p.InstanceID); err != nil {
			return Completion{}, err
		}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	readinessErr := ErrNativeUnavailable
	for {
		select {
		case <-ctx.Done():
			return Completion{}, readinessErr
		case <-ticker.C:
		}
		raw, err := config.ReadPrivateFile(filepath.Join(h.options.RuntimeRoot, p.InstanceID, "native.json"))
		if err != nil {
			continue
		}
		var native nativeChannel
		if webapi.StrictUnmarshal(raw, &native) != nil || !native.matches(p) {
			continue
		}
		container, err = h.container(ctx, p.InstanceID)
		if err != nil || container == nil {
			return Completion{}, ErrHostUnavailable
		}
		if err = validateContainer(*container, p, h.options.RuntimeRoot, false); err != nil {
			return Completion{}, err
		}
		if !container.State.Running {
			return Completion{}, ErrNativeUnavailable
		}
		ip := container.NetworkSettings.Networks["hc-"+p.InstanceID].IPAddress
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil || !parsed.IsPrivate() {
			return Completion{}, ErrHostIdentity
		}
		cookie, expires, err := exchangeReadyNative(ctx, "http://"+ip+":3080", h.authority(p.InstanceID), native.Token, native.Provider, native.Model)
		native.Token = ""
		clear(raw)
		if err != nil {
			readinessErr = err
			continue
		}
		checked := time.Now().UTC()
		return Completion{AssetReady: true, FilesystemID: proof.FilesystemID, Running: &RuntimeEvidence{WorkspaceID: *p.WorkspaceID, BuildID: buildID, Host: h.host(p.InstanceID), IP: ip, Generation: p.RuntimeGeneration, NativeCookie: cookie, NativeExpiresAt: expires, CheckedAt: checked}}, nil
	}
}

type nativeChannel struct {
	SchemaVersion int    `json:"schema_version"`
	InstanceID    string `json:"instance_id"`
	Generation    string `json:"runtime_generation"`
	ModelVersion  int64  `json:"configuration_version"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspacePath string `json:"workspace_path"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Token         string `json:"token"`
}

func (n nativeChannel) matches(p ExecutionPlan) bool {
	return n.SchemaVersion == 1 && n.InstanceID == p.InstanceID && n.Generation == p.RuntimeGeneration && n.ModelVersion == p.Snapshot.ModelVersion && n.WorkspaceID != "" && n.WorkspacePath == "/data/workspace" && n.Provider == p.Snapshot.Provider && n.Model == p.Snapshot.Model
}
