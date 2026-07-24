//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// FaultExpectation describes the precise context a fault must expose.
type FaultExpectation struct {
	Section             string
	PathContains        string
	RequireEventIndex   bool
	RequireMemoryID     bool
	RequireSummaryKey   bool
	RequireTrackName    bool
	RequireBlockingDiff bool
}

// Matches reports whether a blocking diff proves the expected fault location.
func (e FaultExpectation) Matches(diff Diff) bool {
	if e.RequireBlockingDiff && diff.AllowedDiff {
		return false
	}
	if e.Section != "" && diff.Section != e.Section {
		return false
	}
	if e.PathContains != "" && !strings.Contains(diff.Path, e.PathContains) {
		return false
	}
	if e.RequireEventIndex && diff.EventIndex == nil {
		return false
	}
	if e.RequireMemoryID && diff.MemoryID == "" {
		return false
	}
	if e.RequireSummaryKey && diff.SummaryFilterKey == nil {
		return false
	}
	if e.RequireTrackName && diff.TrackName == "" {
		return false
	}
	return true
}

// FaultInjection is a deterministic mutation used to prove replay detection.
type FaultInjection struct {
	Name        string
	Case        string
	Description string
	Expect      FaultExpectation
	Apply       func(*Trace) error
}

// Inject clones a trace and applies the fault without changing the source.
func (f FaultInjection) Inject(source Trace) (Trace, error) {
	if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.Case) == "" {
		return Trace{}, fmt.Errorf("fault name and case are required")
	}
	if f.Apply == nil {
		return Trace{}, fmt.Errorf("fault %q has no mutation", f.Name)
	}
	faulty, err := source.Clone()
	if err != nil {
		return Trace{}, err
	}
	if err := f.Apply(&faulty); err != nil {
		return Trace{}, fmt.Errorf("inject fault %q: %w", f.Name, err)
	}
	return faulty, nil
}

// Clone returns a deep trace copy suitable for deterministic fault injection.
func (t Trace) Clone() (Trace, error) {
	final, err := t.Final.Clone()
	if err != nil {
		return Trace{}, err
	}
	out := Trace{
		Backend:     t.Backend,
		Checkpoints: make([]CheckpointSnapshot, len(t.Checkpoints)),
		Final:       final,
	}
	for i := range t.Checkpoints {
		snapshot, err := t.Checkpoints[i].Snapshot.Clone()
		if err != nil {
			return Trace{}, fmt.Errorf("clone checkpoint %q: %w", t.Checkpoints[i].Name, err)
		}
		out.Checkpoints[i] = CheckpointSnapshot{
			Name:     t.Checkpoints[i].Name,
			AfterOp:  t.Checkpoints[i].AfterOp,
			Snapshot: snapshot,
		}
	}
	return out, nil
}

// DetectInjectedFault applies the same causal validation and semantic
// comparison used by RunCase to an already captured faulty trace.
func DetectInjectedFault(
	replayCase ReplayCase,
	baselineName, faultyName string,
	baseline, faulty Trace,
) ([]Diff, error) {
	var diffs []Diff
	if len(replayCase.Order.ExactLogicalIDs) > 0 ||
		len(replayCase.Order.HappensBefore) > 0 {
		diffs = append(
			diffs,
			validateTraceEventOrder(
				replayCase.Name,
				faultyName,
				faulty,
				replayCase.Order,
			)...,
		)
		baseline = canonicalizeTraceEventOrder(baseline)
		faulty = canonicalizeTraceEventOrder(faulty)
	}
	compared, err := CompareTraces(
		replayCase.Name,
		baselineName,
		faultyName,
		baseline,
		faulty,
		replayCase.Allowed,
	)
	if err != nil {
		return nil, err
	}
	return append(diffs, compared...), nil
}

// PublicFaults returns deterministic anomalies covering every public replay
// case, including all required summary corruption classes.
func PublicFaults() []FaultInjection {
	blocking := func(section, path string) FaultExpectation {
		return FaultExpectation{
			Section: section, PathContains: path, RequireBlockingDiff: true,
		}
	}
	eventFault := func(section, path string) FaultExpectation {
		expect := blocking(section, path)
		expect.RequireEventIndex = true
		return expect
	}
	memoryFault := func(path string) FaultExpectation {
		expect := blocking("memories", path)
		expect.RequireMemoryID = true
		return expect
	}
	summaryFault := func(path string) FaultExpectation {
		expect := blocking("summaries", path)
		expect.RequireSummaryKey = true
		return expect
	}
	trackFault := func(path string) FaultExpectation {
		expect := blocking("tracks", path)
		expect.RequireTrackName = true
		return expect
	}

	return []FaultInjection{
		{
			Name: "single_turn_content_corruption", Case: "single_turn_dialogue",
			Description: "changes the assistant business content",
			Expect:      eventFault("events", "content"),
			Apply: func(trace *Trace) error {
				return mutateEvent(trace, "event:turn-1-assistant", func(message map[string]any, _ map[string]any) {
					message["content"] = "corrupted assistant response"
				})
			},
		},
		{
			Name: "multi_turn_reorder", Case: "multi_turn_ordering",
			Description: "swaps two adjacent turns",
			Expect:      eventFault("events", "$.events["),
			Apply: func(trace *Trace) error {
				if len(trace.Final.Events) < 4 {
					return fmt.Errorf("need at least four events")
				}
				trace.Final.Events[2], trace.Final.Events[3] =
					trace.Final.Events[3], trace.Final.Events[2]
				return nil
			},
		},
		{
			Name:        "tool_args_reference_corruption",
			Case:        "tool_call_response_extensions",
			Description: "changes tool-call arguments in the result extension",
			Expect:      eventFault("events", "extensions"),
			Apply: func(trace *Trace) error {
				return mutateEvent(trace, "event:tool-result-event", func(_ map[string]any, value map[string]any) {
					extensions, _ := value["extensions"].(map[string]any)
					args, _ := extensions[event.ToolCallArgsExtensionKey].(map[string]any)
					weather, _ := args["tool-call:weather-call"].(map[string]any)
					weather["units"] = "fahrenheit"
				})
			},
		},
		{
			Name: "state_stale_after_delete", Case: "state_lifecycle",
			Description: "restores a deleted app-state key",
			Expect:      blocking("app_state", "app_drop"),
			Apply: func(trace *Trace) error {
				trace.Final.AppState["app_drop"] = map[string]any{
					"kind": "json", "value": 2,
				}
				return nil
			},
		},
		{
			Name: "memory_loss", Case: "memory_write_read",
			Description: "drops one persisted memory",
			Expect:      memoryFault("$.memories["),
			Apply: func(trace *Trace) error {
				if len(trace.Final.Memories) == 0 {
					return fmt.Errorf("no memories to remove")
				}
				trace.Final.Memories = trace.Final.Memories[1:]
				return nil
			},
		},
		{
			Name: "memory_duplicate_after_retry", Case: "memory_update_delete",
			Description: "duplicates the updated memory after a retry",
			Expect:      memoryFault("$.memories["),
			Apply: func(trace *Trace) error {
				if len(trace.Final.Memories) == 0 {
					return fmt.Errorf("no memory to duplicate")
				}
				duplicate := trace.Final.Memories[0]
				duplicate.ID += "-duplicate"
				trace.Final.Memories = append(trace.Final.Memories, duplicate)
				return nil
			},
		},
		{
			Name: "summary_loss", Case: "summary_filter_and_overwrite",
			Description: "removes a persisted summary",
			Expect:      summaryFault("root/tools/weather"),
			Apply: func(trace *Trace) error {
				return mutateSummary(trace, "root/tools/weather", func(
					snapshot *Snapshot,
					_ *SummarySnapshot,
				) {
					delete(snapshot.Summaries, "root/tools/weather")
				})
			},
		},
		{
			Name:        "summary_overwrite_failure",
			Case:        "summary_filter_and_overwrite",
			Description: "keeps the first summary after a forced update",
			Expect:      summaryFault(".text"),
			Apply: func(trace *Trace) error {
				first, err := checkpointSummary(
					trace, "after_first_summary", "root/tools/weather",
				)
				if err != nil {
					return err
				}
				return mutateSummary(trace, "root/tools/weather", func(
					_ *Snapshot,
					value *SummarySnapshot,
				) {
					value.Text = first.Text
					value.UpdatedAtEventIndex = cloneIntPointer(
						first.UpdatedAtEventIndex,
					)
					value.CutoffAtEventIndex = cloneIntPointer(
						first.CutoffAtEventIndex,
					)
					value.LastEventLogicalID = first.LastEventLogicalID
					value.LastEventIndex = cloneIntPointer(first.LastEventIndex)
				})
			},
		},
		{
			Name:        "summary_wrong_session",
			Case:        "summary_filter_and_overwrite",
			Description: "assigns the summary to another session",
			Expect:      summaryFault(".session_id"),
			Apply: func(trace *Trace) error {
				return mutateSummary(trace, "root/tools/weather", func(
					_ *Snapshot,
					value *SummarySnapshot,
				) {
					value.SessionID = "session:wrong-owner"
				})
			},
		},
		{
			Name:        "summary_wrong_filter_key",
			Case:        "summary_filter_and_overwrite",
			Description: "stores the summary under the wrong filter key",
			Expect:      summaryFault("root/tools"),
			Apply: func(trace *Trace) error {
				return mutateSummary(trace, "root/tools/weather", func(
					snapshot *Snapshot,
					value *SummarySnapshot,
				) {
					delete(snapshot.Summaries, "root/tools/weather")
					value.FilterKey = "root/tools/wrong"
					snapshot.Summaries["root/tools/wrong"] = *value
				})
			},
		},
		{
			Name:        "summary_boundary_corruption",
			Case:        "summary_event_window_recovery",
			Description: "moves the compression boundary to the wrong event",
			Expect:      summaryFault("last_event"),
			Apply: func(trace *Trace) error {
				return mutateSummary(trace, "", func(
					_ *Snapshot,
					value *SummarySnapshot,
				) {
					value.LastEventLogicalID = "event:window-user-1"
					value.LastEventIndex = intPointer(0)
					value.CutoffAtEventIndex = intPointer(0)
				})
			},
		},
		{
			Name:        "track_name_corruption",
			Case:        "track_status_error_invocation",
			Description: "moves tool track history under a wrong name",
			Expect:      trackFault("tool.weather"),
			Apply: func(trace *Trace) error {
				values, ok := trace.Final.Tracks["tool.weather"]
				if !ok {
					return fmt.Errorf("track tool.weather not found")
				}
				delete(trace.Final.Tracks, "tool.weather")
				for i := range values {
					values[i].Track = "tool.wrong"
				}
				trace.Final.Tracks["tool.wrong"] = values
				return nil
			},
		},
		{
			Name:        "concurrent_causal_order_violation",
			Case:        "concurrent_causal_order",
			Description: "moves a child event before its triggering user event",
			Expect:      eventFault("events", "$.events["),
			Apply: func(trace *Trace) error {
				index := eventIndex(trace.Final, "event:parallel-a-call")
				if index < 0 {
					return fmt.Errorf("parallel child event not found")
				}
				child := trace.Final.Events[index]
				copy(trace.Final.Events[1:index+1], trace.Final.Events[0:index])
				trace.Final.Events[0] = child
				return nil
			},
		},
		{
			Name:        "recovery_duplicate_event",
			Case:        "failure_retry_ack_loss",
			Description: "duplicates the acknowledgement-lost event",
			Expect:      eventFault("events", "$.events["),
			Apply: func(trace *Trace) error {
				index := eventIndex(trace.Final, "event:recovery-assistant")
				if index < 0 {
					return fmt.Errorf("recovery event not found")
				}
				trace.Final.Events = append(
					trace.Final.Events,
					cloneGenericMap(trace.Final.Events[index]),
				)
				trace.Final.State["phase"] = map[string]any{
					"kind": "json", "value": "started",
				}
				return nil
			},
		},
		{
			Name:        "identity_duplicate_collapse",
			Case:        "identity_duplicate_preservation",
			Description: "collapses one of two equal-content events",
			Expect:      eventFault("events", "$.events["),
			Apply: func(trace *Trace) error {
				index := eventIndex(trace.Final, "event:duplicate-user-2")
				if index < 0 {
					return fmt.Errorf("second duplicate event not found")
				}
				trace.Final.Events = append(
					trace.Final.Events[:index],
					trace.Final.Events[index+1:]...,
				)
				return nil
			},
		},
	}
}

// SessionFaultMode identifies a public Session-service anomaly.
type SessionFaultMode string

const (
	// SessionFaultPreCommitEventError rejects an event before persistence.
	SessionFaultPreCommitEventError SessionFaultMode = "pre_commit_event_error"
	// SessionFaultLostEventAck commits an event but returns an error.
	SessionFaultLostEventAck SessionFaultMode = "lost_event_ack"
	// SessionFaultDuplicateEvent persists the same event twice.
	SessionFaultDuplicateEvent SessionFaultMode = "duplicate_event"
	// SessionFaultDirtyState reapplies a stale state delta after a write.
	SessionFaultDirtyState SessionFaultMode = "dirty_state"
	// SessionFaultLostSummaryAck commits a summary but returns an error.
	SessionFaultLostSummaryAck SessionFaultMode = "lost_summary_ack"
)

// FaultySessionService is a reusable service-level fault injector. It embeds
// the complete public interface and overrides only the selected operation.
type FaultySessionService struct {
	session.Service
	Mode SessionFaultMode
}

// AppendEvent injects duplicate-event or dirty-state behavior.
func (s *FaultySessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if s.Mode == SessionFaultPreCommitEventError {
		return errors.New("replaytest: event rejected before commit")
	}
	if err := s.Service.AppendEvent(ctx, sess, evt, opts...); err != nil {
		return err
	}
	switch s.Mode {
	case SessionFaultLostEventAck:
		return errors.New("replaytest: event acknowledgement lost")
	case SessionFaultDuplicateEvent:
		return s.Service.AppendEvent(ctx, sess, evt, opts...)
	case SessionFaultDirtyState:
		if len(evt.StateDelta) == 0 {
			return nil
		}
		stale := make(session.StateMap, len(evt.StateDelta))
		for key := range evt.StateDelta {
			stale[key] = []byte(`"stale-after-retry"`)
		}
		dirty := evt.Clone()
		dirty.ID += "-dirty-state"
		dirty.StateDelta = stale
		return s.Service.AppendEvent(ctx, sess, dirty, opts...)
	default:
		return nil
	}
}

// CreateSessionSummary injects a lost acknowledgement after persistence.
func (s *FaultySessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if err := s.Service.CreateSessionSummary(
		ctx,
		sess,
		filterKey,
		force,
	); err != nil {
		return err
	}
	if s.Mode == SessionFaultLostSummaryAck {
		return errors.New("replaytest: summary acknowledgement lost")
	}
	return nil
}

// MemoryFaultMode identifies a public Memory-service anomaly.
type MemoryFaultMode string

const (
	// MemoryFaultDuplicateWrite writes the same semantic memory twice.
	MemoryFaultDuplicateWrite MemoryFaultMode = "duplicate_write"
)

// FaultyMemoryService is a reusable public Memory-service fault injector.
type FaultyMemoryService struct {
	memory.Service
	Mode MemoryFaultMode
}

// AddMemory injects a second write with a distinguishable duplicate ID.
func (s *FaultyMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	if err := s.Service.AddMemory(
		ctx,
		userKey,
		content,
		topics,
		opts...,
	); err != nil {
		return err
	}
	if s.Mode != MemoryFaultDuplicateWrite {
		return nil
	}
	return s.Service.AddMemory(
		ctx,
		userKey,
		content+" [retry duplicate]",
		topics,
		opts...,
	)
}

func mutateEvent(
	trace *Trace,
	logicalID string,
	mutate func(message, event map[string]any),
) error {
	index := eventIndex(trace.Final, logicalID)
	if index < 0 {
		return fmt.Errorf("event %q not found", logicalID)
	}
	value := trace.Final.Events[index]
	choices, _ := value["choices"].([]any)
	if len(choices) == 0 {
		return fmt.Errorf("event %q has no choices", logicalID)
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message == nil {
		return fmt.Errorf("event %q has no message", logicalID)
	}
	mutate(message, value)
	return nil
}

func eventIndex(snapshot Snapshot, logicalID string) int {
	for index := range snapshot.Events {
		if snapshot.Events[index]["id"] == logicalID {
			return index
		}
	}
	return -1
}

func mutateSummary(
	trace *Trace,
	filterKey string,
	mutate func(*Snapshot, *SummarySnapshot),
) error {
	found := false
	apply := func(snapshot *Snapshot) {
		value, ok := snapshot.Summaries[filterKey]
		if !ok {
			return
		}
		found = true
		mutate(snapshot, &value)
		if _, exists := snapshot.Summaries[filterKey]; exists {
			snapshot.Summaries[filterKey] = value
		}
	}
	apply(&trace.Final)
	for i := range trace.Checkpoints {
		if trace.Checkpoints[i].Name == "after_summary_overwrite" ||
			trace.Checkpoints[i].Name == "summary_plus_tail_events" {
			apply(&trace.Checkpoints[i].Snapshot)
		}
	}
	if !found {
		return fmt.Errorf("summary %q not found", filterKey)
	}
	return nil
}

func checkpointSummary(
	trace *Trace,
	checkpoint, filterKey string,
) (SummarySnapshot, error) {
	for i := range trace.Checkpoints {
		if trace.Checkpoints[i].Name != checkpoint {
			continue
		}
		value, ok := trace.Checkpoints[i].Snapshot.Summaries[filterKey]
		if !ok {
			return SummarySnapshot{}, fmt.Errorf(
				"summary %q not found at checkpoint %q",
				filterKey,
				checkpoint,
			)
		}
		return value, nil
	}
	return SummarySnapshot{}, fmt.Errorf("checkpoint %q not found", checkpoint)
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
