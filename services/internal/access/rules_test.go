package access

import "testing"

func TestAccessAuthorityCannotEscapeConfiguredSite(t *testing.T) {
	rules, err := NewRules("https://portal.harness.test:42013", "harness.test")
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-4000-8000-000000000001"
	host := "i-00000000000040008000000000000001.harness.test"
	if rules.Host(id) != host || rules.URL(host) != "https://"+host+":42013" {
		t.Fatal("development authority did not retain the configured HTTPS port")
	}
	if got, ok := rules.Resolve(host + ":42013"); !ok || got != id {
		t.Fatal("fixed instance authority not recognized")
	}
	for _, bad := range []string{host, host + ":443", host + ":42013.evil.test", host + ".evil.test:42013", "I-00000000000040008000000000000001.harness.test:42013", "i-00000000000000000000000000000001.harness.test:42013", "127.0.0.1:42013", "portal.harness.test:42013"} {
		if _, ok := rules.Resolve(bad); ok {
			t.Fatalf("foreign or noncanonical authority accepted: %s", bad)
		}
	}
	for _, portal := range []string{"http://portal.harness.test:42013", "https://portal.foreign.test:42013", "https://127.0.0.1:42013", "https://portal.harness.test:42013/path", "https://user@portal.harness.test:42013", "https://portal.harness.test:42013/?token=value"} {
		if _, err := NewRules(portal, "harness.test"); err == nil {
			t.Fatal("unsafe or unrelated Portal origin accepted")
		}
	}
}
