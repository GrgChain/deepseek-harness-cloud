package workspace

import "testing"

func TestStableNameRetainsWholeInstanceIdentity(t *testing.T) {
	a, err := WorkspaceName("00000000-0000-4000-8000-000000000001")
	if err != nil || len(a) != 29 {
		t.Fatalf("name contract: %s %v", a, err)
	}
	b, _ := WorkspaceName("00000000-0000-4000-8000-000000000002")
	if a == b {
		t.Fatal("different instance identities collapsed")
	}
	again, _ := WorkspaceName("00000000-0000-4000-8000-000000000001")
	if a != again {
		t.Fatal("unstable name")
	}
	if _, err := WorkspaceName("../../../untrusted"); err == nil {
		t.Fatal("invalid identity accepted")
	}
}

func TestManagedOwnerStatusDistinguishesDormantFromSuspended(t *testing.T) {
	for _, status := range []string{"active", "dormant"} {
		if !OwnerStatusUsable(status) {
			t.Errorf("valid managed owner status rejected: %s", status)
		}
	}
	for _, status := range []string{"suspended", "deleted", "unknown", ""} {
		if OwnerStatusUsable(status) {
			t.Errorf("disabled or unknown owner status accepted: %s", status)
		}
	}
}
