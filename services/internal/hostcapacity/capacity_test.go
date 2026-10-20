package hostcapacity

import "testing"

func TestSelectedResourcesPreserveControlPlaneReserve(t *testing.T) {
	for _, tc := range []struct {
		facts       Facts
		cpu, memory int64
		want        bool
	}{
		{Facts{20, 72 << 30}, 16000, 64 << 30, true},
		{Facts{19, 72 << 30}, 16000, 64 << 30, false},
		{Facts{20, (72 << 30) - 1}, 16000, 64 << 30, false},
		{Facts{6, 12 << 30}, 1000, 2 << 30, true},
		{Facts{32, 128 << 30}, 0, 2 << 30, false},
		{Facts{32, 128 << 30}, 1000, 0, false},
		{Facts{4, 128 << 30}, 1000, 2 << 30, false},
	} {
		if got := tc.facts.Fits(tc.cpu, tc.memory); got != tc.want {
			t.Fatalf("facts=%+v cpu=%d memory=%d fits=%t want=%t", tc.facts, tc.cpu, tc.memory, got, tc.want)
		}
	}
}

func TestSlotsPreserveControlReserveAndFixedSpecification(t *testing.T) {
	for _, tc := range []struct {
		cpu    int
		memory int64
		slots  int
	}{
		{16, 13 << 30, 1}, {16, 12 << 30, 1}, {16, (12 << 30) - 1, 0},
		{5, 64 << 30, 0}, {6, 64 << 30, 1}, {32, 64 << 30, 5}, {16, 0, 0},
	} {
		if got := (Facts{tc.cpu, tc.memory}).Slots(); got != tc.slots {
			t.Fatalf("cpu=%d available=%d slots=%d want=%d", tc.cpu, tc.memory, got, tc.slots)
		}
	}
}

func TestMemAvailableIsRequiredAndBounded(t *testing.T) {
	for _, raw := range []string{"MemTotal: 64000000 kB\n", "MemAvailable: -1 kB\n", "MemAvailable: 100 GB\n", "MemAvailable: 9223372036854775807 kB\n", "MemAvailable: 1 kB\nMemAvailable: 2 kB\n"} {
		if _, err := parseMemory(raw); err == nil {
			t.Fatalf("accepted invalid memory facts %q", raw)
		}
	}
	if n, err := parseMemory("MemTotal: 32596044 kB\nMemAvailable: 13506100 kB\n"); err != nil || n != 13506100*1024 {
		t.Fatalf("available memory=%d err=%v", n, err)
	}
}
