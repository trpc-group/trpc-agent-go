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
	memService := backend.MemoryService

	// Create initial state map.
	initialState := make(session.StateMap)
	for k, v := range c.InitialState {
		initialState[k] = []byte(v)
	}

	// Build session key.
	key := session.Key{
		AppName:   c.AppName,
		UserID:    c.UserID,
		SessionID: c.SessionID,
	}

	// Create session.
	sess, err := sessService.CreateSession(ctx, key, initialState)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create session: %w", err)
	}

	// Append events with deterministic timestamps.
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, es := range c.Events {
		evt := buildEvent(es, i, c.SessionID, baseTime)
		if err := sessService.AppendEvent(ctx, sess, evt); err != nil {
			return Snapshot{}, fmt.Errorf("append event %d: %w", i, err)
		}

		// Check for summary steps after this event.
		for _, ss := range c.SummarySteps {
			if ss.AfterEventIndex == i+1 {
				if err := sessService.CreateSessionSummary(
					ctx, sess, ss.FilterKey, ss.Force,
				); err != nil {
					return Snapshot{}, fmt.Errorf(
						"create summary at event %d: %w", i+1, err,
					)
				}
			}
		}
	}

	// Write memories.
	userKey := memory.UserKey{AppName: c.AppName, UserID: c.UserID}
	for _, mw := range c.MemoryWrites {
		if err := memService.AddMemory(
			ctx, userKey, mw.Memory, mw.Topics,
		); err != nil {
			return Snapshot{}, fmt.Errorf("add memory: %w", err)
		}
	}

	// Read all memories after writes so that write-only
	// cases also produce snapshot data.
	var allMemories []*memory.Entry
	if len(c.MemoryWrites) > 0 {
		readEntries, err := memService.ReadMemories(ctx, userKey, 1000)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read memories: %w", err)
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
				Query:      mq.Query,
				MaxResults: limit,
			}),
		)
		if err != nil {
			return Snapshot{}, fmt.Errorf("search memories: %w", err)
		}
		allMemories = append(allMemories, entries...)
	}

	// Append track events.
	for _, ts := range c.TrackEvents {
		trackEvent := &session.TrackEvent{
			Track:     session.Track(ts.Track),
			Payload:   json.RawMessage(ts.Payload),
			Timestamp: baseTime,
		}
		if ts, ok := sessService.(session.TrackService); ok {
			if err := ts.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
				return Snapshot{}, fmt.Errorf("append track event: %w", err)
			}
		}
	}

	// Re-fetch session to get latest state.
	sess, err = sessService.GetSession(ctx, key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}

	return normalizeSnapshot(sess, allMemories), nil
}

// buildEvent constructs an event.Event from an EventSpec with
// deterministic IDs and timestamps.
func buildEvent(es EventSpec, index int, sessionID string, baseTime time.Time) *event.Event {
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
	return evt
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
		// Run case on each backend.
		snapshots := make([]Snapshot, len(backends))
		for i, b := range backends {
			snap, err := RunCase(ctx, b, c)
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
