package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"commercial-diving-decompression-control/backend/internal/audit"
	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/decompression"
	"commercial-diving-decompression-control/backend/internal/dto"
	"commercial-diving-decompression-control/backend/internal/model"
	"commercial-diving-decompression-control/backend/internal/repository"
	"commercial-diving-decompression-control/backend/internal/util"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() { gin.SetMode(gin.TestMode) }

type freshnessHarness struct {
	db         *gorm.DB
	plans      *repository.DivePlanRepository
	segments   *repository.ExposureSegmentRepository
	assess     *repository.DecompressionAssessmentRepository
	profiles   *repository.DiverProfileRepository
	service    *DecompressionAssessmentService
	segmentSvc *ExposureSegmentService
	planID     uint
}

func newFreshnessHarness(t *testing.T) *freshnessHarness {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.DiverProfile{}, &model.DivePlan{}, &model.ExposureSegment{}, &model.DecompressionAssessment{}, &audit.Event{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	auditRepo := audit.NewRepository(db)
	profiles := repository.NewDiverProfileRepository(db, auditRepo)
	plans := repository.NewDivePlanRepository(db, auditRepo)
	assess := repository.NewDecompressionAssessmentRepository(db, auditRepo)
	segments := repository.NewExposureSegmentRepository(db, auditRepo, assess)

	air, _ := decompression.EncodeGasMix(decompression.GasMix{O2: 0.21, N2: 0.79})
	profile := model.DiverProfile{ProfileCode: "TRN-SVC", DisplayName: "Service Profile", QualificationLevel: "commercial", DefaultO2Fraction: 0.21, DefaultHeFraction: 0, ProfileStatus: "active", Version: 1}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatalf("create profile: %v", err)
	}
	plan := model.DivePlan{PlanCode: "PLAN-SVC", DiverProfileID: profile.ID, WorksitePressureBar: 1, BreathingMixJSON: air, PlanStatus: constants.PlanDraft, CreatedBy: 7, Version: 1, PlannedAt: time.Now().UTC().Add(24 * time.Hour)}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	rows := []model.ExposureSegment{
		{PlanID: plan.ID, SequenceNo: 1, DepthM: 30, DurationMin: 4, GasMixJSON: air, SegmentType: "descent"},
		{PlanID: plan.ID, SequenceNo: 2, DepthM: 30, DurationMin: 20, GasMixJSON: air, SegmentType: "bottom"},
		{PlanID: plan.ID, SequenceNo: 3, DepthM: 0, DurationMin: 3, AscentRateMMin: 10, GasMixJSON: air, SegmentType: "ascent"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create segments: %v", err)
	}
	return &freshnessHarness{
		db: db, plans: plans, segments: segments, assess: assess, profiles: profiles,
		service:    NewDecompressionAssessmentService(assess, plans, profiles, segments, "training-compartment-v1", 20),
		segmentSvc: NewExposureSegmentService(segments, plans, 20),
		planID:     plan.ID,
	}
}

func testActor() audit.Entry {
	return audit.Entry{RequestID: "req-svc", ActorID: 7, ActorUsername: "planner"}
}

func transitionReq(target constants.PlanStatus, version uint, reason string) dto.TransitionPlanRequest {
	return dto.TransitionPlanRequest{TargetStatus: target, Version: version, Reason: reason}
}

// Full lifecycle: run -> submit -> inputs change expires the result ->
// submit blocked until a fresh run supersedes it.
func TestStaleAssessmentBlockedFromReview(t *testing.T) {
	h := newFreshnessHarness(t)
	ctx := context.Background()

	run, err := h.service.Run(ctx, h.planID, dto.RunAssessmentRequest{PlanVersion: 1}, testActor())
	if err != nil {
		t.Fatalf("run model: %v", err)
	}
	if run.InputVersion != 1 || run.PlanInputVersion != 2 || run.Stale {
		t.Fatalf("fresh run metadata = input %d current %d stale %t", run.InputVersion, run.PlanInputVersion, run.Stale)
	}

	submitted, err := h.service.Submit(ctx, run.ID, transitionReq(constants.PlanPendingReview, 2, "please review"), testActor())
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if submitted.AssessmentStatus != string(constants.PlanPendingReview) {
		t.Fatalf("status = %s", submitted.AssessmentStatus)
	}

	// Supervisor sends it back with a mandatory reason.
	returned, err := h.service.Return(ctx, run.ID, transitionReq(constants.PlanDraft, 3, "reduce bottom time"), audit.Entry{RequestID: "req-svc", ActorID: 9, ActorUsername: "supervisor"})
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if returned.AssessmentStatus != string(constants.AssessmentReturned) || returned.ReturnReason != "reduce bottom time" {
		t.Fatalf("return result = %s reason %q", returned.AssessmentStatus, returned.ReturnReason)
	}

	// Planner changes a segment depth: version advances and the old run expires.
	updateReq := dto.UpdateExposureSegmentRequest{PlanVersion: 4, DepthM: 25, DurationMin: 20, AscentRateMMin: 0, GasMix: decompression.GasMix{O2: 0.21, N2: 0.79}, SegmentType: "bottom", Notes: ""}
	var firstBottom model.ExposureSegment
	if err := h.db.Where("plan_id = ? AND sequence_no = ?", h.planID, 2).First(&firstBottom).Error; err != nil {
		t.Fatalf("load bottom segment: %v", err)
	}
	if _, err := h.segmentSvc.Update(ctx, firstBottom.ID, updateReq, testActor()); err != nil {
		t.Fatalf("update segment: %v", err)
	}

	stale, err := h.service.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}
	if !stale.Stale || stale.InputVersion != 1 || stale.PlanInputVersion != 5 {
		t.Fatalf("expected stale run tied to input v1 against plan v5, got stale %t input %d current %d", stale.Stale, stale.InputVersion, stale.PlanInputVersion)
	}

	// Submitting the expired result is rejected even though plan is draft.
	if _, err := h.service.Submit(ctx, run.ID, transitionReq(constants.PlanPendingReview, 5, "try again"), testActor()); err == nil {
		t.Fatal("expected stale submit to be rejected")
	}

	// Rerun against current inputs produces a fresh, submittable result.
	fresh, err := h.service.Run(ctx, h.planID, dto.RunAssessmentRequest{PlanVersion: 5}, testActor())
	if err != nil {
		t.Fatalf("rerun model: %v", err)
	}
	if fresh.Stale || fresh.InputVersion != 5 {
		t.Fatalf("fresh run stale %t input %d", fresh.Stale, fresh.InputVersion)
	}
	if _, err := h.service.Submit(ctx, fresh.ID, transitionReq(constants.PlanPendingReview, 6, "updated inputs"), testActor()); err != nil {
		t.Fatalf("submit fresh: %v", err)
	}
}

// A supervisor cannot approve an expired result.
func TestStaleAssessmentBlockedFromApproval(t *testing.T) {
	h := newFreshnessHarness(t)
	ctx := context.Background()

	run, err := h.service.Run(ctx, h.planID, dto.RunAssessmentRequest{PlanVersion: 1}, testActor())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := h.service.Submit(ctx, run.ID, transitionReq(constants.PlanPendingReview, 2, "review please"), testActor()); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Planner revision is not allowed while pending; first return to draft,
	// then mutate inputs to expire the result.
	if _, err := h.service.Return(ctx, run.ID, transitionReq(constants.PlanDraft, 3, "needs change"), audit.Entry{ActorID: 9, ActorUsername: "supervisor"}); err != nil {
		t.Fatalf("return: %v", err)
	}
	var segment model.ExposureSegment
	if err := h.db.Where("plan_id = ? AND sequence_no = ?", h.planID, 1).First(&segment).Error; err != nil {
		t.Fatalf("load segment: %v", err)
	}
	req := dto.UpdateExposureSegmentRequest{PlanVersion: 4, DepthM: 28, DurationMin: 4, AscentRateMMin: 0, GasMix: decompression.GasMix{O2: 0.21, N2: 0.79}, SegmentType: "descent"}
	if _, err := h.segmentSvc.Update(ctx, segment.ID, req, testActor()); err != nil {
		t.Fatalf("update: %v", err)
	}
	// Force the expired assessment back to a pending-looking state to prove the
	// stale guard itself rejects approval independent of state checks.
	if err := h.db.Model(&model.DecompressionAssessment{}).Where("id = ?", run.ID).Update("assessment_status", constants.AssessmentPending).Error; err != nil {
		t.Fatalf("force pending: %v", err)
	}
	if err := h.db.Model(&model.DivePlan{}).Where("id = ?", h.planID).Updates(map[string]any{"plan_status": constants.PlanPendingReview, "version": 5}).Error; err != nil {
		t.Fatalf("force plan pending: %v", err)
	}
	_, err = h.service.Approve(ctx, run.ID, transitionReq(constants.PlanApprovedTraining, 5, "approve anyway"), audit.Entry{ActorID: 9, ActorUsername: "supervisor"})
	if err == nil {
		t.Fatal("expected stale approval to be rejected")
	}
	var appErr *util.AppError
	if !errors.As(err, &appErr) || appErr.Code != "ASSESSMENT_STALE" {
		t.Fatalf("expected ASSESSMENT_STALE, got %v", err)
	}
}
