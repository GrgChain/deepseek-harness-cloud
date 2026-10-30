package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
	Definition  json.RawMessage `json:"definition"`
	Hash        string          `json:"hash"`
	Enabled     bool            `json:"enabled"`
	Change      string          `json:"change,omitempty"`
}

func digest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func schemaFor(raw []byte) (*jsonschema.Resolved, error) {
	if len(raw) > 64<<10 {
		return nil, errors.New("tool schema too large")
	}
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		return nil, errors.New("invalid schema")
	}
	nodes := 0
	var walk func(any, int) error
	walk = func(v any, depth int) error {
		nodes++
		if nodes > 2048 || depth > 32 {
			return errors.New("schema complexity limit")
		}
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				if k == "default" || k == "const" || k == "enum" || k == "examples" {
					continue
				}
				if k == "type" {
					var types []string
					if str, ok := item.(string); ok {
						types = []string{str}
					}
					if values, ok := item.([]any); ok {
						for _, v := range values {
							str, ok := v.(string)
							if !ok {
								return errors.New("invalid schema type")
							}
							types = append(types, str)
						}
					}
					for _, typ := range types {
						switch typ {
						case "object", "array", "string", "number", "integer", "boolean", "null":
						default:
							return errors.New("invalid schema type")
						}
					}
				}

				if k == "$ref" || k == "$dynamicRef" {
					ref, _ := item.(string)
					if !strings.HasPrefix(ref, "#") {
						return errors.New("external schema references denied")
					}
				}
				if k == "pattern" {
					s, _ := item.(string)
					if len(s) > 512 {
						return errors.New("schema pattern too long")
					}
				}
				if err := walk(item, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := walk(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(tree, 0); err != nil {
		return nil, err
	}
	// Expand local references under a shared budget. Cycles and schemas whose
	// expansion is much larger than their document are unsuitable for an
	// interactive client and must not reach the validator's recursive walker.
	expanded := 0
	var expand func(any, map[string]bool) error
	expand = func(v any, active map[string]bool) error {
		expanded++
		if expanded > 8192 {
			return errors.New("schema reference expansion limit")
		}
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["$dynamicRef"]; ok {
				return errors.New("dynamic schema references unsupported")
			}
			if ref, ok := x["$ref"].(string); ok {
				if active[ref] {
					return errors.New("recursive tool schema unsupported")
				}
				if ref != "#" && !strings.HasPrefix(ref, "#/") {
					return errors.New("schema anchors unsupported")
				}
				target := tree
				if ref != "#" {
					for _, part := range strings.Split(ref[2:], "/") {
						object, ok := target.(map[string]any)
						if !ok {
							return errors.New("invalid schema reference")
						}
						target = object[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
					}
				}
				active[ref] = true
				err := expand(target, active)
				delete(active, ref)
				if err != nil {
					return err
				}
			}
			for k, item := range x {
				if k == "default" || k == "const" || k == "enum" || k == "examples" {
					continue
				}
				if err := expand(item, active); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := expand(item, active); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := expand(tree, map[string]bool{}); err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	if schema.Type != "object" {
		return nil, errors.New("tool input schema must be object")
	}
	return schema.Resolve(&jsonschema.ResolveOptions{Loader: func(*url.URL) (*jsonschema.Schema, error) { return nil, errors.New("external schema loading denied") }})
}
func openSession(ctx context.Context, e Endpoint, credential, version string) (*mcp.ClientSession, error) {
	httpClient, err := endpointClient(ctx, e, credential)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "DSH Cloud Portal", Version: "0.1.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: e.URL, HTTPClient: httpClient, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: maxBytes}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		return nil, err
	}
	// New connections prefer the modern protocol and accept the SDK's legacy
	// handshake fallback. Explicit legacy configurations remain pinned.
	negotiated := session.InitializeResult().ProtocolVersion
	if negotiated != version && !(version == "2026-07-28" && negotiated == "2025-11-25") {
		session.Close()
		return nil, errors.New("MCP protocol version mismatch")
	}
	return session, nil
}
func discover(ctx context.Context, session *mcp.ClientSession) ([]Tool, error) {
	items := []Tool{}
	seen := map[string]bool{}
	names := map[string]bool{}
	cursor := ""
	size := 0
	for page := 0; page < 100; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, tool := range result.Tools {
			if tool == nil || len(tool.Name) == 0 || len(tool.Name) > 256 || names[tool.Name] {
				return nil, errors.New("invalid or repeated tool name")
			}
			definition, err := json.Marshal(tool)
			if err != nil {
				return nil, err
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, err
			}
			if _, err = schemaFor(schema); err != nil {
				return nil, err
			}
			size += len(definition)
			if len(items) >= 1000 || size > maxBytes {
				return nil, errors.New("MCP catalog limit exceeded")
			}
			items = append(items, Tool{Name: tool.Name, Description: tool.Description, Schema: schema, Definition: definition, Hash: digest(definition)})
			names[tool.Name] = true
		}
		if result.NextCursor == "" {
			return items, nil
		}
		if seen[result.NextCursor] {
			return nil, errors.New("repeated MCP cursor")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
	return nil, errors.New("MCP page limit exceeded")
}
