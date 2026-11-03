package workspaceextensions

import "testing"

func TestSessionMemorySelection(t *testing.T) {
	s := Selection{ID: "00000000-0000-4000-8000-000000000001", Version: 1, Scopes: []string{"personal"}, SessionMemory: true, SessionScope: "personal"}
	if !(Input{Memory: []Selection{s}}).Valid() {
		t.Fatal("valid session memory rejected")
	}
	if (Input{Knowledge: []Selection{s}}).Valid() {
		t.Fatal("session capture allowed on knowledge")
	}
	s.SessionScope = ""
	if !(Input{Memory: []Selection{s}}).Valid() {
		t.Fatal("shared session capture requires a caller-selected scope")
	}
	s.SessionScope = "workspace"
	if (Input{Memory: []Selection{s}}).Valid() {
		t.Fatal("scope expanded")
	}
	s.SessionScope = "personal"
	second := s
	second.ID = "00000000-0000-4000-8000-000000000002"
	if (Input{Memory: []Selection{s, second}}).Valid() {
		t.Fatal("multiple native plugins allowed")
	}
	s.SessionMemory = false
	if (Input{Memory: []Selection{s}}).Valid() {
		t.Fatal("inactive capture retained scope")
	}
}

func TestKnowledgePermissions(t *testing.T) {
	legacy := Item{Kind: "knowledge"}
	if !legacy.KnowledgeAllowed("read") || legacy.KnowledgeAllowed("write") || legacy.KnowledgeAllowed("delete") {
		t.Fatal("legacy grants expanded")
	}
	for _, permission := range []string{"read", "write", "delete"} {
		item := Item{Kind: "knowledge", Permissions: []string{permission}}
		for _, action := range []string{"read", "write", "delete"} {
			if item.KnowledgeAllowed(action) != (action == permission) {
				t.Fatal("permissions not independent")
			}
		}
	}
	selection := Selection{ID: "00000000-0000-4000-8000-000000000001", Version: 1}
	for _, permissions := range [][]string{nil, {"read"}, {"write"}, {"delete"}, {"read", "write", "delete"}} {
		selection.Permissions = permissions
		if !(Input{Knowledge: []Selection{selection}}).Valid() {
			t.Fatal("valid permission rejected", permissions)
		}
	}
	for _, permissions := range [][]string{{}, {"admin"}, {"write", "write"}} {
		selection.Permissions = permissions
		if (Input{Knowledge: []Selection{selection}}).Valid() {
			t.Fatal("invalid permission accepted", permissions)
		}
	}
	selection.Permissions = []string{"write"}
	if (Input{Memory: []Selection{selection}}).Valid() {
		t.Fatal("knowledge permissions applied to memory")
	}
}

func TestSingleMemoryConnection(t *testing.T) {
	first := Selection{ID: "00000000-0000-4000-8000-000000000001", Version: 1}
	second := Selection{ID: "00000000-0000-4000-8000-000000000002", Version: 1}
	for _, selections := range [][]Selection{nil, {first}} {
		if !(Input{Memory: selections}).Valid() {
			t.Fatal("empty or single memory rejected")
		}
	}
	if (Input{Memory: []Selection{first, second}}).Valid() {
		t.Fatal("multiple memory connections allowed")
	}
	if !(Input{Knowledge: []Selection{first, second}}).Valid() {
		t.Fatal("knowledge connections incorrectly restricted")
	}
}
