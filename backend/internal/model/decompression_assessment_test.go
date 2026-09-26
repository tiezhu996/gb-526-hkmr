package model

import "testing"

func TestDecompressionAssessmentStale(t *testing.T) {
	cases := []struct {
		name               string
		assessmentInputVer uint
		currentInputVer    uint
		wantStale          bool
	}{
		{"same input version stays current", 3, 3, false},
		{"input advanced after run is stale", 3, 4, true},
		{"multiple edits still stale", 1, 5, true},
		{"initial version matches", 1, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assessment := DecompressionAssessment{InputVersion: tc.assessmentInputVer}
			if got := assessment.Stale(tc.currentInputVer); got != tc.wantStale {
				t.Fatalf("Stale(%d) with input_version=%d = %v, want %v", tc.currentInputVer, tc.assessmentInputVer, got, tc.wantStale)
			}
		})
	}
}
