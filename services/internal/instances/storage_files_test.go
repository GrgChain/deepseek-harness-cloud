package instances

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidateStorageBrowseScopes(t *testing.T) {
	tests := []struct {
		name  string
		input StorageBrowseInput
		valid bool
	}{
		{"workspace root", StorageBrowseInput{Action: "LIST", Root: "workspace"}, true},
		{"dsh subdirectory page", StorageBrowseInput{Action: "LIST", Root: "dsh", Path: "sessions/archive", After: "2026-01-01.json"}, true},
		{"text preview", StorageBrowseInput{Action: "PREVIEW", Root: "workspace", Path: "notes/readme.txt"}, true},
		{"download", StorageBrowseInput{Action: "DOWNLOAD", Root: "dsh", Path: "config.json"}, true},
		{"unknown root", StorageBrowseInput{Action: "LIST", Root: "etc"}, false},
		{"absolute path", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: "/etc/passwd"}, false},
		{"parent traversal", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: "notes/../secret"}, false},
		{"dot path", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: "./secret"}, false},
		{"windows separator", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: `notes\\secret`}, false},
		{"nul byte", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: "a\x00b"}, false},
		{"invalid utf8", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: string([]byte{0xff})}, false},
		{"nested page cursor", StorageBrowseInput{Action: "LIST", Root: "workspace", After: "a/b"}, false},
		{"cursor on file", StorageBrowseInput{Action: "PREVIEW", Root: "workspace", Path: "a.txt", After: "b.txt"}, false},
		{"empty file path", StorageBrowseInput{Action: "DOWNLOAD", Root: "workspace"}, false},
		{"oversized component", StorageBrowseInput{Action: "LIST", Root: "workspace", Path: strings.Repeat("a", 256)}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateStorageBrowse(test.input)
			if test.valid && err != nil {
				t.Fatalf("expected valid input, got %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("expected invalid input to be rejected")
			}
		})
	}
}

func TestStorageBrowseResultEncryption(t *testing.T) {
	key, public, err := NewStorageBrowseKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range [][]byte{[]byte("private file contents\n"), {}} {
		sealed, err := SealStorageBrowseContent("request-123", public, original)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := key.Open("request-123", sealed)
		if err != nil || !bytes.Equal(opened, original) {
			t.Fatalf("encrypted result did not round-trip: %q %v", opened, err)
		}
		if _, err = key.Open("another-request", sealed); err == nil {
			t.Fatal("result decrypted for a different request")
		}
		sealed[len(sealed)-1] ^= 1
		if _, err = key.Open("request-123", sealed); err == nil {
			t.Fatal("tampered result was accepted")
		}
	}
}

func TestStorageBrowseQueryParametersAreStrict(t *testing.T) {
	if _, err := storageBrowseParameters("root=workspace", "LIST"); err != nil {
		t.Fatalf("valid root directory query without optional values rejected: %v", err)
	}
	if _, err := storageBrowseParameters("root=dsh&path=projects&after=old.txt", "LIST"); err != nil {
		t.Fatalf("valid directory query rejected: %v", err)
	}
	for _, raw := range []string{
		"root=workspace&root=dsh",
		"root=workspace&path=..",
		"root=workspace&unexpected=true",
	} {
		if _, err := storageBrowseParameters(raw, "LIST"); err == nil {
			t.Errorf("invalid directory query accepted: %s", raw)
		}
	}
	if _, err := storageFileParameters("root=workspace&path=notes.txt&mode=PREVIEW"); err != nil {
		t.Fatalf("valid file query rejected: %v", err)
	}
	if _, err := storageFileParameters("root=dsh&path=key.json&mode=DOWNLOAD&after=x"); err == nil {
		t.Fatal("unknown file query parameter accepted")
	}
}
