package pagination

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCursorBindsPositionToCallerQueryAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	scope := Scope{Subject: "00000000-0000-4000-8000-000000000001", Organization: "00000000-0000-4000-8000-000000000002", Resource: "instances", Filters: `{"state":"RUNNING"}`}
	position := Position{Timestamp: now.Add(-time.Minute), ID: "00000000-0000-4000-8000-000000000003"}
	codec, err := NewCodec(bytes.Repeat([]byte{9}, 32), "test")
	if err != nil {
		t.Fatal(err)
	}
	token, err := codec.Encode(scope, position, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(token, scope, now.Add(14*time.Minute))
	if err != nil || got != position {
		t.Fatalf("valid cursor: %+v %v", got, err)
	}
	for _, changed := range []Scope{
		{Subject: scope.Organization, Organization: scope.Organization, Resource: scope.Resource, Filters: scope.Filters},
		{Subject: scope.Subject, Organization: scope.Subject, Resource: scope.Resource, Filters: scope.Filters},
		{Subject: scope.Subject, Organization: scope.Organization, Resource: "audit-events", Filters: scope.Filters},
		{Subject: scope.Subject, Organization: scope.Organization, Resource: scope.Resource, Filters: `{"state":"STOPPED"}`},
	} {
		if _, err := codec.Decode(token, changed, now); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("cross-scope cursor accepted: %v", err)
		}
	}
	for _, when := range []time.Time{now.Add(15 * time.Minute), now.Add(-time.Second)} {
		if _, err := codec.Decode(token, scope, when); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("invalid lifetime accepted: %v", err)
		}
	}
	other, _ := NewCodec(bytes.Repeat([]byte{9}, 32), "another-environment")
	if _, err := other.Decode(token, scope, now); err == nil {
		t.Fatal("cross-environment signature accepted")
	}
	other, _ = NewCodec(bytes.Repeat([]byte{8}, 32), "test")
	if _, err := other.Decode(token, scope, now); err == nil {
		t.Fatal("wrong-key signature accepted")
	}
	parts := strings.Split(token, ".")
	for _, bad := range []string{"", token + ".", strings.Repeat("a", 2049), "!.", parts[0] + ".AAAA", parts[0] + "=." + parts[1], "a" + parts[0][1:] + "." + parts[1]} {
		if _, err := codec.Decode(bad, scope, now); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("malformed cursor accepted: %v", err)
		}
	}
}
