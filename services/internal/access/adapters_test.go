package access

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/instances"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	workspaceengine "github.com/jonluo94/deepseek-harness-cloud/services/internal/workspace"
)

type workspaceFacts struct {
	user       workspaceengine.User
	workspace  workspaceengine.Workspace
	parameters []workspaceengine.Parameter
	fail       bool
}

func (f *workspaceFacts) User(ctx context.Context, id string) (workspaceengine.User, error) {
	if f.fail {
		return workspaceengine.User{}, workspaceengine.ErrUnavailable
	}
	return f.user, nil
}
func (f *workspaceFacts) Workspace(ctx context.Context, id string) (workspaceengine.Workspace, error) {
	if f.fail {
		return workspaceengine.Workspace{}, workspaceengine.ErrUnavailable
	}
	return f.workspace, nil
}
func (f *workspaceFacts) BuildParameters(ctx context.Context, id string) ([]workspaceengine.Parameter, error) {
	return f.parameters, nil
}

func TestActualWorkspaceFactsMustMatchFrozenRunningGeneration(t *testing.T) {
	org, control := webapi.ID(), webapi.ID()
	target := Target{OrganizationID: webapi.ID(), InstanceID: webapi.ID(), WorkspaceOwner: webapi.ID(), WorkspaceID: webapi.ID(), WorkspaceName: "hc-owned", AssetID: webapi.ID(), BuildID: webapi.ID(), Snapshot: instances.Snapshot{TemplateID: webapi.ID(), TemplateVersion: webapi.ID()}}
	good := func() *workspaceFacts {
		return &workspaceFacts{user: workspaceengine.User{ID: target.WorkspaceOwner, Status: "dormant", OrganizationIDs: []string{org}, Roles: []workspaceengine.Role{{Name: "member"}}}, workspace: workspaceengine.Workspace{ID: target.WorkspaceID, Name: target.WorkspaceName, OwnerID: target.WorkspaceOwner, OrganizationID: org, TemplateID: target.Snapshot.TemplateID, AutomaticUpdates: "never", LatestBuild: workspaceengine.Build{ID: target.BuildID, WorkspaceID: target.WorkspaceID, OwnerID: target.WorkspaceOwner, InitiatorID: control, TemplateVersionID: target.Snapshot.TemplateVersion, BuildNumber: 1, Transition: "start", Job: workspaceengine.Job{Status: "succeeded"}}}, parameters: []workspaceengine.Parameter{{Name: "instance_id", Value: target.InstanceID}, {Name: "asset_id", Value: target.AssetID}}}
	}
	if err := NewWorkspaceOwnership(good(), org, control).Check(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*workspaceFacts){
		"disabled": func(f *workspaceFacts) { f.user.Status = "suspended" }, "foreign-owner": func(f *workspaceFacts) { f.workspace.OwnerID = webapi.ID() }, "privileged-owner": func(f *workspaceFacts) { f.user.Roles = []workspaceengine.Role{{Name: "owner"}} }, "foreign-org": func(f *workspaceFacts) { f.user.OrganizationIDs = []string{webapi.ID()} }, "foreign-workspace": func(f *workspaceFacts) { f.workspace.ID = webapi.ID() }, "new-build": func(f *workspaceFacts) { f.workspace.LatestBuild.ID = webapi.ID() }, "foreign-initiator": func(f *workspaceFacts) { f.workspace.LatestBuild.InitiatorID = webapi.ID() }, "wrong-template": func(f *workspaceFacts) { f.workspace.LatestBuild.TemplateVersionID = webapi.ID() }, "stopped": func(f *workspaceFacts) { f.workspace.LatestBuild.Transition = "stop" }, "failed": func(f *workspaceFacts) { f.workspace.LatestBuild.Job.Status = "failed" }, "new-asset": func(f *workspaceFacts) { f.parameters[1].Value = webapi.ID() }, "duplicate-parameter": func(f *workspaceFacts) { f.parameters[1] = f.parameters[0] }, "automatic-update": func(f *workspaceFacts) { f.workspace.AutomaticUpdates = "always" }, "ttl": func(f *workspaceFacts) { n := int64(1000); f.workspace.TTLMillis = &n },
	} {
		t.Run(name, func(t *testing.T) {
			f := good()
			change(f)
			if code(NewWorkspaceOwnership(f, org, control).Check(context.Background(), target)) != "FORBIDDEN" {
				t.Fatal("foreign or changed Workspace authority accepted")
			}
		})
	}
	f := good()
	f.fail = true
	if code(NewWorkspaceOwnership(f, org, control).Check(context.Background(), target)) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal("unavailable Workspace did not fail closed")
	}
}

func TestNativeVerificationHasFixedDialAndNeverFollowsRedirects(t *testing.T) {
	rules, _ := NewRules("https://portal.harness.test:42013", "harness.test")
	host := rules.Host(webapi.ID())
	cookie := []byte("dsh-auth-" + base64Host(rules.authority(host)) + "=v1.synthetic.signature")
	for _, mode := range []string{"valid", "redirect", "anonymous-open", "expired-cookie", "set-cookie", "slow"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Host != rules.authority(host) || r.URL.Path != "/" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
					t.Error("native verification altered fixed authority")
				}
				if mode == "slow" {
					<-r.Context().Done()
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "https://foreign.test", 303)
					return
				}
				if mode == "set-cookie" {
					w.Header().Set("Set-Cookie", "leak=secret")
				}
				if r.Header.Get("Cookie") == "" {
					if mode == "anonymous-open" {
						w.WriteHeader(200)
					} else {
						w.WriteHeader(401)
					}
					return
				}
				if r.Header.Get("Cookie") != string(cookie) || mode == "expired-cookie" {
					w.WriteHeader(401)
					return
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			verifier := NewNativeVerifier(rules)
			// Production still constructs http://<verified private IP>:3080. The
			// test transport redirects only the socket to this in-process server.
			verifier.transport.DialContext = testDial(server)
			defer verifier.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := verifier.Verify(ctx, Authorization{Target: Target{Host: host, IP: "172.18.0.5"}, Cookie: cookie})
			if (err == nil) != (mode == "valid") {
				t.Fatal("native authorization matrix was not enforced")
			}
			if calls.Load() > 2 {
				t.Fatal("redirect or unbounded native request followed")
			}
		})
	}
}

func testDial(server *httptest.Server) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
}
