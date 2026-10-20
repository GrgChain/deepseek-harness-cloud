package runtimeapproval

import "testing"

func TestSupportedResourceBoundary(t *testing.T) {
	for _, cpu := range []int64{1000, 2000, 4000, 8000, 16000} {
		for _, memory := range []int64{2 << 30, 4 << 30, 8 << 30, 16 << 30, 32 << 30, 64 << 30} {
			for _, gib := range []int64{1, 10, 20, 100} {
				if !SupportedResources(cpu, memory, gib*GiB) {
					t.Fatal("approved tuple rejected", gib)
				}
			}
		}
	}
	for _, bad := range [][3]int64{{0, 2 << 30, 10 << 30}, {3000, 2 << 30, 10 << 30}, {1000, 0, 10 << 30}, {1000, 3 << 30, 10 << 30}, {32000, 2 << 30, 10 << 30}, {1000, 128 << 30, 10 << 30}, {1000, 2 << 30, 0}, {1000, 2 << 30, GiB + 1}, {1000, 2 << 30, 101 * GiB}} {
		if SupportedResources(bad[0], bad[1], bad[2]) {
			t.Fatal("unsupported tuple accepted", bad)
		}
	}
}
