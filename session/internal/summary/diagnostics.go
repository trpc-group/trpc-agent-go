//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/internal/summarydiag"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Session summary outcomes are stable diagnostic values. They reuse the
// synchronous context-compaction vocabulary where the same failure class
// applies and add the outcomes that only exist for stored summaries.
const (
	// outcomeSuccess reports a newly generated summary that the backend
	// confirmed stored.
	outcomeSuccess = "success"
	// outcomeCopied reports an existing summary that a cascade materialized
	// for this target and persisted without a summary model call.
	outcomeCopied = "copied"
	// outcomeStaleWrite reports a generated summary that the backend refused
	// to store because a newer summary was already persisted. This is a
	// successful set-if-newer no-op and can happen under normal concurrency.
	outcomeStaleWrite = "stale_write"
	// outcomeNotStored reports a generated summary that the backend neither
	// stored nor rejected, for example because the session row was missing.
	outcomeNotStored = "not_stored"
	// outcomeNoUpdate reports a triggered attempt whose summary model call
	// produced no summary text.
	outcomeNoUpdate = "no_update"
	// outcomeNoDelta reports that no event was appended after the recorded
	// summary boundary.
	outcomeNoDelta = "no_delta"
	// outcomeBelowThreshold reports that a configured trigger check ran and
	// did not fire.
	outcomeBelowThreshold = "below_threshold"
	// outcomeCascadeSuppressed reports a full-session target that was skipped
	// because it was only requested as a branch cascade.
	outcomeCascadeSuppressed = "cascade_suppressed"
	// outcomeNoContent reports that the built-in summarizer published a
	// trigger observation with no eligible content, so the model was never
	// called.
	outcomeNoContent = "no_content"
	// outcomeUnobserved reports that this attempt's gate did not fire and no
	// trigger observation was published for this attempt. That is diagnostic
	// uncertainty, not proof of missing content or a failed threshold check.
	outcomeUnobserved = "unobserved"
	// outcomeUnsafeView reports outcomeNoContent caused by a model-visible
	// view that exists but is not bound to the request the model saw.
	outcomeUnsafeView = "unsafe_view"
	// outcomeSummaryError reports a failed summary model call.
	outcomeSummaryError = "summary_error"
	// outcomeContextError reports a canceled or expired summary context.
	outcomeContextError = "context_error"
	// outcomePersistenceError reports a generated summary that could not be
	// persisted by the backend.
	outcomePersistenceError = "persistence_error"
	// outcomeUnknownWrite reports a generated summary whose backend write
	// completed without error, but the set-if-newer reply could not be
	// classified as stored or stale. This is diagnostic uncertainty, not a
	// business failure.
	outcomeUnknownWrite = "unknown_write"
)

// Summary targets are reported by kind so diagnostics stay low cardinality
// even when the raw filter key is not usable for aggregation.
const (
	targetKindFull   = "full"
	targetKindBranch = "branch"
)

// Dispatch values report how the attempt reached the session backend.
const (
	// dispatchAsync marks an attempt dispatched through the asynchronous
	// summary pipeline, including its synchronous queue-full fallback.
	dispatchAsync = "async"
	// dispatchRequest marks an attempt made directly while building a model
	// request, for example synchronous context compaction.
	dispatchRequest = "request"
)

// Call modes that diagnostics can prove. Any other mode, including an unset
// Report, is unobserved rather than reported as a negative model call.
const (
	callModeStandalone     = "standalone"
	callModeCacheSafeFork  = "cache_safe_fork"
	callModeCustomResponse = "custom_response"

	modelCallStatusCalled         = "called"
	modelCallStatusCustomResponse = "custom_response"
	modelCallStatusUnobserved     = "unobserved"
)

// PersistResult reports what a backend persistence stage actually did. Values
// are stable and low cardinality so operators can separate a backend-confirmed
// store from a deliberate no-op.
type PersistResult string

const (
	// PersistNotAttempted means no persistence stage ran for this attempt.
	// It is the zero value.
	PersistNotAttempted PersistResult = "not_attempted"
	// PersistNoSummary means summarization reported an update but left no
	// in-memory summary for the target filter key.
	PersistNoSummary PersistResult = "no_summary"
	// PersistStored means the backend confirmed the write.
	PersistStored PersistResult = "stored"
	// PersistStale means the backend deliberately skipped the write because a
	// newer summary is already persisted.
	PersistStale PersistResult = "stale"
	// PersistUnknown means the backend write finished without error, but the
	// reply could not be classified as stored or stale.
	PersistUnknown PersistResult = "unknown"
	// PersistError means the backend write failed.
	PersistError PersistResult = "error"
)

// Attempt accumulates diagnostics for one session summary target and reports
// exactly one record when the attempt completes.
//
// An Attempt is owned by the single goroutine running the attempt; cascade
// forks create one attempt per target. All methods tolerate a nil receiver so
// callers never need to branch on diagnostics being available.
//
// Diagnostics never include summary, prompt, event, or raw error text.
type Attempt struct {
	// ctx is the reporting context for this attempt. The Attempt owns it for
	// the duration of one CreateSessionSummary call.
	ctx        context.Context
	startedAt  time.Time
	sess       *session.Session
	filterKey  string
	before     summaryBoundaryMark
	selection  isummarycontext.EventSelection
	modelCall  isummarycontext.ModelCall
	triggerObs isummarycontext.TriggerObservation

	gate         summaryGate
	skip         string
	triggered    bool
	updated      bool
	copied       bool
	summarizeErr error
	persist      PersistResult
	persistErr   error
}

// BeginAttempt starts diagnostics for one session summary target. The returned
// context must be used for summarization so trigger, model-call, and event
// selection details are observed. Callers report the attempt exactly once,
// normally with defer.
func BeginAttempt(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
) (context.Context, *Attempt) {
	att := &Attempt{
		startedAt: time.Now(),
		sess:      sess,
		filterKey: filterKey,
		before:    markSummaryBoundary(sess, filterKey),
		// Until a summarizer publishes its selection, nothing is known about
		// how much history would have reached the summary model.
		selection: isummarycontext.UnknownSelection("", isummarycontext.ReasonNone),
		persist:   PersistNotAttempted,
	}
	ctx = contextWithSummaryAttempt(ctx, att)
	_, ctx = ensureSummaryReport(ctx)
	ctx = isummarycontext.WithEventSelectionRecorder(ctx, &att.selection)
	ctx = isummarycontext.WithModelCallRecorder(ctx, &att.modelCall)
	ctx = isummarycontext.WithTriggerRecorder(ctx, &att.triggerObs)
	att.ctx = ctx
	return ctx, att
}

// Summarized records the result of the summarization stage. Backends call it
// with the values returned by SummarizeSession, before applying their own
// error contract.
func (a *Attempt) Summarized(updated bool, err error) {
	if a == nil {
		return
	}
	a.updated = updated
	a.summarizeErr = err
}

// Persisted records what the backend persistence stage did.
func (a *Attempt) Persisted(result PersistResult) {
	if a == nil {
		return
	}
	a.persist = result
}

// RecordWrite records a completed backend write and returns err unchanged so
// backends can report and return in one statement.
func (a *Attempt) RecordWrite(err error) error {
	if a == nil {
		return err
	}
	if err != nil {
		a.persist = PersistError
		a.persistErr = err
		return err
	}
	a.persist = PersistStored
	return nil
}

type summaryAttemptContextKey struct{}
type summaryDispatchContextKey struct{}

func contextWithSummaryAttempt(
	ctx context.Context,
	att *Attempt,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, summaryAttemptContextKey{}, att)
}

func summaryAttemptFromContext(ctx context.Context) *Attempt {
	if ctx == nil {
		return nil
	}
	att, _ := ctx.Value(summaryAttemptContextKey{}).(*Attempt)
	return att
}

// contextWithAsyncSummaryDispatch marks work dispatched through the
// asynchronous summary pipeline so diagnostics can separate it from summaries
// refreshed while a model request is being built.
func contextWithAsyncSummaryDispatch(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, summaryDispatchContextKey{}, dispatchAsync)
}

func summaryDispatch(ctx context.Context) string {
	if ctx == nil {
		return dispatchRequest
	}
	dispatch, ok := ctx.Value(summaryDispatchContextKey{}).(string)
	if !ok || dispatch == "" {
		return dispatchRequest
	}
	return dispatch
}

// recordSummarySkip records the stable gate outcome that stopped generation.
func recordSummarySkip(ctx context.Context, skip string) {
	if att := summaryAttemptFromContext(ctx); att != nil {
		att.skip = skip
	}
}

// summaryGate is the scalar part of a trigger decision. It excludes the
// per-check slice so diagnostics keep a bounded, low-cardinality shape.
type summaryGate struct {
	name           string
	metric         string
	value          int
	threshold      int
	contextWindow  int
	thresholdRatio float64
}

// recordAttemptGate records this attempt's gate from the attempt-local
// trigger observation. A leftover caller Report.Trigger is never read here.
// An unpublished observation cannot be classified as no_content or
// below_threshold.
func recordAttemptGate(ctx context.Context, fired bool) {
	att := summaryAttemptFromContext(ctx)
	if att == nil {
		return
	}
	att.triggered = fired
	obs := isummarycontext.TriggerFromContext(ctx)
	if obs == nil || !obs.Published {
		if fired {
			att.skip = ""
			return
		}
		att.skip = outcomeUnobserved
		return
	}
	att.gate = summaryGate{
		name:           obs.Name,
		metric:         obs.Metric,
		value:          obs.Value,
		threshold:      obs.Threshold,
		contextWindow:  obs.ContextWindow,
		thresholdRatio: obs.ThresholdRatio,
	}
	if fired {
		att.skip = ""
		return
	}
	if obs.Name == "" && obs.Metric == "" && obs.CheckCount == 0 {
		att.skip = outcomeNoContent
		return
	}
	att.skip = outcomeBelowThreshold
}

// recordPublishedTrigger publishes an attempt-local trigger observation and
// records the gate from that observation. It does not mutate a caller Report.
func recordPublishedTrigger(
	ctx context.Context,
	fired bool,
	trigger summary.Trigger,
) {
	isummarycontext.RecordTrigger(ctx, triggerObservation(trigger))
	recordAttemptGate(ctx, fired)
}

func triggerObservation(trigger summary.Trigger) isummarycontext.TriggerObservation {
	return isummarycontext.TriggerObservation{
		Name:           trigger.Name,
		Metric:         trigger.Metric,
		Value:          trigger.Value,
		Threshold:      trigger.Threshold,
		ContextWindow:  trigger.ContextWindow,
		CheckCount:     len(trigger.Checks),
		ThresholdRatio: trigger.ThresholdRatio,
	}
}

// recordSummaryCopied records that an existing summary was materialized for
// this target instead of being generated.
func recordSummaryCopied(ctx context.Context) {
	if att := summaryAttemptFromContext(ctx); att != nil {
		att.copied = true
	}
}

// ensureSummaryReport guarantees that a summary report is observable for this
// attempt. Callers that already attached a report keep it, so hook behavior
// and forked cascade reports are unchanged.
func ensureSummaryReport(
	ctx context.Context,
) (*summary.Report, context.Context) {
	if report, ok := summary.ReportFromContext(ctx); ok {
		return report, ctx
	}
	report := &summary.Report{}
	return report, summary.ContextWithReport(ctx, report)
}

// Report emits the single diagnostic record for this attempt.
func (a *Attempt) Report() {
	if a == nil {
		return
	}
	ctx := a.ctx
	binding := summaryview.BindingFromContext(ctx)
	outcome := a.outcome(binding)
	after := markSummaryBoundary(a.sess, a.filterKey)
	filterKey, filterKeyTruncated := summarydiag.FormatFilterKey(a.filterKey)
	format := "Session summary result: schema_version=%d, outcome=%s, " +
		"dispatch=%s, target_kind=%s, filter_key=%q, " +
		"filter_key_truncated=%t, triggered=%t, trigger=%s, " +
		"trigger_metric=%s, trigger_value=%d, trigger_threshold=%d, " +
		"threshold_ratio=%.2f, context_window=%d, request_tokens=%d, " +
		"summary_view_present=%t, summary_view_bound=%t, binding_reason=%s, " +
		"summary_view_items=%d, input_source=%s, selection_reason=%s, " +
		"eligible_events=%d, skip_recent_requested=%d, " +
		"skip_recent_applied=%d, selected_events=%d, model_call_status=%s, " +
		"updated=%t, boundary_advanced=%t, persist_result=%s, duration_ms=%d"
	args := []any{
		summarydiag.SchemaVersion,
		outcome,
		summaryDispatch(ctx),
		summaryTargetKind(a.filterKey),
		filterKey,
		filterKeyTruncated,
		a.triggered,
		normalizedTriggerName(a.gate.name),
		normalizedTriggerMetric(a.gate.metric),
		a.gate.value,
		a.gate.threshold,
		a.gate.thresholdRatio,
		a.gate.contextWindow,
		binding.RequestTokens,
		binding.Present,
		binding.Bound,
		binding.Reason,
		binding.Items,
		diagnosticValue(a.selection.Source),
		diagnosticValue(a.selection.Reason),
		a.selection.Eligible,
		a.selection.SkipRecentRequested,
		a.selection.SkipRecentApplied,
		a.selection.Selected,
		a.modelCallStatus(),
		a.updated,
		after.advancedFrom(a.before),
		a.persist,
		time.Since(a.startedAt).Milliseconds(),
	}
	switch outcome {
	case outcomeSuccess:
		log.InfofContext(ctx, format, args...)
	case outcomeCopied, outcomeNoDelta, outcomeBelowThreshold,
		outcomeCascadeSuppressed, outcomeNoContent, outcomeUnobserved,
		outcomeStaleWrite, outcomeUnknownWrite:
		// Routine decisions that did no backend-confirmed write, including a
		// set-if-newer skip when a newer summary is already stored.
		log.DebugfContext(ctx, format, args...)
	default:
		log.WarnfContext(ctx, format, args...)
	}
}

// outcome classifies the attempt. Errors are classified by the stage that
// produced them: a non-context error from summarization is always a summary
// error, whether or not the summary model was reached, and
// model_call_status separates a failed model call from a pre-model build,
// a custom response, or an unobserved custom summarizer.
func (a *Attempt) outcome(binding summaryview.Binding) string {
	switch {
	case isSummaryContextError(a.summarizeErr) ||
		isSummaryContextError(a.persistErr):
		return outcomeContextError
	case a.persistErr != nil:
		return outcomePersistenceError
	case a.summarizeErr != nil:
		return outcomeSummaryError
	case a.persist == PersistStale:
		return outcomeStaleWrite
	case a.persist == PersistUnknown:
		// Classified before updated → success so an unobserved write is never
		// reported as a backend-confirmed store.
		return outcomeUnknownWrite
	case a.persist == PersistNoSummary:
		return outcomeNoUpdate
	case a.persist == PersistNotAttempted && a.updated:
		return outcomeNotStored
	case a.copied:
		return outcomeCopied
	case a.updated:
		return outcomeSuccess
	case a.skip != "":
		return unsafeViewOutcome(a.skip, binding)
	}
	return outcomeNoUpdate
}

// diagnosticValue keeps unset enum-like fields readable in log output.
func diagnosticValue(value string) string {
	if value == "" {
		return diagnosticNone
	}
	return value
}

// diagnosticNone marks an unset enum-like diagnostic field.
const diagnosticNone = "none"

// diagnosticCustom is the sentinel reported for a trigger name or metric that
// is not one of the framework's own values.
const diagnosticCustom = "custom"

// triggerNames and triggerMetrics are the framework's own trigger vocabulary.
// summary.Trigger carries exported string fields that any caller can populate
// through summary.ContextWithReport or a custom summary.SessionSummarizer, so
// diagnostics must not log these strings verbatim.
var (
	triggerNames = map[string]struct{}{
		"always":            {},
		"custom":            {},
		"event_threshold":   {},
		"time_threshold":    {},
		"token_threshold":   {},
		"context_threshold": {},
		"force":             {},
		"manual":            {},
	}
	triggerMetrics = map[string]struct{}{
		"custom":   {},
		"duration": {},
		"events":   {},
		"tokens":   {},
	}
)

// normalizedTriggerName reports a bounded trigger name. An unset name reports
// none and any name outside the framework's vocabulary reports custom, so an
// arbitrary caller-supplied string can never reach the log or inflate the
// cardinality of this field.
func normalizedTriggerName(name string) string {
	return normalizedTriggerValue(name, triggerNames)
}

// normalizedTriggerMetric reports a bounded trigger metric under the same rules
// as normalizedTriggerName.
func normalizedTriggerMetric(metric string) string {
	return normalizedTriggerValue(metric, triggerMetrics)
}

func normalizedTriggerValue(value string, known map[string]struct{}) string {
	if value == "" {
		return diagnosticNone
	}
	if _, ok := known[value]; ok {
		return value
	}
	return diagnosticCustom
}

// unsafeViewOutcome refines a missing-content outcome when the request did
// expose a model-visible view that could not be bound. Content was available
// but was not proven visible to the model.
func unsafeViewOutcome(outcome string, binding summaryview.Binding) string {
	if outcome != outcomeNoContent || !binding.Present || binding.Bound {
		return outcome
	}
	return outcomeUnsafeView
}

func isSummaryContextError(err error) bool {
	return err != nil &&
		(errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded))
}

// modelCallStatus reports this attempt's observed summary model call from the
// attempt-local recorder. A caller-attached Report can retain Call.Mode from a
// prior sequential target; leftover Report state is never treated as this
// attempt's observation.
func (a *Attempt) modelCallStatus() string {
	if a == nil {
		return modelCallStatusUnobserved
	}
	return summaryModelCallStatus(a.modelCall.Mode)
}

func summaryModelCallStatus(mode string) string {
	switch mode {
	case callModeStandalone, callModeCacheSafeFork:
		return modelCallStatusCalled
	case callModeCustomResponse:
		return modelCallStatusCustomResponse
	default:
		return modelCallStatusUnobserved
	}
}

func summaryTargetKind(filterKey string) string {
	if filterKey == session.SummaryFilterKeyAllContents {
		return targetKindFull
	}
	return targetKindBranch
}

// Cascade diagnostics describe how a branch-triggered summary reached the
// full-session target.
const (
	// cascadeModeSingleFilter marks a session whose events all match the
	// trigger filter key, so one generated summary is reused for both targets.
	cascadeModeSingleFilter = "single_filter"
	// cascadeModeDependent marks a multi-filter cascade that runs the branch
	// target first and only then the dependent full-session target.
	cascadeModeDependent = "dependent"

	// cascadeActionCopied marks a materialized branch summary that was
	// successfully copied to the full-session target.
	cascadeActionCopied = "copied"
	// cascadeActionDependent marks a full-session target that actually
	// started only after this pass materialized the branch source.
	cascadeActionDependent = "dependent"
	// cascadeActionIndependent marks a full-session target updated without a
	// materialized branch source.
	cascadeActionIndependent = "independent"
	// cascadeActionSkipped marks a cascade that started no full-session
	// action, including a normal upstream stop when this pass did not
	// materialize the branch, a failed copy, or a source-side error that
	// blocked the full target.
	cascadeActionSkipped = "skipped"

	// cascadeInvariantOK marks a cascade whose full-session target is backed
	// by a materialized branch summary or by nothing at all.
	cascadeInvariantOK = "ok"
	// cascadeInvariantViolation marks a full-session target that advanced
	// while the branch target materialized no summary to reuse.
	cascadeInvariantViolation = "violation"

	cascadeOutcomeSuccess = "success"
	cascadeOutcomeError   = "error"
)

// cascadeAttempt accumulates diagnostics for one cascade dispatch. It is owned
// by the goroutine running the cascade; per-target work reports separately.
type cascadeAttempt struct {
	startedAt          time.Time
	mode               string
	filterKey          string
	targets            int
	sourceMaterialized bool
	copied             bool
	dependentStarted   bool
	fullUpdated        bool
	failed             bool
}

func beginCascade(mode, filterKey string, targets int) *cascadeAttempt {
	return &cascadeAttempt{
		startedAt: time.Now(),
		mode:      mode,
		filterKey: filterKey,
		targets:   targets,
	}
}

func (c *cascadeAttempt) action() string {
	if c.copied {
		return cascadeActionCopied
	}
	if c.dependentStarted {
		return cascadeActionDependent
	}
	if c.fullUpdated {
		return cascadeActionIndependent
	}
	return cascadeActionSkipped
}

func (c *cascadeAttempt) invariant() string {
	if c.action() == cascadeActionIndependent {
		return cascadeInvariantViolation
	}
	return cascadeInvariantOK
}

func (c *cascadeAttempt) report(ctx context.Context) {
	outcome := cascadeOutcomeSuccess
	if c.failed {
		outcome = cascadeOutcomeError
	}
	invariant := c.invariant()
	filterKey, filterKeyTruncated := summarydiag.FormatFilterKey(c.filterKey)
	format := "Session summary cascade result: schema_version=%d, " +
		"outcome=%s, mode=%s, trigger_filter_key=%q, " +
		"trigger_filter_key_truncated=%t, targets=%d, " +
		"source_materialized=%t, action=%s, invariant=%s, duration_ms=%d"
	args := []any{
		summarydiag.SchemaVersion,
		outcome,
		c.mode,
		filterKey,
		filterKeyTruncated,
		c.targets,
		c.sourceMaterialized,
		c.action(),
		invariant,
		time.Since(c.startedAt).Milliseconds(),
	}
	if invariant == cascadeInvariantViolation || c.failed {
		log.WarnfContext(ctx, format, args...)
		return
	}
	log.DebugfContext(ctx, format, args...)
}

// summaryBoundaryMark is a comparable snapshot of a summary boundary. It holds
// only structural cutoff metadata, never summary text.
type summaryBoundaryMark struct {
	present bool
	cutoff  time.Time
	eventID string
}

func markSummaryBoundary(
	sess *session.Session,
	filterKey string,
) summaryBoundaryMark {
	if sess == nil {
		return summaryBoundaryMark{}
	}
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	sum := sess.Summaries[filterKey]
	if sum == nil {
		return summaryBoundaryMark{}
	}
	mark := summaryBoundaryMark{present: true, cutoff: sum.UpdatedAt.UTC()}
	if sum.Boundary == nil {
		return mark
	}
	if !sum.Boundary.CutoffAt.IsZero() {
		mark.cutoff = sum.Boundary.CutoffAt.UTC()
	}
	mark.eventID = sum.Boundary.LastEventID
	return mark
}

// advancedFrom reports whether the boundary now covers more stored history
// than prev did.
func (m summaryBoundaryMark) advancedFrom(prev summaryBoundaryMark) bool {
	if !m.present {
		return false
	}
	if !prev.present {
		return true
	}
	if m.cutoff.After(prev.cutoff) {
		return true
	}
	return m.cutoff.Equal(prev.cutoff) && m.eventID != prev.eventID
}
