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
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// RunCase executes a single replay case against a backend and
// returns the normalized snapshot.
func RunCase(
	ctx context.Context,
	backend Backend,
	c ReplayCase,
) (Snapshot, error) {
	sessService := backend.SessionService

	// Build session key.
	key := session.Key{
		AppName:   c.AppName,
		UserID:    c.UserID,
		SessionID: c.SessionID,
	}

	// Create session with initial state.
	sess, err := sessService.CreateSession(ctx, key, initialState(c))
	if err != nil {
		return Snapshot{}, fmt.Errorf("create session: %w", err)
	}

	// Append events with deterministic timestamps and summary steps.
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := runEventSteps(ctx, sessService, sess, c, baseTime); err != nil {
		return Snapshot{}, err
	}

	// Write memories, then read them back so that write-only
	// cases also produce snapshot data.
	allMemories, err := collectMemories(ctx, backend.MemoryService, c)
	if err != nil {
		return Snapshot{}, err
	}

	// Append track events. A case that requests track events must fail
	// loudly when the backend does not implement session.TrackService —
	// silently skipping would let the case pass with empty Tracks.
	if err := appendTrackEvents(ctx, sessService, backend, sess, c, baseTime); err != nil {
		return Snapshot{}, err
	}

	// Re-fetch session to get latest state.
	sess, err = sessService.GetSession(ctx, key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}

	// A case that requests summaries must actually get them persisted —
	// an empty Summaries means CreateSessionSummary was a no-op (e.g. no
	// summarizer configured), which would let the case pass vacuously.
	if len(c.SummarySteps) > 0 && len(sess.Summaries) == 0 {
		return Snapshot{}, fmt.Errorf(
			"case %q requested summaries but backend %q persisted none",
			c.Name, backend.Name,
		)
	}

	return normalizeSnapshot(sess, allMemories), nil
}

// initialState converts the case's initial state into a session.StateMap.
func initialState(c ReplayCase) session.StateMap {
	initialState := make(session.StateMap, len(c.InitialState))
	for k, v := range c.InitialState {
		initialState[k] = []byte(v)
	}
	return initialState
}

// runEventSteps appends events with deterministic timestamps and
// triggers summary steps after their target event index.
func runEventSteps(
	ctx context.Context,
	sessService session.Service,
	sess *session.Session,
	c ReplayCase,
	baseTime time.Time,
) error {
	for i, es := range c.Events {
		evt, err := buildEvent(es, i, c.SessionID, baseTime)
		if err != nil {
			return fmt.Errorf("build event %d: %w", i, err)
		}
		if err := sessService.AppendEvent(ctx, sess, evt); err != nil {
			return fmt.Errorf("append event %d: %w", i, err)
		}

		// Check for summary steps after this event.
		for _, ss := range c.SummarySteps {
			if ss.AfterEventIndex == i+1 {
				if err := sessService.CreateSessionSummary(
					ctx, sess, ss.FilterKey, ss.Force,
				); err != nil {
					return fmt.Errorf(
						"create summary at event %d: %w", i+1, err,
					)
				}
			}
		}
	}
	return nil
}

// collectMemories writes memories, reads them back, and runs memory
// queries, returning every entry observed.
func collectMemories(
	ctx context.Context,
	memService memory.Service,
	c ReplayCase,
) ([]*memory.Entry, error) {
	userKey := memory.UserKey{AppName: c.AppName, UserID: c.UserID}
	for _, mw := range c.MemoryWrites {
		if err := memService.AddMemory(
			ctx, userKey, mw.Memory, mw.Topics,
		); err != nil {
			return nil, fmt.Errorf("add memory: %w", err)
		}
	}

	var allMemories []*memory.Entry
	if len(c.MemoryWrites) > 0 {
		readEntries, err := memService.ReadMemories(ctx, userKey, 1000)
		if err != nil {
			return nil, fmt.Errorf("read memories: %w", err)
		}
		allMemories = append(allMemories, readEntries...)
	}

	// Execute memory queries and collect results.
	for _, mq := range c.MemoryQueries {
		limit := mq.Limit
		if limit <= 0 {
			limit = 10
		}
		entries, err := memService.SearchMemories(
			ctx, userKey, mq.Query,
			memory.WithSearchOptions(memory.SearchOptions{
				MaxResults: limit,
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("search memories: %w", err)
		}
		allMemories = append(allMemories, entries...)
	}
	return allMemories, nil
}

// appendTrackEvents writes track events when the case requests them.
func appendTrackEvents(
	ctx context.Context,
	sessService session.Service,
	backend Backend,
	sess *session.Session,
	c ReplayCase,
	baseTime time.Time,
) error {
	if len(c.TrackEvents) == 0 {
		return nil
	}
	trackSvc, ok := sessService.(session.TrackService)
	if !ok {
		return fmt.Errorf(
			"case %q requests track events but backend %q does not implement session.TrackService",
			c.Name, backend.Name,
		)
	}
	for _, ts := range c.TrackEvents {
		trackEvent := &session.TrackEvent{
			Track:     session.Track(ts.Track),
			Payload:   json.RawMessage(ts.Payload),
			Timestamp: baseTime,
		}
		if err := trackSvc.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
			return fmt.Errorf("append track event: %w", err)
		}
	}
	return nil
}

// buildEvent constructs an event.Event from an EventSpec with
// deterministic IDs and timestamps.
func buildEvent(es EventSpec, index int, sessionID string, baseTime time.Time) (*event.Event, error) {
	invocationID := es.InvocationID
	if invocationID == "" {
		invocationID = fmt.Sprintf("inv-%s-%d", sessionID, index)
	}
	evt := event.New(invocationID, es.Author)
	evt.Timestamp = baseTime.Add(time.Duration(index) * time.Second)
	evt.FilterKey = es.FilterKey
	evt.Branch = es.Branch
	evt.Tag = es.Tag

	// Build model response based on spec.
	msg := model.Message{
		Role: model.Role(es.Role),
	}

	if es.Role == string(model.RoleTool) {
		// Tool response.
		if es.ToolResponse == nil {
			return nil, fmt.Errorf("tool response is nil for role=tool at event index %d", index)
		}
		msg.ToolID = es.ToolResponse.ID
		msg.Content = es.ToolResponse.Content
	} else if len(es.ToolCalls) > 0 {
		// Assistant message with tool calls.
		toolCalls := make([]model.ToolCall, len(es.ToolCalls))
		for i, tc := range es.ToolCalls {
			toolCalls[i] = model.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      tc.Name,
					Arguments: []byte(tc.Arguments),
				},
			}
		}
		msg.ToolCalls = toolCalls
	} else {
		// Regular text message.
		msg.Content = es.Content
	}

	resp := &model.Response{
		Choices: []model.Choice{
			{Message: msg},
		},
	}

	// Include state delta if present.
	if es.StateDelta != nil {
		evt.StateDelta = make(map[string][]byte)
		for k, v := range es.StateDelta {
			evt.StateDelta[k] = []byte(v)
		}
	}

	evt.Response = resp
	return evt, nil
}

// RunReplayMatrix executes all replay cases across all provided
// backends, computes pairwise diffs, and returns the full diff
// report for each case-backend pair.
func RunReplayMatrix(
	ctx context.Context,
	backends []Backend,
	cases []ReplayCase,
	allowedDiffs []AllowedDiffRule,
) ([]DiffReport, error) {
	var reports []DiffReport

	for _, c := range cases {
		// Run case on each backend, honoring Setup/Teardown so
		// database-backed backends can prepare and clean up schema.
		snapshots := make([]Snapshot, len(backends))
		for i, b := range backends {
			var snap Snapshot
			var err error
			if b.Setup != nil {
				if err = b.Setup(ctx); err != nil {
					return nil, fmt.Errorf("backend %q setup: %w", b.Name, err)
				}
			}
			snap, err = RunCase(ctx, b, c)
			if b.Teardown != nil {
				if terr := b.Teardown(ctx); terr != nil && err == nil {
					err = fmt.Errorf("backend %q teardown: %w", b.Name, terr)
				}
			}
			if err != nil {
				return nil, fmt.Errorf(
					"case %q on backend %q: %w", c.Name, b.Name, err,
				)
			}
			snapshots[i] = snap
		}

		// Pairwise comparison.
		for i := 0; i < len(backends); i++ {
			for j := i + 1; j < len(backends); j++ {
				diffs := CompareSnapshots(
					snapshots[i], snapshots[j],
					backends[i].Name, backends[j].Name,
					allowedDiffs,
				)
				reports = append(reports, DiffReport{
					CaseName:  c.Name,
					SessionID: c.SessionID,
					BackendA:  backends[i].Name,
					BackendB:  backends[j].Name,
					Diffs:     diffs,
				})
			}
		}
	}
	return reports, nil
}
