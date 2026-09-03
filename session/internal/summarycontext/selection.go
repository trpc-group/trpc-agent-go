//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import "context"

// Summary input sources describe where the summarizer took its candidate
// events from. They are stable, low-cardinality diagnostic values.
const (
	// SourceModelVisible marks events taken from a bound model-visible view.
	SourceModelVisible = "model_visible"
	// SourceUnboundView marks a model-visible view that exists but is not
	// bound to the request the model answered, so nothing may be summarized.
	SourceUnboundView = "unbound_view"
	// SourceSessionEvents marks the fallback that summarizes stored session
	// events because no model-visible view was available.
	SourceSessionEvents = "session_events"
	// SourceCustom marks a summarizer that does not publish the built-in
	// event selection, so counts for this call are unknown.
	SourceCustom = "custom"
)

// Selection reasons form a closed set that explains why the summary model saw
// the number of events it saw. Exactly one applies to a summary call.
const (
	// ReasonNone marks a call for which no selection was observed, because
	// the attempt never reached a summarizer.
	ReasonNone = "none"
	// ReasonCustom marks a summarizer that does not publish the built-in
	// event selection. Counts are unknown for this call.
	ReasonCustom = "custom"
	// ReasonSelected marks a selection that reached the summary model.
	ReasonSelected = "selected"
	// ReasonNoCandidates marks a selection whose stage had no candidate event
	// to consider in the first place.
	ReasonNoCandidates = "no_candidates"
	// ReasonSkipRecentAll marks a skip-recent callback that asked to skip at
	// least as many events as were available, leaving nothing to summarize.
	ReasonSkipRecentAll = "skip_recent_all"
	// ReasonUnsafePrefix marks a retained prefix that was dropped because it
	// contained neither a user message nor a prepended previous summary, so
	// it had no anchor to summarize against.
	ReasonUnsafePrefix = "unsafe_prefix"
	// ReasonSessionFilterEmpty marks candidates that survived skip-recent but
	// were all removed by the session's branch scoping.
	ReasonSessionFilterEmpty = "session_filter_empty"
	// ReasonUnboundView marks a model-visible view that exists but is not
	// bound to the request the model answered, so nothing may be summarized.
	ReasonUnboundView = "unbound_view"
	// ReasonBoundaryUnmapped marks selected items that have no structural
	// mapping to a stored event, so the selection was dropped rather than
	// advance the persistence boundary past unmapped content.
	ReasonBoundaryUnmapped = "boundary_unmapped"
)

// UnknownEventCount marks a count that the configured summarizer did not
// publish, for example a custom summarizer that does not use the built-in
// event selection.
const UnknownEventCount = -1

// EventSelection reports how many events reached the summary model for one
// summary call and why. It carries counts and stable reasons only, never event
// content.
type EventSelection struct {
	// Source names where the candidate events came from.
	Source string
	// Reason is the closed-set explanation for Selected. It distinguishes a
	// normal selection from each way a selection can end up empty.
	Reason string
	// Eligible is the number of candidate events at the stage that feeds the
	// skip-recent callback, counted before skip-recent runs. For a bound
	// model-visible view this includes any prepended previous summary; for an
	// unbound view it is the number of view items that were not considered.
	Eligible int
	// SkipRecentRequested is the raw count returned by the configured
	// WithSkipRecent callback for the slice it received at this stage. It is
	// zero when no callback is configured, and is reported unchanged so a
	// callback returning a nonsensical count stays visible.
	SkipRecentRequested int
	// SkipRecentApplied is how many events skip-recent itself removed:
	// clamp(SkipRecentRequested, 0, Eligible). Later stages such as an unsafe
	// retained prefix, session scoping, or an unmapped boundary are not
	// counted here.
	SkipRecentApplied int
	// Selected is the number of events chosen before a PreSummaryHook or
	// before-model callback may rewrite the prompt. It is not a count of the
	// payload that later reached the summary model.
	Selected int
}

// UnknownSelection returns the selection reported for a summary call whose
// counts were never published.
func UnknownSelection(source, reason string) EventSelection {
	return EventSelection{
		Source:              source,
		Reason:              reason,
		Eligible:            UnknownEventCount,
		SkipRecentRequested: UnknownEventCount,
		SkipRecentApplied:   UnknownEventCount,
		Selected:            UnknownEventCount,
	}
}

type eventSelectionKey struct{}

// WithEventSelectionRecorder attaches a recorder that the summarizer fills in
// while selecting summary input. The returned selection is written by
// RecordEventSelection during the summary call and must not be read before it
// returns. A nil recorder makes RecordEventSelection a no-op.
func WithEventSelectionRecorder(
	ctx context.Context,
	selection *EventSelection,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if selection == nil {
		return ctx
	}
	return context.WithValue(ctx, eventSelectionKey{}, selection)
}

// RecordEventSelection publishes the summary input selection for the current
// summary call, replacing any selection recorded earlier in the same call. It
// is a no-op when no recorder is attached to ctx.
func RecordEventSelection(ctx context.Context, selection EventSelection) {
	if ctx == nil {
		return
	}
	recorder, ok := ctx.Value(eventSelectionKey{}).(*EventSelection)
	if !ok || recorder == nil {
		return
	}
	*recorder = selection
}

// EventSelectionFromContext returns the attempt-local event-selection
// recorder, or nil when none is attached.
func EventSelectionFromContext(ctx context.Context) *EventSelection {
	if ctx == nil {
		return nil
	}
	recorder, ok := ctx.Value(eventSelectionKey{}).(*EventSelection)
	if !ok {
		return nil
	}
	return recorder
}
