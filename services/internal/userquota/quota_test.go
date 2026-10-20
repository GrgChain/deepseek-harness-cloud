package userquota

import (
	"math"
	"testing"
)

func TestAdmissionChecksEveryUserDimensionAndAllowsReleasedCompute(t *testing.T) {
	v := View{Limits: Limits{2, 4000, 8 << 30, 20 << 30}, Usage: Limits{1, 2000, 4 << 30, 10 << 30}}
	demand := map[string]int64{"INSTANCE": 1, "CPU": 2000, "MEMORY": 4 << 30, "STORAGE": 10 << 30}
	if err := v.Check(demand); err != nil {
		t.Fatal("exact remaining resources rejected", err)
	}
	for resource := range demand {
		t.Run(resource, func(t *testing.T) {
			exceeded := map[string]int64{}
			for key, value := range demand {
				exceeded[key] = value
			}
			exceeded[resource]++
			if v.Check(exceeded) == nil {
				t.Fatal("over-budget resource admitted")
			}
		})
	}
	v.Limits, v.Usage = Limits{1, 0, 0, 10 << 30}, Limits{1, 0, 0, 10 << 30}
	if err := v.Check(nil); err != nil {
		t.Fatal("zero compute allowance blocked operation without new commitments", err)
	}
	if v.Check(map[string]int64{"CPU": 1}) == nil {
		t.Fatal("zero allowance admitted compute")
	}
}

func TestAdmissionRejectsOverflowAndRecoveryDebt(t *testing.T) {
	v := View{Limits: Limits{Storage: math.MaxInt64}, Usage: Limits{Storage: math.MaxInt64 - 1}}
	if v.Check(map[string]int64{"STORAGE": 2}) == nil {
		t.Fatal("overflow created quota credit")
	}
	v.Usage.Storage = math.MaxInt64
	v.Limits.Storage--
	if v.Check(nil) == nil {
		t.Fatal("recovery debt allowed new admission")
	}
}
