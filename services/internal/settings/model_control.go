package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
)

// Control credentials are loaded only by Worker. Errors deliberately contain
// no response bodies, URLs, provider keys or virtual credentials.
var errModelControl = errors.New("model control operation could not be verified")
var errModelRouteDrift = errors.New("model route differs from the approved configuration")

type modelControl struct {
	gateway Gateway
	client  *http.Client
	key     string
}
type remoteRoute struct {
	ID, Alias string
	Params    map[string]any
}

func (c modelControl) request(ctx context.Context, method, path string, input any, output any) (int, error) {
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
		defer clear(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.gateway.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return 0, errModelControl
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, errModelControl
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, errModelControl
	}
	if output == nil {
		return resp.StatusCode, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
	defer clear(raw)
	if err != nil || len(raw) > 256<<10 || json.Unmarshal(raw, output) != nil {
		return resp.StatusCode, errModelControl
	}
	return resp.StatusCode, nil
}
func (c modelControl) route(ctx context.Context, id string) (remoteRoute, bool, error) {
	var response struct {
		Data []struct {
			Alias  string         `json:"model_name"`
			Params map[string]any `json:"provider_params"`
			Info   struct {
				ID string `json:"id"`
			} `json:"model_info"`
		}
	}
	status, err := c.request(ctx, "GET", "/v2/model/info?size=2&modelId="+url.QueryEscape(id), nil, &response)
	if status == 404 {
		return remoteRoute{}, false, nil
	}
	if err != nil {
		return remoteRoute{}, false, err
	}
	if len(response.Data) == 0 {
		return remoteRoute{}, false, nil
	}
	if len(response.Data) != 1 || response.Data[0].Info.ID != id {
		return remoteRoute{}, false, errModelControl
	}
	item := response.Data[0]
	// The pinned proxy omits provider credentials. Never retain or compare a
	// credential field if a different server happens to return one.
	delete(item.Params, "api_key")
	return remoteRoute{id, item.Alias, item.Params}, true, nil
}

func routeParameters(m personalManifest) (map[string]any, error) {
	var params map[string]any
	if json.Unmarshal(m.Parameters, &params) != nil || params == nil {
		return nil, errModelControl
	}
	delete(params, "api_key")
	params["model"], params["api_base"], params["max_retries"] = m.Prefix+"/"+m.Catalog.Model, m.Catalog.APIBase, float64(0)
	return params, nil
}

func matchingRoute(route remoteRoute, alias string, expected map[string]any) bool {
	if route.Alias != alias {
		return false
	}
	for key, value := range expected {
		actual, found := route.Params[key]
		if !found || !reflect.DeepEqual(actual, value) {
			return false
		}
	}
	if len(route.Params) != len(expected) {
		return false
	}
	return true
}

func (c modelControl) verifyRoute(ctx context.Context, id, alias string, m personalManifest) error {
	expected, err := routeParameters(m)
	if err != nil {
		return err
	}
	current, found, err := c.route(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return errModelControl
	}
	if !matchingRoute(current, alias, expected) {
		return errModelRouteDrift
	}
	return nil
}

func (c modelControl) ensureRoute(ctx context.Context, id, alias string, m personalManifest, providerAccountKey string) error {
	params, err := routeParameters(m)
	if err != nil {
		return err
	}
	current, found, err := c.route(ctx, id)
	if err != nil {
		return err
	}
	if found {
		if !matchingRoute(current, alias, params) {
			return errModelRouteDrift
		}
		return nil
	}
	params["api_key"] = providerAccountKey
	_, err = c.request(ctx, "POST", "/model/new", map[string]any{"model_name": alias, "provider_params": params, "model_info": map[string]any{"id": id}}, nil)
	if err != nil {
		return err
	}
	return c.verifyRoute(ctx, id, alias, m)
}

type remoteKeyInfo struct {
	Models        []string       `json:"models"`
	AllowedRoutes []string       `json:"allowed_routes"`
	Status        string         `json:"status"`
	Blocked       bool           `json:"blocked"`
	Expires       *string        `json:"expires"`
	Aliases       map[string]any `json:"aliases"`
	Config        map[string]any `json:"config"`
	Permissions   map[string]any `json:"permissions"`
	TeamID        *string        `json:"team_id"`
}

func (c modelControl) readKey(ctx context.Context, key string) (remoteKeyInfo, bool, error) {
	hash := sha256.Sum256([]byte(key))
	var response struct {
		Info remoteKeyInfo `json:"info"`
	}
	status, err := c.request(ctx, "GET", "/key/info?key="+hex.EncodeToString(hash[:]), nil, &response)
	if status == 404 {
		return response.Info, false, nil
	}
	if err != nil {
		return response.Info, false, err
	}
	if response.Info.Status == "deleted" {
		return response.Info, false, nil
	}
	return response.Info, true, nil
}

func (c modelControl) keyInfo(ctx context.Context, key, alias string) (bool, error) {
	info, found, err := c.readKey(ctx, key)
	if err != nil || !found {
		return false, err
	}
	// The platform creates revocable keys without expiry, team inheritance or
	// per-key routing overrides. A later override is not an approved credential.
	if info.Blocked || info.Expires != nil || info.TeamID != nil || len(info.Aliases) != 0 || len(info.Config) != 0 || len(info.Permissions) != 0 || !slices.Equal(info.Models, []string{alias}) || !slices.Equal(info.AllowedRoutes, []string{"/v1/messages", "/v1/models"}) {
		return false, errModelControl
	}
	return true, nil
}
func (c modelControl) ensureKey(ctx context.Context, key, alias string) error {
	found, err := c.keyInfo(ctx, key, alias)
	if err != nil || found {
		return err
	}
	_, err = c.request(ctx, "POST", "/key/generate", map[string]any{"key": key, "models": []string{alias}, "allowed_routes": []string{"/v1/messages", "/v1/models"}, "key_type": "default", "send_invite_email": false, "rpm_limit": 60}, nil)
	return err
}
func (c modelControl) revoke(ctx context.Context, id, key, _ string) error {
	if key != "" {
		_, found, err := c.readKey(ctx, key)
		if err != nil {
			return err
		}
		if found {
			if _, err = c.request(ctx, "POST", "/key/delete", map[string]any{"keys": []string{key}}, nil); err != nil {
				return err
			}
		}
		_, found, err = c.readKey(ctx, key)
		if err != nil || found {
			return errModelControl
		}
	}
	_, found, err := c.route(ctx, id)
	if err != nil {
		return err
	}
	if found {
		if _, err = c.request(ctx, "POST", "/model/delete", map[string]any{"id": id}, nil); err != nil {
			return err
		}
	}
	_, found, err = c.route(ctx, id)
	if err != nil || found {
		return errModelControl
	}
	return nil
}
