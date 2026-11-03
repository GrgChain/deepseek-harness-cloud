package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type registryRoundTripper func(*http.Request) (*http.Response, error)

func (r registryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return r(request)
}

func manifestFixture() map[string]any {
	return map[string]any{
		"name": "@community/context-memory", "version": "1.2.3-beta.1+build.4", "description": "连接上下文与历史记忆。",
		"keywords": []string{"memory", "上下文"}, "author": map[string]string{"name": "社区作者"},
		"repository": map[string]string{"url": "git+https://github.com/community/context.git"},
		"engines":    map[string]string{"node": ">=24"},
		"dsh":        map[string]any{"bundle": map[string]string{"patch": "./cordis.patch.yml"}, "engines": map[string]string{"dsh": ">=0.2.1"}},
	}
}

func manifestResponse(t *testing.T, request *http.Request, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: request}
}

func registryProblem(t *testing.T, err error, status int, code string) {
	t.Helper()
	var problem *webapi.Problem
	if !errors.As(err, &problem) || problem.Status != status || problem.Code != code {
		t.Fatalf("got %v, want %d %s", err, status, code)
	}
}

func TestRegistryResolvesPinnedThirdPartyBundle(t *testing.T) {
	for _, spec := range []string{"@community/context-memory", " @community/context-memory@1.2.3-beta.1+build.4 "} {
		t.Run(spec, func(t *testing.T) {
			calls := 0
			registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != "GET" || request.URL.Scheme != "https" || request.URL.Host != "registry.npmjs.org" || request.URL.User != nil || request.URL.RawQuery != "" {
					t.Fatalf("unsafe metadata request: %s %s", request.Method, request.URL)
				}
				wantPath := "/@community/context-memory/latest"
				if strings.Contains(spec, "@1.2.3") {
					wantPath = "/@community/context-memory/1.2.3-beta.1+build.4"
				}
				if request.URL.Path != wantPath || !strings.Contains(request.URL.EscapedPath(), "%2F") {
					t.Fatalf("unexpected registry path: %s", request.URL.EscapedPath())
				}
				if deadline, ok := request.Context().Deadline(); !ok || time.Until(deadline) > 6*time.Second {
					t.Fatal("registry request is not bounded")
				}
				return manifestResponse(t, request, 200, manifestFixture()), nil
			})}}
			plugin, err := registry.Resolve(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if plugin.PackageName != "@community/context-memory" || plugin.Version != "1.2.3-beta.1+build.4" || plugin.Source != "community" || !plugin.Custom || plugin.Category != "集成扩展" {
				t.Fatalf("unexpected plugin identity: %+v", plugin)
			}
			if plugin.AdditionMethod != "online" || plugin.Filename != "" || plugin.SHA256 != "" {
				t.Fatalf("invalid registry provenance: %+v", plugin)
			}
			if plugin.Name != plugin.PackageName || plugin.Author != "社区作者" || plugin.Description != "连接上下文与历史记忆。" || plugin.Summary != plugin.Description || len(plugin.Keywords) != 2 {
				t.Fatalf("lost package metadata: %+v", plugin)
			}
			if plugin.Repository != "https://github.com/community/context" || plugin.Documentation != plugin.Repository || !strings.Contains(plugin.Compatibility, "DSH >=0.2.1") || !strings.Contains(plugin.Compatibility, "Node.js 要求 >=24") {
				t.Fatalf("lost package requirements/provenance: %+v", plugin)
			}
			if _, err := time.Parse("2006-01-02", plugin.CheckedAt); err != nil || calls != 1 {
				t.Fatalf("invalid date or unexpected registry/download calls: %s, %d", plugin.CheckedAt, calls)
			}
		})
	}
}

func TestRegistryRejectsOfficialAndNonRegistryInputsWithoutFetching(t *testing.T) {
	registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid spec made an HTTP request")
		return nil, errors.New("unexpected request")
	})}}
	for _, spec := range []string{
		"", "@deepseek-ai/dsh-experimental-session-search", "@deepseek-ai/other@1.0.0", "https://example.com/plugin.tgz", "git+https://github.com/team/plugin",
		"file:../plugin", "npm:plugin@1.0.0", "../plugin", "@community/../plugin", "@community%2fplugin", "@community/plugin?secret=x", "Plugin", "@Team/plugin",
		"pkg@^1.2.3", "pkg@1", "pkg@1.2", "pkg@1.2.3 || 2.0.0", "pkg@1.2.3/../../x", "pkg@", "pkg@01.2.3", "pkg@1.2.3-01", strings.Repeat("a", 215),
	} {
		t.Run(spec, func(t *testing.T) {
			_, err := registry.Resolve(context.Background(), spec)
			registryProblem(t, err, 422, "PLUGIN_INVALID")
		})
	}
}

func TestRegistryRejectsMissingBundleAndInvalidRegistryResponses(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]any)
		status int
		code   string
	}{
		{"ordinary npm package", func(m map[string]any) { delete(m, "dsh") }, 422, "PLUGIN_INVALID"},
		{"empty bundle", func(m map[string]any) { m["dsh"] = map[string]any{"bundle": map[string]string{}} }, 422, "PLUGIN_INVALID"},
		{"remote patch", func(m map[string]any) {
			m["dsh"] = map[string]any{"bundle": map[string]string{"patch": "https://example.com/x.yml"}}
		}, 422, "PLUGIN_INVALID"},
		{"parent patch", func(m map[string]any) { m["dsh"] = map[string]any{"bundle": map[string]string{"patch": "../x.yml"}} }, 422, "PLUGIN_INVALID"},
		{"directory patch", func(m map[string]any) { m["dsh"] = map[string]any{"bundle": map[string]string{"patch": "."}} }, 422, "PLUGIN_INVALID"},
		{"different package", func(m map[string]any) { m["name"] = "@deepseek-ai/official" }, 503, "DEPENDENCY_UNAVAILABLE"},
		{"different pinned version", func(m map[string]any) { m["version"] = "2.0.0" }, 503, "DEPENDENCY_UNAVAILABLE"},
		{"version range in manifest", func(m map[string]any) { m["version"] = "^1.2.3" }, 503, "DEPENDENCY_UNAVAILABLE"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				manifest := manifestFixture()
				test.change(manifest)
				return manifestResponse(t, request, 200, manifest), nil
			})}}
			_, err := registry.Resolve(context.Background(), "@community/context-memory@1.2.3-beta.1+build.4")
			registryProblem(t, err, test.status, test.code)
		})
	}
	for _, status := range []int{404, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
				return manifestResponse(t, request, status, map[string]string{"error": "not returned to the user"}), nil
			})}}
			_, err := registry.Resolve(context.Background(), "@community/context-memory")
			if status == 404 {
				registryProblem(t, err, 404, "PLUGIN_NOT_FOUND")
			} else {
				registryProblem(t, err, 503, "DEPENDENCY_UNAVAILABLE")
			}
		})
	}
	for _, body := range []string{"not json", strings.Repeat(" ", registryLimit+1)} {
		registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}}
		_, err := registry.Resolve(context.Background(), "@community/context-memory")
		registryProblem(t, err, 503, "DEPENDENCY_UNAVAILABLE")
	}
}

func TestRegistryNeverFollowsRedirectsAndHandlesTransportFailure(t *testing.T) {
	calls := 0
	registry := Registry{Client: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			t.Fatal("supplied redirect policy must not be used")
			return nil
		},
		Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}),
	}}
	_, err := registry.Resolve(context.Background(), "@community/context-memory")
	registryProblem(t, err, 503, "DEPENDENCY_UNAVAILABLE")
	if calls != 1 {
		t.Fatalf("followed metadata redirect: %d requests", calls)
	}
	registry.Client.Transport = registryRoundTripper(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	_, err = registry.Resolve(context.Background(), "@community/context-memory")
	registryProblem(t, err, 503, "DEPENDENCY_UNAVAILABLE")
}

func TestRegistryHandlesOptionalPublicMetadataWithoutInventingSources(t *testing.T) {
	for _, author := range []any{"社区作者", map[string]string{"name": "社区作者"}, nil} {
		registry := Registry{Client: &http.Client{Transport: registryRoundTripper(func(request *http.Request) (*http.Response, error) {
			manifest := manifestFixture()
			manifest["author"] = author
			manifest["maintainers"] = []map[string]string{{"name": "社区作者"}}
			manifest["repository"] = "javascript:alert(1)"
			manifest["homepage"] = "https://user:password@example.com/docs"
			manifest["dsh"] = map[string]any{"bundle": map[string]string{"patch": "./cordis.patch.yml"}}
			return manifestResponse(t, request, 200, manifest), nil
		})}}
		plugin, err := registry.Resolve(context.Background(), "@community/context-memory")
		if err != nil {
			t.Fatal(err)
		}
		if plugin.Author != "社区作者" || plugin.Repository != "" || plugin.Documentation != "https://www.npmjs.com/package/@community/context-memory/v/1.2.3-beta.1+build.4" || !strings.Contains(plugin.Compatibility, "未声明 DSH 版本范围") {
			t.Fatalf("invalid optional source metadata: %+v", plugin)
		}
	}
}
