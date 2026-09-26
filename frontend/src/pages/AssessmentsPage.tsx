import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, MenuItem, TextField, Tooltip } from '@mui/material'
import { ArrowRight, CheckCheck, GitCompareArrows, History, Play, RotateCcw, Send, ShieldAlert, Undo2 } from 'lucide-react'
import { AssumptionPanel } from '@/components/common/AssumptionPanel'
import { PageHeader } from '@/components/common/PageHeader'
import { PlanStatusBadge } from '@/components/common/PlanStatusBadge'
import { StaleBadge } from '@/components/common/StaleBadge'
import { getPlan } from '@/api/plan'
import { useAuth } from '@/hooks/useAuth'
import { useAssessmentPolling } from '@/hooks/useAssessmentPolling'
import { useAssessmentStore } from '@/stores/assessment'
import { usePlanStore } from '@/stores/plan'
import type { DecompressionAssessment } from '@/types/assessment'

type ReviewAction = 'submit' | 'approve' | 'revise' | 'return'

export function AssessmentsPage() {
  const { isPlanner, isSupervisor } = useAuth()
  const plans = usePlanStore()
  const assessments = useAssessmentStore()
  const [compareId, setCompareId] = useState<number>(0)
  const [reason, setReason] = useState('Reviewed training assumptions and versioned model evidence.')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [localError, setLocalError] = useState<string | null>(null)
  useEffect(() => { void assessments.load(); void plans.load() }, [assessments.load, plans.load])
  useAssessmentPolling(true)
  const selected = assessments.selected
  const selectedPlan = useMemo(() => plans.items.find((plan) => plan.id === selected?.plan_id), [plans.items, selected?.plan_id])

  const act = async (kind: ReviewAction) => {
    if (!selected) return
    setBusy(true); setLocalError(null); setNotice(null)
    try {
      const plan = await getPlan(selected.plan_id)
      const targetDraft = kind === 'revise' || kind === 'return'
      if (kind === 'submit') await assessments.submit(selected.id, plan.version, reason)
      else if (kind === 'approve') await assessments.approve(selected.id, plan.version, reason)
      else if (kind === 'revise') await assessments.revise(selected.id, plan.version, reason)
      else await assessments.return(selected.id, plan.version, reason)
      await plans.load()
      if (targetDraft) setNotice(kind === 'return' ? 'Plan returned to the planner; the return reason is attached to this result.' : 'Plan pulled back to draft for new inputs.')
      else if (kind === 'submit') setNotice('Assessment submitted for human supervisor review.')
      else setNotice('Assessment approved for training comparison; no operational clearance was issued.')
    } catch (error) { setLocalError(error instanceof Error ? error.message : 'Review action failed') }
    finally { setBusy(false) }
  }

  const rerun = async (item: DecompressionAssessment) => {
    setBusy(true); setLocalError(null); setNotice(null)
    try {
      const plan = await getPlan(item.plan_id)
      if (plan.plan_status !== 'draft') {
        setLocalError('The plan must be in draft before the model can run against the current inputs.')
        return
      }
      const result = await assessments.run(item.plan_id, plan.version)
      await plans.load()
      setNotice(`New assessment #${result.id} produced from current inputs; the expired result remains available for inspection.`)
    } catch (error) { setLocalError(error instanceof Error ? error.message : 'Model run failed') }
    finally { setBusy(false) }
  }

  const canSubmit = isPlanner && selected?.plan_status === 'modeled' && selected.assessment_status === 'modeled'
  const canApprove = isSupervisor && selected?.plan_status === 'pending_supervisor_review' && selected.assessment_status === 'pending_supervisor_review'
  const canRevise = isPlanner && selected?.plan_status === 'modeled' && selected.assessment_status === 'modeled'
  const canReturn = isSupervisor && selected?.plan_status === 'pending_supervisor_review' && selected.assessment_status === 'pending_supervisor_review'
  const canRerun = isPlanner && selected?.plan_status === 'draft'

  return (
    <div className="page">
      <PageHeader eyebrow="IMMUTABLE MODEL RUNS" title="Assessment review" detail="Compare fixed snapshots, inspect risk evidence, and record explicit human decisions." />
      {(assessments.error || plans.error || localError) && <Alert severity="error">{assessments.error ?? plans.error ?? localError}</Alert>}
      {notice && <Alert severity="success" onClose={() => setNotice(null)}>{notice}</Alert>}
      <div className="assessment-layout">
        <section className="assessment-queue">
          <div className="list-heading"><span>{assessments.items.length} RUNS</span><span>INPUT / INDEX</span></div>
          {assessments.items.map((item) => (
            <button key={item.id} className={`assessment-row ${selected?.id === item.id ? 'selected' : ''} ${item.stale ? 'assessment-stale-row' : ''}`} onClick={() => void assessments.select(item.id)}>
              <div><strong>#{item.id} · {plans.items.find((plan) => plan.id === item.plan_id)?.plan_code ?? `Plan ${item.plan_id}`}</strong><span>{item.algorithm_version} · input v{item.input_version}{item.plan_input_version !== item.input_version ? ` / plan v${item.plan_input_version}` : ''}</span></div>
              <div className="assessment-row-badges">
                {item.stale ? <StaleBadge stale inputVersion={item.input_version} currentVersion={item.plan_input_version} /> : <PlanStatusBadge status={item.assessment_status} />}
                <b>{item.comparative_score.toFixed(1)}</b>
              </div>
            </button>
          ))}
          {!assessments.items.length && <div className="empty-state">No immutable assessments recorded.</div>}
        </section>
        <section className="assessment-detail">
          {selected ? <>
            <div className="assessment-title">
              <div>
                <span className="eyebrow">ASSESSMENT #{selected.id}</span>
                <h2>{selectedPlan?.plan_code ?? `Plan ${selected.plan_id}`}</h2>
                <p>Created {new Date(selected.created_at).toLocaleString()} · input snapshot preserved at v{selected.input_version}</p>
              </div>
              <div className="score-dial"><span>COMPARATIVE INDEX</span><strong>{selected.comparative_score.toFixed(1)}</strong><small>{selected.highest_risk_band} · not a safety score</small></div>
            </div>

            <div className="freshness-bar">
              {selected.stale
                ? <StaleBadge stale inputVersion={selected.input_version} currentVersion={selected.plan_input_version} />
                : <StaleBadge stale={false} inputVersion={selected.input_version} currentVersion={selected.plan_input_version} />}
              <PlanStatusBadge status={selected.assessment_status} />
              <span className="freshness-note">
                This run was produced from <strong>input version {selected.input_version}</strong>;
                {selected.stale
                  ? <> the plan inputs are now at <strong>version {selected.plan_input_version}</strong>. The result is kept for inspection but cannot be submitted or approved.</>
                  : <> the plan inputs are unchanged.</>}
              </span>
            </div>

            {selected.stale && (
              <Alert severity="warning" icon={<History size={20} />} className="stale-alert">
                Expired result: depth, duration, gas, or ordering inputs changed after this run. Re-run the model against the current inputs (v{selected.plan_input_version}) before review can continue.
                {canRerun && <Button size="small" variant="contained" startIcon={<Play size={15} />} disabled={busy} onClick={() => void rerun(selected)} sx={{ ml: 2 }}>Run model on current inputs</Button>}
              </Alert>
            )}
            {selected.assessment_status === 'returned' && selected.return_reason && (
              <Alert severity="info" icon={<RotateCcw size={18} />} className="return-alert">
                <strong>Supervisor return reason:</strong> {selected.return_reason}
              </Alert>
            )}

            <div className="review-bar">
              <TextField label="Review / revision reason" value={reason} onChange={(event) => setReason(event.target.value)} fullWidth />
              {canSubmit && (
                <Tooltip title={selected.stale ? 'Expired results cannot be submitted' : ''}><span>
                  <Button variant="contained" startIcon={<Send size={17} />} disabled={busy || reason.trim().length < 3 || selected.stale} onClick={() => void act('submit')}>Submit</Button>
                </span></Tooltip>
              )}
              {canRevise && (
                <Tooltip title="Pull back to draft to change depth or duration"><span>
                  <Button variant="outlined" startIcon={<Undo2 size={16} />} disabled={busy || reason.trim().length < 3} onClick={() => void act('revise')}>Revise to draft</Button>
                </span></Tooltip>
              )}
              {canApprove && (
                <Tooltip title={selected.stale ? 'Expired results cannot be approved' : ''}><span>
                  <Button variant="contained" color="secondary" startIcon={<CheckCheck size={17} />} disabled={busy || reason.trim().length < 3 || selected.stale} onClick={() => void act('approve')}>Approve training</Button>
                </span></Tooltip>
              )}
              {canReturn && (
                <Button variant="outlined" color="warning" startIcon={<RotateCcw size={16} />} disabled={busy || reason.trim().length < 3} onClick={() => void act('return')}>Return to planner</Button>
              )}
            </div>
            {selected.plan_status === 'draft' && isPlanner && selected.stale && (
              <div className="rerun-bar"><History size={16} /><span>Review is blocked until a new run replaces this expired evidence.</span><Button size="small" variant="contained" startIcon={<Play size={15} />} disabled={busy} onClick={() => void rerun(selected)}>Run model on v{selected.plan_input_version} inputs</Button></div>
            )}
            <section className="risk-section"><div className="subheading">Risk evidence <span>{selected.risk_flags.length}</span></div><div className="risk-list">{selected.risk_flags.map((flag) => <article className={`risk-row risk-${flag.band}`} key={flag.code}><ShieldAlert size={18} /><div><strong>{flag.code.replaceAll('_', ' ')}</strong><p>{flag.message}</p><small>{flag.evidence}</small></div><span>{flag.band}</span></article>)}</div></section>
            <div className="compartment-grid">{selected.compartment_loads.map((curve) => { const last = curve.points.at(-1); return <div key={curve.name}><span>{curve.name}</span><strong>{last?.total_inert_bar.toFixed(3)} bar</strong><small>N2 t½ {curve.n2_half_time_min} · He t½ {curve.he_half_time_min}</small></div> })}</div>
            <AssumptionPanel assumptions={selected.assumptions} />
            <section className="compare-panel"><div className="section-title"><GitCompareArrows size={18} /><div><strong>Compare immutable runs</strong><span>Difference is descriptive, not relative safety</span></div></div><TextField select label="Other assessment" value={compareId || ''} onChange={(event) => setCompareId(Number(event.target.value))} sx={{ minWidth: 220 }}>{assessments.items.filter((item) => item.id !== selected.id).map((item) => <MenuItem key={item.id} value={item.id}>#{item.id} · input v{item.input_version} · index {item.comparative_score.toFixed(1)}{item.stale ? ' · expired' : ''}</MenuItem>)}</TextField><Button startIcon={<ArrowRight size={16} />} disabled={!compareId} onClick={() => void assessments.compare(selected.id, compareId)}>Compare</Button>{assessments.comparison && <div className="comparison-result"><strong>{assessments.comparison.score_delta >= 0 ? '+' : ''}{assessments.comparison.score_delta.toFixed(2)} index</strong><span>{assessments.comparison.flag_delta >= 0 ? '+' : ''}{assessments.comparison.flag_delta} flags</span><p>{assessments.comparison.summary.join(' ')}</p></div>}</section>
            <p className="disclaimer-line">{selected.safety_disclaimer}</p>
          </> : <div className="empty-state">Select an assessment to inspect its immutable evidence.</div>}
        </section>
      </div>
    </div>
  )
}
