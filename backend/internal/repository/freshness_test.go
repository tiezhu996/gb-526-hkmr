package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"commercial-diving-decompression-control/backend/internal/audit"
	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newFreshnessDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.DiverProfile{}, &model.DivePlan{}, &model.ExposureSegment{}, &model.DecompressionAssessment{}, &audit.Event{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedFreshnessPlan(t *testing.T, db *gorm.DB, planStatus constants.PlanStatus, assessmentStatus constants.AssessmentStatus, version uint) (model.DivePlan, model.DecompressionAssessment) {
	t.Helper()
	profile := model.DiverProfile{ProfileCode: "TRN-FRESH", DisplayName: "Freshness Profile", ProfileStatus: "active", Version: 1}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	plan := model.DivePlan{PlanCode: "PLAN-FRESH", DiverProfileID: profile.ID, WorksitePressureBar: 1, BreathingMixJSON: "{}", PlanStatus: planStatus, CreatedBy: 1, Version: version, PlannedAt: time.Now().UTC()}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	segment := model.ExposureSegment{PlanID: plan.ID, SequenceNo: 1, DepthM: 30, DurationMin: 10, GasMixJSON: "{}", SegmentType: "bottom"}
	if err := db.Create(&segment).Error; err != nil {
		t.Fatalf("create segment: %v", err)
	}
	assessment := model.DecompressionAssessment{
		PlanID: plan.ID, AssessmentStatus: string(assessmentStatus), AlgorithmVersion: "test-v1",
		InputSnapshotJSON: "{}", CompartmentLoadsJSON: "[]", RiskFlagsJSON: "[]",
		HighestRiskBand: "informational", AssumptionsJSON: "{}", InputVersion: version,
	}
	if err := db.Create(&assessment).Error; err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	return plan, assessment
}

func actor() audit.Entry {
	return audit.Entry{RequestID: "req-test", ActorID: 1, ActorUsername: "planner"}
}

// Changing draft inputs bumps the version and expires the prior modeled result
// inside one transaction.
func TestSegmentUpdateExpiresAssessment(t *testing.T) {
	db := newFreshnessDB(t)
	auditRepo := audit.NewRepository(db)
	assessmentRepo := NewDecompressionAssessmentRepository(db, auditRepo)
	segmentRepo := NewExposureSegmentRepository(db, auditRepo, assessmentRepo)
	ctx := context.Background()

	plan, assessment := seedFreshnessPlan(t, db, constants.PlanDraft, constants.AssessmentModeled, 2)

	changes := map[string]any{"depth_m": 34.5}
	entry := actor()
	entry.Action = "exposure_segment.update"
	entry.EntityType = "exposure_segment"
	if err := segmentRepo.Update(ctx, model.ExposureSegment{ID: 1, PlanID: plan.ID}, 2, changes, entry); err != nil {
		t.Fatalf("update segment: %v", err)
	}

	var refreshed model.DivePlan
	if err := db.First(&refreshed, plan.ID).Error; err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	if refreshed.Version != 3 {
		t.Fatalf("plan version = %d, want 3", refreshed.Version)
	}
	var expired model.DecompressionAssessment
	if err := db.First(&expired, assessment.ID).Error; err != nil {
		t.Fatalf("reload assessment: %v", err)
	}
	if !expired.Stale {
		t.Fatal("assessment must be marked stale after input change")
	}
	if expired.InputVersion != 2 {
		t.Fatalf("recorded input version = %d, want 2", expired.InputVersion)
	}

	var staleEvents int64
	if err := db.Model(&audit.Event{}).Where("action = ? AND entity_id = ?", "decompression_assessment.stale", assessment.ID).Count(&staleEvents).Error; err != nil {
		t.Fatalf("count stale events: %v", err)
	}
	if staleEvents != 1 {
		t.Fatalf("stale audit events = %d, want 1", staleEvents)
	}
}

// A second input change must not duplicate stale flags or audit events.
func TestRepeatedInputChangeDoesNotDuplicateStaleEvent(t *testing.T) {
	db := newFreshnessDB(t)
	auditRepo := audit.NewRepository(db)
	assessmentRepo := NewDecompressionAssessmentRepository(db, auditRepo)
	segmentRepo := NewExposureSegmentRepository(db, auditRepo, assessmentRepo)
	ctx := context.Background()

	plan, assessment := seedFreshnessPlan(t, db, constants.PlanDraft, constants.AssessmentModeled, 1)

	for index := 0; index < 2; index++ {
		var segment model.ExposureSegment
		if err := db.Where("plan_id = ?", plan.ID).First(&segment).Error; err != nil {
			t.Fatalf("load segment: %v", err)
		}
		var livePlan model.DivePlan
		if err := db.First(&livePlan, plan.ID).Error; err != nil {
			t.Fatalf("load plan: %v", err)
		}
		entry := actor()
		entry.Action = "exposure_segment.update"
		entry.EntityType = "exposure_segment"
		if err := segmentRepo.Update(ctx, segment, livePlan.Version, map[string]any{"depth_m": 31.0 + float64(index)}, entry); err != nil {
			t.Fatalf("update %d: %v", index, err)
		}
	}

	var staleEvents int64
	if err := db.Model(&audit.Event{}).Where("action = ? AND entity_id = ?", "decompression_assessment.stale", assessment.ID).Count(&staleEvents).Error; err != nil {
		t.Fatalf("count stale events: %v", err)
	}
	if staleEvents != 1 {
		t.Fatalf("stale audit events = %d, want exactly 1", staleEvents)
	}
}

// Supervisor return moves plan to draft, assessment to "returned", and keeps
// the mandatory reason.
func TestSupervisorReturnRecordsReason(t *testing.T) {
	db := newFreshnessDB(t)
	auditRepo := audit.NewRepository(db)
	assessmentRepo := NewDecompressionAssessmentRepository(db, auditRepo)
	ctx := context.Background()

	plan, assessment := seedFreshnessPlan(t, db, constants.PlanPendingReview, constants.AssessmentPending, 3)

	entry := actor()
	entry.ActorUsername = "supervisor"
	entry.Action = "decompression_assessment.return"
	if err := assessmentRepo.BackToDraft(ctx, plan, assessment, constants.AssessmentReturned, "bottom time exceeds training rule", entry); err != nil {
		t.Fatalf("return: %v", err)
	}

	var reloadedPlan model.DivePlan
	if err := db.First(&reloadedPlan, plan.ID).Error; err != nil {
		t.Fatalf("reload plan: %v", err)
	}
	if reloadedPlan.PlanStatus != constants.PlanDraft {
		t.Fatalf("plan status = %s, want draft", reloadedPlan.PlanStatus)
	}
	var reloaded model.DecompressionAssessment
	if err := db.First(&reloaded, assessment.ID).Error; err != nil {
		t.Fatalf("reload assessment: %v", err)
	}
	if reloaded.AssessmentStatus != string(constants.AssessmentReturned) {
		t.Fatalf("assessment status = %s, want returned", reloaded.AssessmentStatus)
	}
	if reloaded.ReturnReason != "bottom time exceeds training rule" {
		t.Fatalf("return reason = %q", reloaded.ReturnReason)
	}
}

// Archived results are never marked stale when later inputs change.
func TestArchivedAssessmentNotExpired(t *testing.T) {
	db := newFreshnessDB(t)
	auditRepo := audit.NewRepository(db)
	assessmentRepo := NewDecompressionAssessmentRepository(db, auditRepo)
	segmentRepo := NewExposureSegmentRepository(db, auditRepo, assessmentRepo)
	ctx := context.Background()

	plan, assessment := seedFreshnessPlan(t, db, constants.PlanDraft, constants.AssessmentModeled, 1)
	if err := db.Model(&model.DecompressionAssessment{}).Where("id = ?", assessment.ID).Update("assessment_status", constants.AssessmentArchived).Error; err != nil {
		t.Fatalf("archive: %v", err)
	}

	entry := actor()
	entry.Action = "exposure_segment.update"
	entry.EntityType = "exposure_segment"
	var segment model.ExposureSegment
	if err := db.Where("plan_id = ?", plan.ID).First(&segment).Error; err != nil {
		t.Fatalf("load segment: %v", err)
	}
	if err := segmentRepo.Update(ctx, segment, 1, map[string]any{"depth_m": 12.0}, entry); err != nil {
		t.Fatalf("update: %v", err)
	}
	var archived model.DecompressionAssessment
	if err := db.First(&archived, assessment.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if archived.Stale {
		t.Fatal("archived assessment must never be marked stale")
	}
}
