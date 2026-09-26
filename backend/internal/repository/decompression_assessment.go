package repository

import (
	"context"
	"fmt"
	"time"

	"commercial-diving-decompression-control/backend/internal/audit"
	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/model"
	"commercial-diving-decompression-control/backend/internal/util"
	"gorm.io/gorm"
)

type DecompressionAssessmentRepository struct {
	db    *gorm.DB
	audit *audit.Repository
}

func NewDecompressionAssessmentRepository(db *gorm.DB, auditRepo *audit.Repository) *DecompressionAssessmentRepository {
	return &DecompressionAssessmentRepository{db: db, audit: auditRepo}
}

func (r *DecompressionAssessmentRepository) List(ctx context.Context, planID uint, status string, stale *bool, page, size int) ([]model.DecompressionAssessment, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.DecompressionAssessment{})
	if planID > 0 {
		query = query.Where("plan_id = ?", planID)
	}
	if status != "" {
		query = query.Where("assessment_status = ?", status)
	}
	if stale != nil {
		query = query.Where("stale = ?", *stale)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count assessments: %w", err)
	}
	var items []model.DecompressionAssessment
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list assessments: %w", err)
	}
	return items, total, nil
}

func (r *DecompressionAssessmentRepository) Get(ctx context.Context, id uint) (model.DecompressionAssessment, error) {
	var item model.DecompressionAssessment
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return model.DecompressionAssessment{}, fmt.Errorf("get assessment %d: %w", id, err)
	}
	return item, nil
}

func (r *DecompressionAssessmentRepository) LatestByPlan(ctx context.Context, planID uint) (model.DecompressionAssessment, error) {
	var item model.DecompressionAssessment
	if err := r.db.WithContext(ctx).Where("plan_id = ?", planID).Order("created_at DESC, id DESC").First(&item).Error; err != nil {
		return model.DecompressionAssessment{}, fmt.Errorf("get latest assessment for plan %d: %w", planID, err)
	}
	return item, nil
}

func (r *DecompressionAssessmentRepository) CreateModeled(ctx context.Context, plan model.DivePlan, item *model.DecompressionAssessment, entry audit.Entry) error {
	item.InputVersion = plan.Version
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.DivePlan{}).Where("id = ? AND version = ? AND plan_status = ?", plan.ID, plan.Version, constants.PlanDraft).Updates(map[string]any{"plan_status": constants.PlanModeled, "version": gorm.Expr("version + 1")})
		if result.Error != nil {
			return fmt.Errorf("mark plan modeled: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return util.Conflict("PLAN_VERSION_CONFLICT", "plan must remain at the requested draft version", nil)
		}
		if err := tx.Create(item).Error; err != nil {
			return fmt.Errorf("create immutable assessment: %w", err)
		}
		entry.EntityID = item.ID
		if err := r.audit.RecordWithDB(ctx, tx, entry); err != nil {
			return err
		}
		planEntry := entry
		planEntry.Action = "dive_plan.transition"
		planEntry.EntityType = "dive_plan"
		planEntry.EntityID = plan.ID
		planEntry.BeforeSummary = string(constants.PlanDraft)
		planEntry.AfterSummary = fmt.Sprintf("%s assessment=%d", constants.PlanModeled, item.ID)
		return r.audit.RecordWithDB(ctx, tx, planEntry)
	})
	if err != nil {
		return fmt.Errorf("create modeled assessment transaction: %w", err)
	}
	return nil
}

func (r *DecompressionAssessmentRepository) Transition(ctx context.Context, plan model.DivePlan, assessment model.DecompressionAssessment, target constants.PlanStatus, actorID uint, entry audit.Entry) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		planChanges := map[string]any{"plan_status": target, "version": gorm.Expr("version + 1")}
		assessmentChanges := map[string]any{"assessment_status": string(target)}
		if target == constants.PlanApprovedTraining {
			now := time.Now().UTC()
			planChanges["reviewed_by"] = actorID
			assessmentChanges["reviewed_at"] = now
		}
		planResult := tx.Model(&model.DivePlan{}).Where("id = ? AND version = ? AND plan_status = ?", plan.ID, plan.Version, plan.PlanStatus).Updates(planChanges)
		if planResult.Error != nil {
			return fmt.Errorf("transition assessment plan: %w", planResult.Error)
		}
		if planResult.RowsAffected != 1 {
			return util.Conflict("PLAN_VERSION_CONFLICT", "plan state or version changed concurrently", nil)
		}
		assessmentResult := tx.Model(&model.DecompressionAssessment{}).Where("id = ? AND assessment_status = ?", assessment.ID, assessment.AssessmentStatus).Updates(assessmentChanges)
		if assessmentResult.Error != nil {
			return fmt.Errorf("transition assessment metadata: %w", assessmentResult.Error)
		}
		if assessmentResult.RowsAffected != 1 {
			return util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment review state changed concurrently", nil)
		}
		entry.EntityID = assessment.ID
		if err := r.audit.RecordWithDB(ctx, tx, entry); err != nil {
			return err
		}
		planEntry := entry
		planEntry.EntityType = "dive_plan"
		planEntry.EntityID = plan.ID
		planEntry.Action = "dive_plan.transition"
		return r.audit.RecordWithDB(ctx, tx, planEntry)
	})
	if err != nil {
		return fmt.Errorf("transition assessment transaction: %w", err)
	}
	return nil
}

// BackToDraft moves a plan out of its review state together with the
// assessment under review. assessmentTarget distinguishes a planner-driven
// revision (modeled -> draft) from a supervisor return (pending -> draft);
// returnReason is stored on the assessment for supervisor returns.
func (r *DecompressionAssessmentRepository) BackToDraft(ctx context.Context, plan model.DivePlan, assessment model.DecompressionAssessment, assessmentTarget constants.AssessmentStatus, returnReason string, entry audit.Entry) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		planChanges := map[string]any{"plan_status": constants.PlanDraft, "version": gorm.Expr("version + 1")}
		assessmentChanges := map[string]any{"assessment_status": string(assessmentTarget)}
		if assessmentTarget == constants.AssessmentReturned {
			assessmentChanges["return_reason"] = returnReason
		}
		planResult := tx.Model(&model.DivePlan{}).Where("id = ? AND version = ? AND plan_status = ?", plan.ID, plan.Version, plan.PlanStatus).Updates(planChanges)
		if planResult.Error != nil {
			return fmt.Errorf("return plan to draft: %w", planResult.Error)
		}
		if planResult.RowsAffected != 1 {
			return util.Conflict("PLAN_VERSION_CONFLICT", "plan state or version changed concurrently", nil)
		}
		assessmentResult := tx.Model(&model.DecompressionAssessment{}).Where("id = ? AND assessment_status = ?", assessment.ID, assessment.AssessmentStatus).Updates(assessmentChanges)
		if assessmentResult.Error != nil {
			return fmt.Errorf("mark assessment %s: %w", assessmentTarget, assessmentResult.Error)
		}
		if assessmentResult.RowsAffected != 1 {
			return util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment review state changed concurrently", nil)
		}
		entry.EntityID = assessment.ID
		if err := r.audit.RecordWithDB(ctx, tx, entry); err != nil {
			return err
		}
		planEntry := entry
		planEntry.Action = "dive_plan.transition"
		planEntry.EntityType = "dive_plan"
		planEntry.EntityID = plan.ID
		return r.audit.RecordWithDB(ctx, tx, planEntry)
	})
	if err != nil {
		return fmt.Errorf("return plan to draft transaction: %w", err)
	}
	return nil
}

// MarkStaleForInputChange marks every review-relevant assessment of a plan as
// stale when its inputs move to a new version. Results under review, results
// awaiting a planner rerun, and returned results all expire; approved and
// archived evidence is immutable. Already-stale rows are left untouched and
// generate no duplicate audit events. It must run inside the same transaction
// as the input change.
func (r *DecompressionAssessmentRepository) MarkStaleForInputChange(tx *gorm.DB, ctx context.Context, planID, fromVersion, toVersion uint, entry audit.Entry) error {
	var fresh []model.DecompressionAssessment
	if err := tx.WithContext(ctx).Where(
		"plan_id = ? AND stale = ? AND assessment_status IN ?",
		planID, false,
		[]string{string(constants.AssessmentModeled), string(constants.AssessmentPending), string(constants.AssessmentReturned)},
	).Find(&fresh).Error; err != nil {
		return fmt.Errorf("load assessments to expire for plan %d: %w", planID, err)
	}
	for _, assessment := range fresh {
		if err := tx.Model(&model.DecompressionAssessment{}).Where("id = ? AND stale = ?", assessment.ID, false).Update("stale", true).Error; err != nil {
			return fmt.Errorf("expire assessment %d: %w", assessment.ID, err)
		}
		staleEntry := entry
		staleEntry.Action = "decompression_assessment.stale"
		staleEntry.EntityType = "decompression_assessment"
		staleEntry.EntityID = assessment.ID
		staleEntry.BeforeSummary = fmt.Sprintf("input_version=%d stale=false", fromVersion)
		staleEntry.AfterSummary = fmt.Sprintf("input_version=%d stale=true assessment=%d", toVersion, assessment.ID)
		if err := r.audit.RecordWithDB(ctx, tx, staleEntry); err != nil {
			return err
		}
	}
	return nil
}
