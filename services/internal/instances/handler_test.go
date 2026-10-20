package instances

import "testing"

func TestQueryParametersRejectAmbiguousOrUnboundedInputs(t *testing.T) {
	for _, raw := range []string{"limit=101", "limit=0", "limit=1&limit=2", "include_deleted=1", "unknown=true", "cursor=", "q=%ZZ", "page=0", "page=-1", "page=1000000001", "page=1&page=2", "page=2&cursor=opaque", "page=oops"} {
		if _, err := listParameters(raw); err == nil {
			t.Fatalf("accepted ambiguous list query %q", raw)
		}
	}
	valid, err := listParameters("limit=10&page=3&state=STOPPED&include_deleted=true&q=work-")
	if err != nil || valid.Page != 3 || valid.Limit != 10 || !valid.IncludeDeleted || valid.Query != "work-" {
		t.Fatalf("valid filters: %+v %v", valid, err)
	}
	for _, raw := range []string{"from=2026-10-10", "from=2026-10-10T08:00:00%2B08:00", "from=", "limit=1000", "action=a&action=b", "owner_id=spoof", "page=0", "page=2&cursor=opaque", "page=1&page=2"} {
		if _, err := operationLogParameters(raw); err == nil {
			t.Fatalf("accepted ambiguous operation log query %q", raw)
		}
	}
	if _, err := operationLogParameters("from=2026-10-09T00:00:00Z&to=2026-10-10T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}
