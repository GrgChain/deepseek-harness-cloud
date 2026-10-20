package access

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type WorkspaceReader interface {
	User(context.Context, string) (workspaceengine.User, error)
	Workspace(context.Context, string) (workspaceengine.Workspace, error)
	BuildParameters(context.Context, string) ([]workspaceengine.Parameter, error)
}
type WorkspaceOwnership struct {
	client                WorkspaceReader
	organization, control string
}

func NewWorkspaceOwnership(client WorkspaceReader, organization, control string) *WorkspaceOwnership {
	return &WorkspaceOwnership{client, organization, control}
}
func workspaceFailure(err error) error {
	if errors.Is(err, workspaceengine.ErrNotFound) {
		return denied()
	}
	return unavailable()
}
func (o *WorkspaceOwnership) Check(ctx context.Context, t Target) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if o.client == nil {
		return denied()
	}
	if err := o.CheckOwner(ctx, t.WorkspaceOwner); err != nil {
		return err
	}
	w, err := o.client.Workspace(ctx, t.WorkspaceID)
	if err != nil {
		return workspaceFailure(err)
	}
	b := w.LatestBuild
	if w.ID != t.WorkspaceID || w.Name != t.WorkspaceName || w.OwnerID != t.WorkspaceOwner || w.OrganizationID != o.organization || w.TemplateID != t.Snapshot.TemplateID || w.AutomaticUpdates != "never" || (w.TTLMillis != nil && *w.TTLMillis != 0) || b.ID != t.BuildID || b.WorkspaceID != w.ID || b.OwnerID != t.WorkspaceOwner || b.InitiatorID != o.control || b.TemplateVersionID != t.Snapshot.TemplateVersion || b.BuildNumber < 1 || b.Transition != "start" || b.Job.Status != "succeeded" {
		return denied()
	}
	parameters, err := o.client.BuildParameters(ctx, b.ID)
	if err != nil {
		return workspaceFailure(err)
	}
	if len(parameters) != 2 {
		return denied()
	}
	seen := map[string]string{}
	for _, p := range parameters {
		if _, ok := seen[p.Name]; ok {
			return denied()
		}
		seen[p.Name] = p.Value
	}
	if seen["instance_id"] != t.InstanceID || seen["asset_id"] != t.AssetID {
		return denied()
	}
	return nil
}
func (o *WorkspaceOwnership) CheckOwner(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if o.client == nil || !uuid.MatchString(id) {
		return denied()
	}
	user, err := o.client.User(ctx, id)
	if err != nil {
		return workspaceFailure(err)
	}
	if user.ID != id || !workspaceengine.OwnerStatusUsable(user.Status) || len(user.OrganizationIDs) != 1 || user.OrganizationIDs[0] != o.organization {
		return denied()
	}
	for _, role := range user.Roles {
		if role.Name != "member" {
			return denied()
		}
	}
	return nil
}

type HTTPNativeVerifier struct {
	rules     Rules
	transport *http.Transport
}

func fixedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = (&net.Dialer{Timeout: time.Second, KeepAlive: 30 * time.Second}).DialContext
	t.ResponseHeaderTimeout = 2 * time.Second
	t.MaxResponseHeaderBytes = 32 << 10
	t.MaxIdleConns = 40
	t.MaxIdleConnsPerHost = 4
	return t
}
func NewNativeVerifier(rules Rules) *HTTPNativeVerifier {
	return &HTTPNativeVerifier{rules, fixedTransport()}
}
func (v *HTTPNativeVerifier) Close() { v.transport.CloseIdleConnections() }
func (v *HTTPNativeVerifier) Verify(ctx context.Context, a Authorization) error {
	ip := net.ParseIP(a.Target.IP)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() || !validNativeHeader(v.rules.authority(a.Target.Host), a.Cookie) {
		return denied()
	}
	if _, ok := v.rules.Resolve(v.rules.authority(a.Target.Host)); !ok {
		return denied()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Transport: v.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, auth := range []bool{false, true} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(a.Target.IP, "3080")+"/", nil)
		if err != nil {
			return unavailable()
		}
		req.Host = v.rules.authority(a.Target.Host)
		if auth {
			req.Header.Set("Cookie", string(a.Cookie))
		}
		response, err := client.Do(req)
		if err != nil {
			return unavailable()
		}
		response.Body.Close()
		expected := 401
		if auth {
			expected = 200
		}
		if response.StatusCode != expected || len(response.Header.Values("Set-Cookie")) != 0 {
			return unavailable()
		}
	}
	return nil
}
