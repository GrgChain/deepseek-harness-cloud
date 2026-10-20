package modelgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type DiscoveredModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AllowsEndpoint checks exact provider roots or an explicitly configured custom root.
func AllowsEndpoint(prefix, base string, customBases []string) bool {
	return approvedParams(map[string]any{"model": prefix + "/discovery", "api_base": base, "max_retries": float64(0)}, customBases...)
}

func DiscoverModels(ctx context.Context, prefix, base, key string, customBases []string) ([]DiscoveredModel, error) {
	if !AllowsEndpoint(prefix, base, customBases) {
		return nil, errProtocol
	}
	endpoint := base
	switch prefix {
	case "anthropic", "custom_anthropic":
		if strings.HasSuffix(endpoint, "/v1") {
			endpoint += "/models"
		} else {
			endpoint += "/v1/models"
		}
	case "gemini":
		endpoint += "/v1beta/models"
	default:
		endpoint += "/models"
	}
	client := providerClient(customBases...)
	defer client.CloseIdleConnections()
	return discoverModels(ctx, client, prefix, endpoint, key)
}
func discoverModels(ctx context.Context, client *http.Client, prefix, endpoint, key string) ([]DiscoveredModel, error) {
	items := []DiscoveredModel{}
	seen := map[string]bool{}
	cursor := ""
	// Bound paginated lists; exceeding bounds is an error, never a partial success.
	for page := 0; page < 10; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errProtocol
		}
		request.Header.Set("Accept", "application/json")
		q := request.URL.Query()
		switch prefix {
		case "anthropic", "custom_anthropic":
			request.Header.Set("anthropic-version", "2023-06-01")
			if key != "" {
				request.Header.Set("x-api-key", key)
			}
			q.Set("limit", "100")
			if cursor != "" {
				q.Set("after_id", cursor)
			}
		case "gemini":
			request.Header.Set("x-goog-api-key", key)
			q.Set("pageSize", "100")
			if cursor != "" {
				q.Set("pageToken", cursor)
			}
		default:
			if key != "" {
				request.Header.Set("Authorization", "Bearer "+key)
			}
		}
		request.URL.RawQuery = q.Encode()
		response, err := client.Do(request)
		if err != nil {
			return nil, errProtocol
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		response.Body.Close()
		if readErr != nil || len(raw) > 1<<20 || response.StatusCode != 200 {
			clear(raw)
			return nil, errProtocol
		}
		var payload struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"display_name"`
			} `json:"data"`
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
			Next    string `json:"nextPageToken"`
		}
		err = json.Unmarshal(raw, &payload)
		clear(raw)
		if err != nil || (payload.Data == nil && payload.Models == nil) {
			return nil, errProtocol
		}
		for _, model := range payload.Models {
			generate := false
			for _, method := range model.Methods {
				generate = generate || method == "generateContent"
			}
			if generate {
				payload.Data = append(payload.Data, struct {
					ID   string `json:"id"`
					Name string `json:"display_name"`
				}{strings.TrimPrefix(model.Name, "models/"), model.DisplayName})
			}
		}
		for _, model := range payload.Data {
			if len(model.ID) == 0 || len(model.ID) > 128 || strings.ContainsAny(model.ID, "?&#%\\ \t\r\n") {
				return nil, errProtocol
			}
			if !seen[model.ID] {
				seen[model.ID] = true
				name := model.Name
				if name == "" {
					name = model.ID
				}
				items = append(items, DiscoveredModel{model.ID, name})
			}
			if len(items) > 1000 {
				return nil, errProtocol
			}
		}
		next := ""
		if prefix == "gemini" {
			next = payload.Next
		} else if (prefix == "anthropic" || prefix == "custom_anthropic") && payload.HasMore {
			next = payload.LastID
			if next == "" {
				return nil, errProtocol
			}
		}
		if next == "" {
			return items, nil
		}
		if next == cursor {
			return nil, errProtocol
		}
		cursor = next
	}
	return nil, errProtocol
}
