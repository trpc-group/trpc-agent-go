//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var caseEpoch = time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)

// PublicCases returns fresh instances of the standard replay matrix.
func PublicCases() []Case {
	return []Case{
		singleTurnCase(),
		multiTurnCase(),
		toolCallCase(),
		stateCRUDCase(),
		memoryCase(),
		summaryUpdateCase(),
		summaryTruncationCase(),
		summaryFilterKeyCase(),
		trackCase(),
		concurrentCase(),
		recoveryCase(),
	}
}

func singleTurnCase() Case {
	return Case{
		Name:        "single_turn_text",
		Description: "one user message and one assistant response",
		Requires:    []Capability{CapabilitySession},
		Steps: []Step{
			messageStep("append-user", "single-user", 1, "user", model.RoleUser, "hello", ""),
			messageStep("append-assistant", "single-assistant", 2, "assistant", model.RoleAssistant, "hello back", ""),
		},
		Fault: FaultEventContent,
	}
}

func multiTurnCase() Case {
	return Case{
		Name:        "multi_turn_order",
		Description: "three conversation turns preserve global event order",
		Requires:    []Capability{CapabilitySession},
		Steps: []Step{
			messageStep("turn-1-user", "turn-1-user", 1, "user", model.RoleUser, "first", ""),
			messageStep("turn-1-assistant", "turn-1-assistant", 2, "assistant", model.RoleAssistant, "one", ""),
			messageStep("turn-2-user", "turn-2-user", 3, "user", model.RoleUser, "second", ""),
			messageStep("turn-2-assistant", "turn-2-assistant", 4, "assistant", model.RoleAssistant, "two", ""),
			messageStep("turn-3-user", "turn-3-user", 5, "user", model.RoleUser, "third", ""),
			messageStep("turn-3-assistant", "turn-3-assistant", 6, "assistant", model.RoleAssistant, "three", ""),
		},
		Fault: FaultEventOrder,
	}
}

func toolCallCase() Case {
	call := model.ToolCall{
		Type: "function",
		ID:   "call-weather-1",
		Function: model.FunctionDefinitionParam{
			Name:      "weather",
			Arguments: []byte(`{"city":"Shenzhen","units":"c"}`),
		},
	}
	toolEvent := responseEvent("tool-call", 2, "assistant", model.Response{
		ID:     "tool-call-response",
		Object: model.ObjectTypeChatCompletion,
		Model:  "replay-model",
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call}},
		}},
	})
	toolEvent.Event.Event.Tag = "tool"
	toolResult := responseEvent("tool-result", 3, "tool", model.Response{
		ID:     "tool-result-response",
		Object: model.ObjectTypeToolResponse,
		Done:   true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:     model.RoleTool,
				Content:  `{"temperature":31}`,
				ToolID:   call.ID,
				ToolName: call.Function.Name,
			},
		}},
	})
	if err := event.SetExtension(toolResult.Event.Event, event.ToolCallArgsExtensionKey, map[string]json.RawMessage{
		call.ID: json.RawMessage(call.Function.Arguments),
	}); err != nil {
		panic(err)
	}
	return Case{
		Name:        "tool_call_roundtrip",
		Description: "tool call, response, arguments extension, tag, and role survive persistence",
		Requires:    []Capability{CapabilitySession},
		Steps: []Step{
			messageStep("tool-user", "tool-user", 1, "user", model.RoleUser, "weather?", ""),
			toolEvent,
			toolResult,
			messageStep("tool-assistant", "tool-assistant", 4, "assistant", model.RoleAssistant, "31 C", ""),
		},
		Fault: FaultToolArguments,
	}
}

func stateCRUDCase() Case {
	stateEvent := messageStep("state-event", "state-event", 1, "assistant", model.RoleAssistant, "state updated", "")
	stateEvent.Event.Event.StateDelta = session.StateMap{"event_counter": []byte(`1`)}
	return Case{
		Name:         "state_crud",
		Description:  "app, user, session, and event-delta state obey overwrite and delete semantics",
		InitialState: session.StateMap{"seed": []byte(`{"ready":true}`)},
		Requires: []Capability{
			CapabilitySession,
			CapabilityAppState,
			CapabilityUserState,
			CapabilitySessionState,
		},
		Steps: []Step{
			stateStep("app-initial", StateScopeApp, session.StateMap{"theme": []byte(`"light"`), "obsolete": []byte(`true`)}, nil),
			stateStep("app-overwrite-delete", StateScopeApp, session.StateMap{"theme": []byte(`"dark"`)}, []string{"obsolete"}),
			stateStep("user-initial", StateScopeUser, session.StateMap{"locale": []byte(`"zh-CN"`), "temporary": []byte(`1`)}, nil),
			stateStep("user-overwrite-delete", StateScopeUser, session.StateMap{"locale": []byte(`"en-US"`)}, []string{"temporary"}),
			stateStep("session-initial", StateScopeSession, session.StateMap{"draft": []byte(`{"step":1}`)}, nil),
			stateStep("session-overwrite", StateScopeSession, session.StateMap{"draft": []byte(`{"step":2}`)}, nil),
			stateEvent,
		},
		Fault: FaultStateValue,
	}
}

func memoryCase() Case {
	eventTime := caseEpoch.Add(-24 * time.Hour)
	preference := &MemoryInput{
		Memory: "User prefers concise technical answers.",
		Topics: []string{"preference", "communication"},
		Metadata: &memory.Metadata{
			Kind: memory.KindFact,
		},
	}
	return Case{
		Name:        "memory_write_read",
		Description: "fact and episodic memories preserve content, metadata, and idempotency",
		Requires:    []Capability{CapabilitySession, CapabilityMemory},
		Steps: []Step{
			{Name: "add-preference", Kind: StepAddMemory, Memory: preference},
			{Name: "retry-preference", Kind: StepAddMemory, Memory: preference},
			{
				Name: "add-episode",
				Kind: StepAddMemory,
				Memory: &MemoryInput{
					Memory: "User shipped the replay harness.",
					Topics: []string{"task", "experience"},
					Metadata: &memory.Metadata{
						Kind:         memory.KindEpisode,
						EventTime:    &eventTime,
						Participants: []string{"user", "reviewer"},
						Location:     "remote",
					},
				},
			},
		},
		Fault: FaultMemoryContent,
	}
}

func summaryUpdateCase() Case {
	return Case{
		Name:        "summary_update",
		Description: "a full-session summary is generated, persisted, and replaced after new events",
		Requires:    []Capability{CapabilitySession, CapabilitySummary},
		Steps: []Step{
			messageStep("summary-user-1", "summary-user-1", 1, "user", model.RoleUser, "alpha", ""),
			messageStep("summary-assistant-1", "summary-assistant-1", 2, "assistant", model.RoleAssistant, "beta", ""),
			{Name: "summary-create", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
			messageStep("summary-user-2", "summary-user-2", 3, "user", model.RoleUser, "gamma", ""),
			{Name: "summary-update", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
		},
		Fault: FaultSummaryText,
	}
}

func summaryTruncationCase() Case {
	return Case{
		Name:        "summary_retained_tail",
		Description: "summary boundary plus post-summary events reconstruct the current context",
		Requires:    []Capability{CapabilitySession, CapabilitySummary},
		Steps: []Step{
			messageStep("history-user", "history-user", 1, "user", model.RoleUser, "old question", ""),
			messageStep("history-assistant", "history-assistant", 2, "assistant", model.RoleAssistant, "old answer", ""),
			{Name: "history-summary", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
			messageStep("tail-user", "tail-user", 3, "user", model.RoleUser, "new question", ""),
			messageStep("tail-assistant", "tail-assistant", 4, "assistant", model.RoleAssistant, "new answer", ""),
		},
		Fault: FaultSummaryMissing,
	}
}

func summaryFilterKeyCase() Case {
	return Case{
		Name:        "summary_filter_key",
		Description: "branch summary ownership and boundary filter key remain distinct",
		Requires:    []Capability{CapabilitySession, CapabilitySummary},
		Steps: []Step{
			messageStep("weather-user", "weather-user", 1, "user", model.RoleUser, "weather", "agent/weather"),
			messageStep("weather-assistant", "weather-assistant", 2, "assistant", model.RoleAssistant, "sunny", "agent/weather"),
			messageStep("research-assistant", "research-assistant", 3, "assistant", model.RoleAssistant, "unrelated", "agent/research"),
			{Name: "weather-summary", Kind: StepCreateSummary, Summary: &SummaryInput{FilterKey: "agent/weather", Force: true}},
		},
		Fault: FaultSummaryFilterKey,
	}
}

func trackCase() Case {
	return Case{
		Name:        "track_events",
		Description: "track name, type, invocation, status, error, and order survive persistence",
		Requires:    []Capability{CapabilitySession, CapabilityTrack},
		Steps: []Step{
			trackStep("tool-start", "tool/weather", 1, map[string]any{"type": "start", "invocation_id": "inv-tool", "status": "running"}),
			trackStep("tool-finish", "tool/weather", 2, map[string]any{"type": "finish", "invocation_id": "inv-tool", "status": "ok", "duration_ms": 12.4}),
			trackStep("subtask-error", "subtask/research", 3, map[string]any{"type": "error", "invocation_id": "inv-subtask", "status": "failed", "error": "timeout", "latency_ms": 99}),
		},
		Fault: FaultTrackPayload,
	}
}

func concurrentCase() Case {
	return Case{
		Name:        "concurrent_interleaving",
		Description: "parallel branches may interleave globally but preserve branch-local causal order",
		Requires:    []Capability{CapabilitySession, CapabilityConcurrent},
		EventOrder:  EventOrderCausal,
		Steps: []Step{
			messageStep("parallel-user", "parallel-user", 1, "user", model.RoleUser, "run both tools", ""),
			{
				Name: "parallel-tools",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{
						messageStep("branch-a-1", "branch-a-1", 2, "assistant", model.RoleAssistant, "a-start", "branch/a"),
						messageStep("branch-a-2", "branch-a-2", 4, "assistant", model.RoleAssistant, "a-end", "branch/a"),
					},
					{
						messageStep("branch-b-1", "branch-b-1", 3, "assistant", model.RoleAssistant, "b-start", "branch/b"),
						messageStep("branch-b-2", "branch-b-2", 5, "assistant", model.RoleAssistant, "b-end", "branch/b"),
					},
				},
			},
		},
		Fault: FaultDuplicateEvent,
	}
}

func recoveryCase() Case {
	preference := &MemoryInput{Memory: "Retries must be idempotent.", Topics: []string{"reliability"}}
	return Case{
		Name:        "failure_retry_recovery",
		Description: "a pre-write failure, event retry, memory retry, reload, and summary retry leave no dirty data",
		Requires:    []Capability{CapabilitySession, CapabilityMemory, CapabilitySummary},
		Steps: []Step{
			{
				Name:  "retry-event-after-failure",
				Kind:  StepRetryEvent,
				Event: messageStep("ignored", "retry-event", 1, "user", model.RoleUser, "retry once", "").Event,
			},
			{Name: "retry-memory-first", Kind: StepAddMemory, Memory: preference},
			{Name: "retry-memory-second", Kind: StepAddMemory, Memory: preference},
			{Name: "reload", Kind: StepReloadSession},
			{Name: "summary-first", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
			{Name: "summary-retry", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
		},
		Fault: FaultSummaryOwner,
	}
}

func messageStep(
	name string,
	logicalID string,
	ordinal int,
	author string,
	role model.Role,
	content string,
	filterKey string,
) Step {
	step := responseEvent(logicalID, ordinal, author, model.Response{
		ID:        "response-" + logicalID,
		Object:    model.ObjectTypeChatCompletion,
		Created:   caseEpoch.Add(time.Duration(ordinal) * time.Second).Unix(),
		Model:     "replay-model",
		Timestamp: caseEpoch.Add(time.Duration(ordinal) * time.Second),
		Done:      true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: role, Content: content},
		}},
	})
	event.WithBranch(filterKey)(step.Event.Event)
	step.Event.Event.FilterKey = filterKey
	step.Name = name
	return step
}

func responseEvent(logicalID string, ordinal int, author string, response model.Response) Step {
	evt := event.NewResponseEvent("invocation-"+logicalID, author, &response)
	evt.Timestamp = caseEpoch.Add(time.Duration(ordinal) * time.Second)
	return Step{
		Name: logicalID,
		Kind: StepAppendEvent,
		Event: &EventInput{
			LogicalID: logicalID,
			Event:     evt,
			Offset:    time.Duration(ordinal) * time.Second,
		},
	}
}

func stateStep(name string, scope StateScope, values session.StateMap, deletes []string) Step {
	return Step{
		Name: name,
		Kind: StepUpdateState,
		State: &StateInput{
			Scope:      scope,
			Values:     values,
			DeleteKeys: deletes,
		},
	}
}

func trackStep(name, track string, ordinal int, payload map[string]any) Step {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return Step{
		Name: name,
		Kind: StepAppendTrack,
		Track: &TrackInput{
			Event: &session.TrackEvent{
				Track:     session.Track(track),
				Payload:   raw,
				Timestamp: caseEpoch.Add(time.Duration(ordinal) * time.Second),
			},
			Offset: time.Duration(ordinal) * time.Second,
		},
	}
}
