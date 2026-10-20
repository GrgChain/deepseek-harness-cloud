package operations

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/worker"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type InventoryObserver interface {
	Observe(context.Context) ([]ResourceObservation, error)
}
type LocalInventoryObserver struct {
	Host                               *worker.LocalHost
	Workspace                          *workspaceengine.Client
	WorkspaceOrganization, ControlUser string
}

func (o *LocalInventoryObserver) Observe(ctx context.Context) ([]ResourceObservation, error) {
	if o.Host == nil || o.Workspace == nil || !uuid.MatchString(o.WorkspaceOrganization) || !uuid.MatchString(o.ControlUser) {
		return nil, ErrInput
	}
	info, err := o.Workspace.BuildInfo(ctx)
	if err != nil || info.Version != workspaceengine.Version || info.Telemetry {
		return nil, fmt.Errorf("%w: workspace source version or telemetry", ErrInventory)
	}
	me, err := o.Workspace.User(ctx, "me")
	fullVisibility := false
	for _, r := range me.Roles {
		if r.Name == "owner" {
			fullVisibility = true
		}
	}
	if err != nil || me.ID != o.ControlUser || !workspaceengine.OwnerStatusUsable(me.Status) || !fullVisibility || len(me.OrganizationIDs) != 1 || me.OrganizationIDs[0] != o.WorkspaceOrganization {
		return nil, fmt.Errorf("%w: workspace control identity", ErrInventory)
	}
	workspaces, err := o.Workspace.WorkspaceInventory(ctx, o.WorkspaceOrganization)
	if err != nil {
		return nil, fmt.Errorf("%w: workspace census", ErrInventory)
	}
	resources, err := o.Host.Inventory(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: read-only Docker and asset census", ErrInventory)
	}
	for _, w := range workspaces {
		id := instanceFromWorkspaceName(w.Name)
		r := ResourceObservation{Kind: "WORKSPACE", ExternalID: w.ID, InstanceID: id, WorkspaceOwner: w.OwnerID, TemplateVersion: w.LatestBuild.TemplateVersionID, Status: w.LatestBuild.Status}
		r.IdentityValid = id != "" && uuid.MatchString(w.OwnerID) && uuid.MatchString(w.LatestBuild.TemplateVersionID) && w.LatestBuild.WorkspaceID == w.ID && w.AutomaticUpdates == "never" && (w.TTLMillis == nil || *w.TTLMillis == 0)
		if !keyName.MatchString(r.Status) {
			r.Status = "unknown"
			r.IdentityValid = false
		}
		for _, p := range []*string{&r.WorkspaceOwner, &r.TemplateVersion} {
			if !uuid.MatchString(*p) {
				*p = ""
				r.IdentityValid = false
			}
		}
		resources = append(resources, r)
	}
	return resources, nil
}
func instanceFromWorkspaceName(name string) string {
	if !strings.HasPrefix(name, "hc-") {
		return ""
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimPrefix(name, "hc-")))
	if err != nil || len(raw) != 16 {
		return ""
	}
	h := hex.EncodeToString(raw)
	id := h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	expected, err := workspaceengine.WorkspaceName(id)
	if err != nil || expected != name {
		return ""
	}
	return id
}
func inventoryHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
