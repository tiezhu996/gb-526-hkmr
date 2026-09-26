package constants

import "testing"

func TestAssessmentStatuses(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{string(AssessmentModeled), true},
		{string(AssessmentPending), true},
		{string(AssessmentReturned), true},
		{string(AssessmentApproved), true},
		{string(AssessmentArchived), true},
		{"draft", false},
		{"", false},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			if got := ValidAssessmentStatus(test.status); got != test.want {
				t.Fatalf("ValidAssessmentStatus(%q) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

func TestReturnedIsNotPlanStatus(t *testing.T) {
	// "returned" lives only on assessments; the plan itself goes back to draft.
	if CanTransitionPlan(PlanPendingReview, PlanStatus(AssessmentReturned)) {
		t.Fatal("assessment-only status must not be a plan transition target")
	}
	if !CanTransitionPlan(PlanPendingReview, PlanDraft) {
		t.Fatal("supervisor must be able to return a pending plan to draft")
	}
	if !CanTransitionPlan(PlanModeled, PlanDraft) {
		t.Fatal("planner must be able to revise a modeled plan back to draft")
	}
}
