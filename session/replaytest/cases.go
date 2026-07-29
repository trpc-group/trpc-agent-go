//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var standardReplayEpoch = time.Date(
	2030,
	time.January,
	2,
	3,
	4,
	5,
	0,
	time.UTC,
)

// StandardCases returns the ten public replay cases required by the
// consistency benchmark.
func StandardCases() []ReplayCase {
	episodeTime := standardReplayEpoch.Add(24 * time.Hour)
	return []ReplayCase{
		{
			Name:        "single_turn_dialogue",
			Description: "one user message and one assistant text event",
			Key:         standardKey("single-turn"),
			Operations: []Operation{
				appendEvent(userEvent("single-user", 1, "Hello")),
				appendEvent(assistantEvent(
					"single-assistant",
					2,
					"Hello, how can I help?",
				)),
			},
			Expected: Expectations{
				EventCount: intPointer(2),
			},
		},
		{
			Name:        "multi_turn_order",
			Description: "three ordered user and assistant rounds",
			Key:         standardKey("multi-turn"),
			Operations: []Operation{
				appendEvent(userEvent("multi-u1", 1, "Remember project Atlas")),
				appendEvent(assistantEvent("multi-a1", 2, "I will remember Atlas")),
				appendEvent(userEvent("multi-u2", 3, "What is the project?")),
				appendEvent(assistantEvent("multi-a2", 4, "The project is Atlas")),
				appendEvent(userEvent("multi-u3", 5, "Mark it active")),
				appendEvent(assistantEvent("multi-a3", 6, "Atlas is active")),
			},
			Expected: Expectations{
				EventCount: intPointer(6),
			},
		},
		{
			Name:        "tool_call_round_trip",
			Description: "tool call, tool response, args extension, and final text",
			Key:         standardKey("tool-call"),
			Operations: []Operation{
				appendEvent(userEvent("tool-u1", 1, "Weather in Shenzhen?")),
				appendEvent(OperationEvent(EventInput{
					LogicalID:    "tool-call",
					InvocationID: "inv-tool",
					Author:       "assistant",
					Role:         model.RoleAssistant,
					Branch:       "agent/weather",
					FilterKey:    "agent/weather",
					Tag:          "tool;weather",
					Timestamp:    standardTime(2),
					Sequence:     2,
					ToolCalls: []ToolCallInput{{
						ID:   "call-weather",
						Type: "function",
						Name: "get_weather",
						Arguments: rawJSON(
							map[string]any{
								"city": "Shenzhen",
								"unit": "celsius",
							},
						),
					}},
				})),
				appendEvent(OperationEvent(EventInput{
					LogicalID:    "tool-response",
					InvocationID: "inv-tool",
					Author:       "tool",
					Role:         model.RoleTool,
					Content:      `{"temperature":28,"condition":"sunny"}`,
					ToolID:       "call-weather",
					ToolName:     "get_weather",
					Branch:       "agent/weather",
					FilterKey:    "agent/weather",
					Tag:          "weather;tool",
					Timestamp:    standardTime(3),
					Sequence:     3,
					Extensions: map[string]json.RawMessage{
						event.ToolCallArgsExtensionKey: rawJSON(
							map[string]any{
								"call-weather": map[string]any{
									"city": "Shenzhen",
									"unit": "celsius",
								},
							},
						),
					},
				})),
				appendEvent(OperationEvent(EventInput{
					LogicalID:    "tool-final",
					InvocationID: "inv-tool",
					Author:       "assistant",
					Role:         model.RoleAssistant,
					Content:      "It is 28 C and sunny in Shenzhen.",
					Branch:       "agent/weather",
					FilterKey:    "agent/weather",
					Timestamp:    standardTime(4),
					Sequence:     4,
					StateDelta: map[string]json.RawMessage{
						"last_city": rawJSON("Shenzhen"),
					},
				})),
			},
			Expected: Expectations{
				EventCount: intPointer(4),
			},
		},
		{
			Name:         "state_lifecycle",
			Description:  "session overwrite and clear plus app and user deletion",
			Key:          standardKey("state"),
			InitialState: session.StateMap{"counter": []byte(`0`)},
			Operations: []Operation{
				stateSet(StateScopeSession, "counter", 1),
				stateSet(StateScopeSession, "counter", 2),
				stateSet(StateScopeSession, "temporary", "value"),
				stateDelete(StateScopeSession, "temporary"),
				stateSet(StateScopeApp, "locale", "zh-CN"),
				stateDelete(StateScopeApp, "locale"),
				stateSet(StateScopeUser, "theme", "dark"),
				stateSet(StateScopeUser, "theme", "light"),
				stateDelete(StateScopeUser, "theme"),
			},
			Expected: Expectations{
				EventCount: intPointer(0),
			},
		},
		{
			Name:        "memory_lifecycle",
			Description: "fact and episode add, search, update, and delete",
			Key:         standardKey("memory"),
			Operations: []Operation{
				memoryAdd(
					"preference",
					"User prefers Go for backend services",
					[]string{"preference", "language"},
					&memory.Metadata{Kind: memory.KindFact},
				),
				memoryAdd(
					"episode",
					"User completed the replay harness task",
					[]string{"task", "experience"},
					&memory.Metadata{
						Kind:         memory.KindEpisode,
						EventTime:    &episodeTime,
						Participants: []string{"user", "assistant"},
						Location:     "Shenzhen",
					},
				),
				memorySearch("backend Go", 10),
				memoryUpdate(
					"preference",
					"User prefers Go and SQLite for backend services",
					[]string{"language", "preference"},
					&memory.Metadata{Kind: memory.KindFact},
				),
				memoryDelete("episode"),
				memorySearch("SQLite backend", 10),
			},
			AllowedDiffs: []AllowedDiffRule{{
				PathPrefix:        "memory_searches",
				AbsoluteTolerance: 0.001,
				Explanation:       "similarity precision may vary by backend",
			}},
			Expected: Expectations{
				EventCount:  intPointer(0),
				MemoryCount: intPointer(1),
			},
		},
		{
			Name:        "summary_update",
			Description: "filter-key summary generation and rolling update",
			Key:         standardKey("summary-update"),
			Operations: []Operation{
				appendEvent(filteredUserEvent(
					"summary-u1",
					1,
					"Research SQLite semantics",
					"agent/research",
				)),
				appendEvent(filteredAssistantEvent(
					"summary-a1",
					2,
					"SQLite uses transactional persistence",
					"agent/research",
				)),
				summaryGenerate("agent/research", true),
				appendEvent(filteredUserEvent(
					"summary-u2",
					3,
					"Also compare in-memory behavior",
					"agent/research",
				)),
				appendEvent(filteredAssistantEvent(
					"summary-a2",
					4,
					"In-memory behavior now matches SQLite",
					"agent/research",
				)),
				summaryGenerate("agent/research", true),
			},
			Expected: Expectations{
				EventCount:     intPointer(4),
				SummaryFilters: []string{"agent/research"},
			},
		},
		{
			Name:        "summary_event_truncation",
			Description: "summary plus retained and post-cutoff events reconstruct context",
			Key:         standardKey("summary-truncation"),
			EventLimit:  4,
			Operations: []Operation{
				appendEvent(filteredUserEvent(
					"long-u1",
					1,
					"Long conversation turn one",
					"agent/long",
				)),
				appendEvent(filteredAssistantEvent(
					"long-a1",
					2,
					"Long answer one",
					"agent/long",
				)),
				appendEvent(filteredUserEvent(
					"long-u2",
					3,
					"Long conversation turn two",
					"agent/long",
				)),
				appendEvent(filteredAssistantEvent(
					"long-a2",
					4,
					"Long answer two",
					"agent/long",
				)),
				summaryGenerate("agent/long", true),
				appendEvent(filteredUserEvent(
					"long-u3",
					5,
					"New turn after compression",
					"agent/long",
				)),
				appendEvent(filteredAssistantEvent(
					"long-a3",
					6,
					"New answer after compression",
					"agent/long",
				)),
			},
			Expected: Expectations{
				EventCount:     intPointer(4),
				SummaryFilters: []string{"agent/long"},
			},
		},
		{
			Name:        "track_observability",
			Description: "tool timing, subtask status, and failure observations",
			Key:         standardKey("track"),
			Operations: []Operation{
				trackAppend(
					"tools",
					1,
					map[string]any{
						"event_type":    "started",
						"invocation_id": "inv-track",
						"tool":          "search",
					},
				),
				trackAppend(
					"tools",
					2,
					map[string]any{
						"duration_ms":   12.345,
						"event_type":    "completed",
						"invocation_id": "inv-track",
						"tool":          "search",
					},
				),
				trackAppend(
					"subtasks",
					3,
					map[string]any{
						"event_type":    "status",
						"invocation_id": "inv-child",
						"status":        "running",
					},
				),
				trackAppend(
					"errors",
					4,
					map[string]any{
						"error":         "tool timeout",
						"event_type":    "failed",
						"invocation_id": "inv-child",
					},
				),
			},
			Expected: Expectations{
				EventCount: intPointer(0),
				TrackEventCount: map[string]int{
					"errors":   1,
					"subtasks": 1,
					"tools":    2,
				},
			},
		},
		{
			Name:                   "concurrent_interleaving",
			Description:            "concurrent sub-agent events with logical ordering",
			Key:                    standardKey("concurrent"),
			CanonicalizeEventOrder: true,
			Operations: []Operation{
				appendEvent(userEvent(
					"parallel-user",
					1,
					"Run subtasks A and B",
				)),
				{
					Kind: OperationParallel,
					Parallel: []Operation{
						appendEvent(OperationEvent(EventInput{
							LogicalID:    "parallel-a-start",
							InvocationID: "inv-a",
							Author:       "subagent-a",
							Role:         model.RoleAssistant,
							Content:      "subtask A started",
							Branch:       "root/a",
							FilterKey:    "root/a",
							Timestamp:    standardTime(4),
							Sequence:     2,
						})),
						appendEvent(OperationEvent(EventInput{
							LogicalID:    "parallel-b-start",
							InvocationID: "inv-b",
							Author:       "subagent-b",
							Role:         model.RoleAssistant,
							Content:      "subtask B started",
							Branch:       "root/b",
							FilterKey:    "root/b",
							Timestamp:    standardTime(1),
							Sequence:     3,
						})),
						appendEvent(OperationEvent(EventInput{
							LogicalID:    "parallel-a-done",
							InvocationID: "inv-a",
							Author:       "subagent-a",
							Role:         model.RoleAssistant,
							Content:      "subtask A completed",
							Branch:       "root/a",
							FilterKey:    "root/a",
							Timestamp:    standardTime(3),
							Sequence:     4,
						})),
						appendEvent(OperationEvent(EventInput{
							LogicalID:    "parallel-b-done",
							InvocationID: "inv-b",
							Author:       "subagent-b",
							Role:         model.RoleAssistant,
							Content:      "subtask B completed",
							Branch:       "root/b",
							FilterKey:    "root/b",
							Timestamp:    standardTime(2),
							Sequence:     5,
						})),
					},
				},
			},
			AllowedDiffs: []AllowedDiffRule{{
				PathPrefix:  "observed_event_order",
				Explanation: "concurrent commit order is nondeterministic",
			}},
			Expected: Expectations{
				EventCount: intPointer(5),
			},
		},
		{
			Name:        "failure_recovery",
			Description: "pre-commit failures, retries, and idempotent memory writes",
			Key:         standardKey("recovery"),
			Operations: []Operation{
				withRetry(
					appendEvent(filteredUserEvent(
						"recovery-u1",
						1,
						"Persist this request",
						"agent/recovery",
					)),
					2,
					1,
				),
				appendEvent(filteredAssistantEvent(
					"recovery-a1",
					2,
					"Request persisted once",
					"agent/recovery",
				)),
				withRetry(
					stateSet(StateScopeSession, "status", "recovered"),
					2,
					1,
				),
				withRetry(
					memoryAdd(
						"recovery-memory",
						"Retry-safe task experience",
						[]string{"recovery", "task"},
						&memory.Metadata{Kind: memory.KindFact},
					),
					2,
					1,
				),
				memoryAdd(
					"recovery-memory",
					"Retry-safe task experience",
					[]string{"recovery", "task"},
					&memory.Metadata{Kind: memory.KindFact},
				),
				withRetry(
					summaryGenerate("agent/recovery", true),
					2,
					1,
				),
			},
			Expected: Expectations{
				EventCount:     intPointer(2),
				MemoryCount:    intPointer(1),
				SummaryFilters: []string{"agent/recovery"},
			},
		},
	}
}

// SummaryFilterKeys returns sorted non-empty summary filter keys used by a
// case, including keys nested in parallel operations.
func SummaryFilterKeys(replayCase ReplayCase) []string {
	keys := make(map[string]struct{})
	var collect func([]Operation)
	collect = func(operations []Operation) {
		for _, operation := range operations {
			if operation.Summary != nil && operation.Summary.FilterKey != "" {
				keys[operation.Summary.FilterKey] = struct{}{}
			}
			collect(operation.Parallel)
		}
	}
	collect(replayCase.Operations)
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// OperationEvent wraps an event input as an append operation.
func OperationEvent(input EventInput) Operation {
	return Operation{
		Kind:  OperationAppendEvent,
		Event: &input,
	}
}

func appendEvent(operation Operation) Operation {
	return operation
}

func userEvent(id string, sequence int, content string) Operation {
	return OperationEvent(EventInput{
		LogicalID:    id,
		InvocationID: "inv-" + id,
		Author:       "user",
		Role:         model.RoleUser,
		Content:      content,
		Timestamp:    standardTime(sequence),
		Sequence:     sequence,
	})
}

func assistantEvent(id string, sequence int, content string) Operation {
	return OperationEvent(EventInput{
		LogicalID:    id,
		InvocationID: "inv-" + id,
		Author:       "assistant",
		Role:         model.RoleAssistant,
		Content:      content,
		Timestamp:    standardTime(sequence),
		Sequence:     sequence,
	})
}

func filteredUserEvent(
	id string,
	sequence int,
	content string,
	filterKey string,
) Operation {
	operation := userEvent(id, sequence, content)
	operation.Event.Branch = filterKey
	operation.Event.FilterKey = filterKey
	return operation
}

func filteredAssistantEvent(
	id string,
	sequence int,
	content string,
	filterKey string,
) Operation {
	operation := assistantEvent(id, sequence, content)
	operation.Event.Branch = filterKey
	operation.Event.FilterKey = filterKey
	return operation
}

func stateSet(scope StateScope, key string, value any) Operation {
	return Operation{
		Kind: OperationSetState,
		State: &StateInput{
			Scope: scope,
			Key:   key,
			Value: rawJSON(value),
		},
	}
}

func stateDelete(scope StateScope, key string) Operation {
	return Operation{
		Kind: OperationDeleteState,
		State: &StateInput{
			Scope: scope,
			Key:   key,
		},
	}
}

func memoryAdd(
	ref string,
	content string,
	topics []string,
	metadata *memory.Metadata,
) Operation {
	return Operation{
		Kind: OperationAddMemory,
		Memory: &MemoryInput{
			Ref:      ref,
			Content:  content,
			Topics:   topics,
			Metadata: metadata,
		},
	}
}

func memoryUpdate(
	ref string,
	content string,
	topics []string,
	metadata *memory.Metadata,
) Operation {
	return Operation{
		Kind: OperationUpdateMemory,
		Memory: &MemoryInput{
			Ref:      ref,
			Content:  content,
			Topics:   topics,
			Metadata: metadata,
		},
	}
}

func memoryDelete(ref string) Operation {
	return Operation{
		Kind:   OperationDeleteMemory,
		Memory: &MemoryInput{Ref: ref},
	}
}

func memorySearch(query string, limit int) Operation {
	return Operation{
		Kind: OperationSearchMemory,
		Memory: &MemoryInput{
			Query: query,
			Limit: limit,
		},
	}
}

func summaryGenerate(filterKey string, force bool) Operation {
	return Operation{
		Kind: OperationGenerateSummary,
		Summary: &SummaryInput{
			FilterKey: filterKey,
			Force:     force,
		},
	}
}

func trackAppend(name string, sequence int, payload any) Operation {
	return Operation{
		Kind: OperationAppendTrack,
		Track: &TrackInput{
			Name:      name,
			Payload:   rawJSON(payload),
			Timestamp: standardTime(sequence),
		},
	}
}

func withRetry(
	operation Operation,
	attempts int,
	failures int,
) Operation {
	operation.Retry = RetryPolicy{
		Attempts:           attempts,
		FailBeforeAttempts: failures,
	}
	return operation
}

func standardKey(id string) session.Key {
	return session.Key{
		AppName:   "replay-" + id,
		UserID:    "user-" + id,
		SessionID: id,
	}
}

func standardTime(sequence int) time.Time {
	return standardReplayEpoch.Add(time.Duration(sequence) * time.Second)
}

func rawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func intPointer(value int) *int {
	return &value
}
