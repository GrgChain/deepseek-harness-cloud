package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelRouteRequiresMatchingReadback(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000051"
	const alias = "hc-private-v1"
	frozen := personalManifest{Catalog: ModelCatalogEntry{Model: "kimi-k2.5", APIBase: "https://api.moonshot.ai/v1"}, Prefix: "moonshot", Parameters: json.RawMessage(`{"extra_body":{"thinking":{"type":"disabled"}}}`)}
	cases := []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"approved route", func(map[string]any) {}, true},
		{"upstream model changed", func(p map[string]any) { p["model"] = "moonshot/other" }, false},
		{"API region changed", func(p map[string]any) { p["api_base"] = "https://api.moonshot.cn/v1" }, false},
		{"API path changed", func(p map[string]any) { p["api_base"] = "https://api.moonshot.ai/other" }, false},
		{"API base missing", func(p map[string]any) { delete(p, "api_base") }, false},
		{"SDK retries enabled", func(p map[string]any) { p["max_retries"] = 2 }, false},
		{"SDK retries missing", func(p map[string]any) { delete(p, "max_retries") }, false},
		{"SDK retry type changed", func(p map[string]any) { p["max_retries"] = "0" }, false},
		{"router retries enabled", func(p map[string]any) { p["num_retries"] = 2 }, false},
		{"thinking enabled", func(p map[string]any) {
			p["extra_body"] = map[string]any{"thinking": map[string]any{"type": "enabled"}}
		}, false},
		{"thinking setting missing", func(p map[string]any) { delete(p, "extra_body") }, false},
		{"extra provider fields", func(p map[string]any) { p["extra_body"].(map[string]any)["other"] = true }, false},
		{"unexpected headers", func(p map[string]any) { p["headers"] = map[string]any{"X-Override": "unapproved"} }, false},
		{"proxy mode changed", func(p map[string]any) { p["use_unapproved_proxy"] = true }, false},
	}
	for _, existing := range []bool{true, false} {
		mode := "new route"
		if existing {
			mode = "existing route"
		}
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				params := map[string]any{"model": "moonshot/kimi-k2.5", "api_base": frozen.Catalog.APIBase, "max_retries": 0, "extra_body": map[string]any{"thinking": map[string]any{"type": "disabled"}}}
				found, reads, writes := existing, 0, 0
				if existing {
					tc.change(params)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v2/model/info":
						reads++
						data := []any{}
						if found {
							data = append(data, map[string]any{"model_name": alias, "model_info": map[string]any{"id": id}, "provider_params": params})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
					case "/model/new":
						writes++
						found = true
						var request struct {
							Params map[string]any `json:"provider_params"`
						}
						if json.NewDecoder(r.Body).Decode(&request) != nil {
							t.Error("invalid route request")
						}
						params = request.Params
						delete(params, "api_key") // Real 平台模型网关 never returns this field.
						tc.change(params)
						_, _ = w.Write([]byte(`{}`))
					default:
						t.Error("unexpected route operation")
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				defer server.Close()
				control := modelControl{gateway: Gateway{Endpoint: server.URL}, client: server.Client(), key: "synthetic-control-key"}
				err := control.ensureRoute(context.Background(), id, alias, frozen, "synthetic-provider-key")
				if (err == nil) != tc.valid {
					t.Fatalf("route acceptance differs from frozen configuration: accepted=%v", err == nil)
				}
				if existing && writes != 0 || !existing && (writes != 1 || reads != 2) {
					t.Fatal("route recovery rewrote an existing route or skipped registration readback")
				}
			})
		}
	}
}
