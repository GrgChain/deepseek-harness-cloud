package contextservice

import (
	"math"
	"testing"
)

func TestValidateMinimumSimilarity(t *testing.T) {
	for _, tc := range []struct {
		action, mode string
		value        float64
		valid        bool
	}{
		{"search", "semantic", -1, true}, {"search", "semantic", 0, true}, {"search", "hybrid", 1, true},
		{"search", "semantic", -1.01, false}, {"search", "semantic", 1.01, false},
		{"search", "semantic", math.NaN(), false}, {"search", "semantic", math.Inf(1), false},
		{"search", "exact", 0.5, false}, {"search", "keyword", 0.5, false}, {"list", "", 0.5, false},
	} {
		value := tc.value
		err := validateAction(tc.action, Action{ExpectedVersion: 1, Query: "example", SearchMode: tc.mode, MinSimilarity: &value})
		if (err == nil) != tc.valid {
			t.Errorf("action=%s mode=%s value=%v: %v", tc.action, tc.mode, tc.value, err)
		}
	}
}

func TestValidateKeywordSearch(t *testing.T) {
	if err := validateAction("search", Action{ExpectedVersion: 1, Query: "营业收入", SearchMode: "keyword"}); err != nil {
		t.Fatalf("keyword search rejected: %v", err)
	}
}
