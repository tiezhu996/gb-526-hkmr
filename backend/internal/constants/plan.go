package constants

type PlanStatus string

const (
	PlanDraft            PlanStatus = "draft"
	PlanModeled          PlanStatus = "modeled"
	PlanPendingReview    PlanStatus = "pending_supervisor_review"
	PlanApprovedTraining PlanStatus = "approved_for_training"
	PlanArchived         PlanStatus = "archived"
)

var planTransitions = map[PlanStatus]map[PlanStatus]bool{
	PlanDraft:            {PlanModeled: true},
	PlanModeled:          {PlanDraft: true, PlanPendingReview: true},
	PlanPendingReview:    {PlanDraft: true, PlanApprovedTraining: true},
	PlanApprovedTraining: {PlanArchived: true},
	PlanArchived:         {},
}

func ValidPlanStatus(status PlanStatus) bool {
	_, ok := planTransitions[status]
	return ok
}

func CanTransitionPlan(from, to PlanStatus) bool {
	return planTransitions[from][to]
}

func PlanStatuses() []PlanStatus {
	return []PlanStatus{PlanDraft, PlanModeled, PlanPendingReview, PlanApprovedTraining, PlanArchived}
}

// AssessmentStatus mirrors the review state stored on an immutable
// assessment. It is deliberately a separate string so that an assessment can
// remain "returned" while the plan itself is simply "draft".
type AssessmentStatus string

const (
	AssessmentModeled  AssessmentStatus = "modeled"
	AssessmentPending  AssessmentStatus = "pending_supervisor_review"
	AssessmentReturned AssessmentStatus = "returned"
	AssessmentApproved AssessmentStatus = "approved_for_training"
	AssessmentArchived AssessmentStatus = "archived"
)

var validAssessmentStatuses = map[AssessmentStatus]bool{
	AssessmentModeled:  true,
	AssessmentPending:  true,
	AssessmentReturned: true,
	AssessmentApproved: true,
	AssessmentArchived: true,
}

func ValidAssessmentStatus(status string) bool {
	return validAssessmentStatuses[AssessmentStatus(status)]
}

func AssessmentStatuses() []AssessmentStatus {
	return []AssessmentStatus{AssessmentModeled, AssessmentPending, AssessmentReturned, AssessmentApproved, AssessmentArchived}
}
