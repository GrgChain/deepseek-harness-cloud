package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelKeyOverridesFailClosedAndRemainRevocable(t *testing.T) {
	for _, kind := range []string{"approved", "wildcard-model", "extra-route", "blocked", "expired", "future-expiry", "alias-override", "config-override", "extra-permission", "team-inheritance"} {
		t.Run(kind, func(t *testing.T) {
			info := map[string]any{"models": []string{"private-route"}, "allowed_routes": []string{"/v1/messages", "/v1/models"}}
			switch kind {
			case "wildcard-model":
				info["models"] = []string{"*"}
			case "extra-route":
				info["allowed_routes"] = []string{"/v1/messages", "/v1/models", "/model/new"}
			case "blocked":
				info["blocked"] = true
			case "expired":
				info["expires"] = "2000-01-01T00:00:00Z"
			case "future-expiry":
				info["expires"] = "2099-01-01T00:00:00Z"
			case "alias-override":
				info["aliases"] = map[string]any{"private-route": "other-model"}
			case "config-override":
				info["config"] = map[string]any{"fallbacks": []string{"other-model"}}
			case "extra-permission":
				info["permissions"] = map[string]any{"model_management": true}
			case "team-inheritance":
				info["team_id"] = "other-team"
			}
			deleted := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/key/info":
					if deleted {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"info": info})
				case "/key/delete":
					deleted = true
				case "/v2/model/info":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
				default:
					t.Error("unexpected mutation", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			control := modelControl{Gateway{Endpoint: server.URL}, server.Client(), "synthetic-control-key"}
			found, err := control.keyInfo(context.Background(), "synthetic-virtual-key", "private-route")
			if (kind == "approved") != (found && err == nil) {
				t.Fatal("key scope verification disagrees")
			}
			if err := control.revoke(context.Background(), "stable-route", "synthetic-virtual-key", "private-route"); err != nil || !deleted {
				t.Fatal("unsafe key could not be revoked", err)
			}
		})
	}
}
