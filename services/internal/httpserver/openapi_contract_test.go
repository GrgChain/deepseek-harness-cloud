package httpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIContractIntegrity(t *testing.T) {
	specPath := filepath.Join("..", "..", "..", "api", "openapi.yaml")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	if version, _ := doc["openapi"].(string); !strings.HasPrefix(version, "3.0.") {
		t.Fatalf("unexpected openapi version: %q", version)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("openapi paths missing or empty")
	}
	if _, ok := doc["components"].(map[string]any); !ok {
		t.Fatal("openapi components missing")
	}

	opIDs := map[string]string{}
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	for path, item := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("path %q must start with /", path)
		}
		ops, ok := item.(map[string]any)
		if !ok {
			t.Errorf("path %q is not an object", path)
			continue
		}
		for method, opVal := range ops {
			if !methods[strings.ToLower(method)] {
				continue
			}
			op, ok := opVal.(map[string]any)
			if !ok {
				t.Errorf("%s %s is not an object", method, path)
				continue
			}
			opID, _ := op["operationId"].(string)
			if opID == "" {
				t.Errorf("%s %s missing operationId", method, path)
			} else if prev, dup := opIDs[opID]; dup {
				t.Errorf("duplicate operationId %q on %s %s and %s", opID, method, path, prev)
			} else {
				opIDs[opID] = strings.ToUpper(method) + " " + path
			}
			if responses, ok := op["responses"].(map[string]any); !ok || len(responses) == 0 {
				t.Errorf("%s %s missing responses", method, path)
			}
		}
	}

	resolvePointer := func(ref string) bool {
		if !strings.HasPrefix(ref, "#/") {
			return false
		}
		var current any = doc
		for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
			obj, ok := current.(map[string]any)
			if !ok {
				return false
			}
			next, exists := obj[segment]
			if !exists {
				return false
			}
			current = next
		}
		return true
	}

	var checkRefs func(node any)
	checkRefs = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				if !resolvePointer(ref) {
					t.Errorf("broken $ref %s", ref)
				}
				return
			}
			for _, child := range v {
				checkRefs(child)
			}
		case []any:
			for _, child := range v {
				checkRefs(child)
			}
		}
	}
	checkRefs(doc)
}
