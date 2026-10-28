package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/hostcapacity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/retention"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

// Service wires the trusted host boundary to the durable queue. Readiness
// includes the embedded workspace authority and immutable local helper images.
type Service struct {
	runner                    *Runner
	monitor                   *Monitor
	models                    *settings.AutoValidator
	modelSync                 *settings.ModelSync
	cleaner                   *retention.Cleaner
	usage                     *StorageUsageCollector
	releaser                  *StorageReleaser
	browser                   *StorageBrowser
	extensions                *ExtensionBroker
	client                    *workspaceengine.Client
	provisioner               *workspaceengine.Terraform
	host                      *LocalHost
	organization, controlUser string
}

func NewService(ctx context.Context, pool *pgxpool.Pool, path string) (*Service, error) {
	o, err := LoadOptions(path)
	if err != nil {
		return nil, err
	}
	cipher, err := security.LoadSecretCipher(o.DataKeyID, o.DataKeyFile, o.DataKeyringFile)
	if err != nil {
		return nil, ErrWorkerConfiguration
	}
	policyRaw, err := config.ReadPrivateFile(o.ModelAllowlistFile)
	if err != nil {
		return nil, ErrWorkerConfiguration
	}
	policy, err := settings.ParsePolicy(policyRaw)
	if err != nil {
		return nil, ErrWorkerConfiguration
	}
	client, err := workspaceengine.NewEmbedded(pool, o.WorkspaceOrganizationID, o.ControlUserID)
	if err != nil {
		return nil, ErrWorkerConfiguration
	}
	provisioner, err := workspaceengine.NewTerraform(o.Terraform)
	if err != nil {
		return nil, ErrWorkerConfiguration
	}
	host, err := NewLocalHost(o.Host)
	if err != nil {
		return nil, err
	}
	host.WithModelPolicy(policy)
	maintenance := os.Getenv("HC_MAINTENANCE")
	if maintenance != "" && maintenance != "true" && maintenance != "false" {
		return nil, errors.New("HC_MAINTENANCE must be true or false")
	}
	leases := NewLeaseStore(pool).WithMaintenance(maintenance == "true")
	executor, err := NewExecutor(NewExecutionStore(pool, cipher, o.Environment), leases, client, host, o.WorkspaceOrganizationID, o.ControlUserID)
	if err != nil {
		return nil, err
	}
	service := &Service{runner: NewRunner(leases, executor), monitor: NewMonitor(NewHealthStore(pool, cipher, o.Environment, o.Host.PublicPort), NewRuntimeHealthChecker(executor), NewSafetyStore(pool)), client: client, host: host, organization: o.WorkspaceOrganizationID, controlUser: o.ControlUserID}
	service.provisioner = provisioner
	service.cleaner = retention.NewCleaner(pool)
	service.usage = NewStorageUsageCollector(pool, host)
	service.releaser = NewStorageReleaser(pool, host)
	service.browser = NewStorageBrowser(pool, host)
	service.extensions = NewExtensionBroker(pool, o.Host.RuntimeRoot, cipher, o.Environment, maintenance == "true")
	service.releaser.maintenance = maintenance == "true"
	service.models = settings.NewAutoValidator(settings.NewService(pool, cipher, policy, o.Environment, maintenance == "true"))
	if o.ModelControlKeyFile != "" {
		key, e := config.ReadPrivateFile(o.ModelControlKeyFile)
		if e != nil || len(key) < 16 || len(key) > 4096 {
			return nil, ErrWorkerConfiguration
		}
		for _, b := range key {
			if b < 0x21 || b > 0x7e {
				clear(key)
				return nil, ErrWorkerConfiguration
			}
		}
		service.modelSync = settings.NewModelSync(settings.NewService(pool, cipher, policy, o.Environment, maintenance == "true"), key)
		clear(key)
	}
	if err = service.Ready(ctx); err != nil {
		return nil, errors.New("Worker workspace or trusted host verification failed")
	}
	return service, nil
}
func (s *Service) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	count := 9
	if s.modelSync != nil {
		count += 2
	}
	results := make(chan error, count)
	if s.modelSync != nil {
		go func() { results <- s.modelSync.Run(ctx) }()
		go func() { results <- s.modelSync.RunChecks(ctx) }()
	}
	go func() { results <- s.runner.Run(ctx) }()
	go func() { results <- s.monitor.Run(ctx) }()
	go func() { results <- s.models.Run(ctx) }()
	go func() { results <- s.cleaner.Run(ctx) }()
	go func() { results <- s.usage.Run(ctx) }()
	go func() { results <- s.releaser.Run(ctx) }()
	go func() { results <- s.browser.Run(ctx) }()
	go func() { results <- s.extensions.Run(ctx) }()
	go func() { results <- s.client.RunBuilds(ctx, s.provisioner) }()
	var runErr error
	for range count {
		err := <-results
		cancel()
		if runErr == nil {
			runErr = err
		}
	}
	return runErr
}
func (s *Service) Ready(ctx context.Context) error {
	if err := s.workspaceReady(ctx); err != nil {
		return err
	}
	if err := s.provisioner.Check(ctx); err != nil {
		return ErrHostUnavailable
	}
	return s.host.Probe(ctx)
}
func (s *Service) workspaceReady(ctx context.Context) error {
	info, err := s.client.BuildInfo(ctx)
	if err != nil || info.Version != workspaceengine.Version || info.Telemetry || info.Engine != workspaceengine.EmbeddedEngine {
		return ErrHostUnavailable
	}
	user, err := s.client.User(ctx, "me")
	if err != nil || user.ID != s.controlUser || !workspaceengine.OwnerStatusUsable(user.Status) || len(user.OrganizationIDs) != 1 || user.OrganizationIDs[0] != s.organization {
		return ErrHostIdentity
	}
	owner := false
	for _, role := range user.Roles {
		owner = owner || role.Name == "owner"
	}
	if !owner {
		return ErrHostIdentity
	}
	return nil
}

type AdmissionFacts struct {
	SchemaVersion         int      `json:"schema_version"`
	Domain                string   `json:"domain"`
	PublicPort            int      `json:"public_port"`
	RuntimeImage          string   `json:"runtime_image"`
	PreviousRuntimeImages []string `json:"previous_runtime_images,omitempty"`
	StorageImage          string   `json:"storage_image"`
	NetworkImage          string   `json:"network_image"`
	Workspace             bool     `json:"workspace"`
	Docker                bool     `json:"docker"`
	Storage               bool     `json:"storage"`
	Capacity              bool     `json:"capacity"`
	HostCPUCount          int      `json:"host_cpu_count"`
	HostAvailableMemory   int64    `json:"host_available_memory_bytes"`
}

func (s *Service) Admission(ctx context.Context) AdmissionFacts {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	port := s.host.options.PublicPort
	if port == 0 {
		port = 443
	}
	compute, computeErr := hostcapacity.Read()
	// Report whether the smallest supported instance fits. The API checks the
	// selected specification against these facts; fixed build slots use 2 CPU/4 GiB.
	return AdmissionFacts{SchemaVersion: 1, Domain: s.host.options.Domain, PublicPort: port, RuntimeImage: s.host.options.RuntimeImage, PreviousRuntimeImages: append([]string(nil), s.host.options.PreviousRuntimeImages...), StorageImage: s.host.options.StorageImage, NetworkImage: s.host.options.NetworkImage, Workspace: s.workspaceReady(ctx) == nil, Docker: s.host.Probe(ctx) == nil, Storage: s.host.Capacity() == nil, Capacity: computeErr == nil && compute.Fits(1000, 2<<30), HostCPUCount: compute.CPUs, HostAvailableMemory: compute.AvailableMemory}
}
func (h *LocalHost) Probe(ctx context.Context) error {
	ids := []string{h.options.RuntimeImage, h.options.StorageImage, h.options.NetworkImage}
	ids = append(ids, h.options.PreviousRuntimeImages...)
	raw, err := dockerCommand(ctx, nil, append([]string{"image", "inspect"}, ids...)...)
	if err != nil {
		return ErrHostUnavailable
	}
	var images []struct {
		ID           string `json:"Id"`
		OS           string `json:"Os"`
		Architecture string `json:"Architecture"`
	}
	if json.Unmarshal(raw, &images) != nil || len(images) != len(ids) {
		return ErrHostIdentity
	}
	for i, image := range images {
		if image.ID != ids[i] || image.OS != "linux" || image.Architecture != "amd64" {
			return ErrHostIdentity
		}
	}
	return nil
}
