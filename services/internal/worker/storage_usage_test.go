package worker

import "testing"

func TestStorageUsageRejectsInvalidCounters(t *testing.T) {
	for _, raw := range []string{`{"used_bytes":1}`, `{"available_bytes":1}`, `{"used_bytes":-1,"available_bytes":2}`, `{"used_bytes":101,"available_bytes":0}`, `{"used_bytes":50,"available_bytes":51}`, `{"used_bytes":0,"available_bytes":0}`, `{"used_bytes":1.5,"available_bytes":2}`, `{"used_bytes":1,"available_bytes":2,"files":["secret"]}`} {
		if _, e := decodeStorageUsage([]byte(raw), 100); e == nil {
			t.Fatal("invalid filesystem evidence accepted", raw)
		}
	}
	v, e := decodeStorageUsage([]byte(`{"used_bytes":0,"available_bytes":90}`), 100)
	if e != nil || v.Available != 90 {
		t.Fatal("filesystem overhead or real zero rejected", e)
	}
}
