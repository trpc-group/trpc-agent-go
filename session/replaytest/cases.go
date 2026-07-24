//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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

// PublicCases returns the canonical lightweight replay suite.
func PublicCases() []ReplayCase {
	return []ReplayCase{
		singleTurnCase(),
		multiTurnCase(),
		toolCallCase(),
		stateSetOverwriteCase(),
		memoryCase(),
		summaryCase(),
		summaryTruncationCase(),
		trackCase(),
		interleavedCase(),
		retryRecoveryCase(),
		stateDeleteClearCase(),
		memoryFactCase(),
		duplicateEventRecoveryCase(),
		repeatMemoryStoreCase(),
		partialEventCase(),
		eventMetadataCase(),
		multiPartEventCase(),
		memoryAfterSummaryCase(),
		memoryMultiResultCase(),
		memoryLifecycleCase(),
		sessionTTLCase(),
	}
}

func baseKey(name string) session.Key {
	return session.Key{
		AppName:   "replay-app",
		UserID:    "user-42",
		SessionID: "sess-" + name,
	}
}

func singleTurnCase() ReplayCase {
	return ReplayCase{
		Name: "01_single_turn_dialogue",
		Key:  baseKey("single-turn"),
		Operations: []Operation{
			appendMsg("u1", "user", model.RoleUser, "hello"),
			appendMsg("a1", "assistant", model.RoleAssistant, "hi, how can I help?"),
		},
	}
}

func multiTurnCase() ReplayCase {
	return ReplayCase{
		Name: "02_multi_turn_ordering",
		Key:  baseKey("multi-turn"),
		Operations: []Operation{
			appendMsg("u1", "user", model.RoleUser, "book a train"),
			appendMsg("a1", "assistant", model.RoleAssistant, "from where?"),
			appendMsg("u2", "user", model.RoleUser, "Shenzhen to Shanghai"),
			appendMsg("a2", "assistant", model.RoleAssistant, "tomorrow morning works"),
			appendMsg("u3", "user", model.RoleUser, "choose the earliest"),
			appendMsg("a3", "assistant", model.RoleAssistant, "earliest option saved"),
		},
	}
}

func toolCallCase() ReplayCase {
	args := map[string]any{"city": "Shenzhen", "unit": "celsius"}
	return ReplayCase{
		Name: "03_tool_call_and_response",
		Key:  baseKey("tool-call"),
		Operations: []Operation{
			appendMsg("u1", "user", model.RoleUser, "weather?"),
			{
				Kind: OpAppendEvent,
				Event: &EventSpec{
					LogicalID:    "tool-call-a1",
					InvocationID: "inv-tool",
					Author:       "assistant",
					Role:         model.RoleAssistant,
					ToolCalls: []ToolCallSpec{{
						ID:        "call-weather-1",
						Name:      "get_weather",
						Arguments: args,
					}},
					Extensions: map[string]any{
						event.ToolCallArgsExtensionKey: map[string]any{"call-weather-1": args},
					},
				},
			},
			{
				Kind: OpAppendEvent,
				Event: &EventSpec{
					LogicalID:    "tool-result-1",
					InvocationID: "inv-tool",
					Author:       "tool",
					Role:         model.RoleTool,
					Content:      `{"temperature":31,"condition":"sunny"}`,
					ToolID:       "call-weather-1",
					ToolName:     "get_weather",
					Object:       model.ObjectTypeToolResponse,
				},
			},
			appendMsg("a2", "assistant", model.RoleAssistant, "It is sunny and 31 C."),
		},
	}
}

func stateSetOverwriteCase() ReplayCase {
	return ReplayCase{
		Name: "04_state_set_overwrite",
		Key:  baseKey("state-set"),
		Operations: []Operation{
			setState("locale", raw(`"zh-CN"`)),
			setState("locale", raw(`"en-US"`)),
			setState("temp:step", raw(`2`)),
			setState("profile", raw(`{"tier":"gold","active":true}`)),
		},
	}
}

func memoryCase() ReplayCase {
	when := time.Date(2026, 1, 3, 8, 0, 0, 0, time.UTC)
	return ReplayCase{
		Name: "05_memory_write_read",
		Key:  baseKey("memory"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "preference", Query: "concise Go", Limit: 5},
			{Name: "episode", Query: "debugged replay", Limit: 5},
		},
		Operations: []Operation{
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "pref",
					Content: "User prefers concise Go code reviews.",
					Topics:  []string{"preference", "go"},
					Metadata: &memory.Metadata{
						Kind: memory.KindFact,
					},
				},
			},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "episode",
					Content: "User debugged replay consistency on 2026-01-03.",
					Topics:  []string{"task", "replay"},
					Metadata: &memory.Metadata{
						Kind:         memory.KindEpisode,
						EventTime:    &when,
						Participants: []string{"user", "agent"},
						Location:     "local-workspace",
					},
				},
			},
		},
	}
}

func summaryCase() ReplayCase {
	return ReplayCase{
		Name: "06_summary_filter_key_update",
		Key:  baseKey("summary"),
		Operations: []Operation{
			appendFiltered("u1", "user", model.RoleUser, "billing question", "support/billing"),
			appendFiltered("a1", "assistant", model.RoleAssistant, "billing answer", "support/billing"),
			writeSummary("support/billing"),
			appendFiltered("u2", "user", model.RoleUser, "refund follow-up", "support/billing/refund"),
			appendFiltered("a2", "assistant", model.RoleAssistant, "refund answer", "support/billing/refund"),
			writeSummary("support/billing"),
		},
	}
}

func summaryTruncationCase() ReplayCase {
	return ReplayCase{
		Name:           "07_summary_with_event_truncation",
		Key:            baseKey("summary-truncate"),
		ReadEventLimit: 2,
		Operations: []Operation{
			appendMsg("u1", "user", model.RoleUser, "long turn 1"),
			appendMsg("a1", "assistant", model.RoleAssistant, "long answer 1"),
			appendMsg("u2", "user", model.RoleUser, "long turn 2"),
			appendMsg("a2", "assistant", model.RoleAssistant, "long answer 2"),
			writeSummary(session.SummaryFilterKeyAllContents),
			appendMsg("u3", "user", model.RoleUser, "new turn after compression"),
			appendMsg("a3", "assistant", model.RoleAssistant, "new answer after compression"),
		},
	}
}

func trackCase() ReplayCase {
	return ReplayCase{
		Name: "08_track_events",
		Key:  baseKey("track"),
		Operations: []Operation{
			appendMsg("u1", "user", model.RoleUser, "run lookup"),
			trackAt(0, "tool.lookup", map[string]any{
				"type":        "start",
				"invocation":  "inv-track",
				"duration_ms": 0.13,
			}),
			trackAt(1, "tool.lookup", map[string]any{
				"type":        "finish",
				"invocation":  "inv-track",
				"duration_ms": 41.72891,
				"status":      "ok",
			}),
			trackAt(2, "subtask.plan", map[string]any{
				"type":       "error",
				"invocation": "inv-track",
				"error":      "temporary tool timeout",
			}),
		},
	}
}

func interleavedCase() ReplayCase {
	return ReplayCase{
		Name: "09_interleaved_out_of_order_writes",
		Key:  baseKey("interleaved"),
		Operations: []Operation{
			appendFiltered("u1", "user", model.RoleUser, "parallel tasks", "root"),
			{
				Kind: OpConcurrent,
				Concurrent: []Operation{
					appendFiltered("a-tool-2", "assistant", model.RoleAssistant, "sub agent B ready", "root/toolB"),
					appendFiltered("a-tool-1", "assistant", model.RoleAssistant, "sub agent A ready", "root/toolA"),
					trackAt(10, "parallel.toolB", map[string]any{"type": "finish", "invocation": "toolB", "elapsed_ms": 8.4}),
					trackAt(11, "parallel.toolA", map[string]any{"type": "finish", "invocation": "toolA", "elapsed_ms": 11.1}),
				},
			},
			appendFiltered("a-final", "assistant", model.RoleAssistant, "merged result", "root"),
		},
	}
}

func retryRecoveryCase() ReplayCase {
	return ReplayCase{
		Name: "10_retry_failure_recovery",
		Key:  baseKey("retry"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "retry", Query: "retry duplicate", Limit: 5},
		},
		Operations: []Operation{
			setState("phase", raw(`"started"`)),
			{
				Kind: OpRetryEvent,
				Event: &EventSpec{
					LogicalID:    "retry-user-1",
					InvocationID: "inv-retry",
					Author:       "user",
					Role:         model.RoleUser,
					Content:      "please retry safely",
					StateDelta: map[string]json.RawMessage{
						"phase": raw(`"retried"`),
					},
				},
			},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "retry-note",
					Content: "Retry should not duplicate recovered events.",
					Topics:  []string{"recovery"},
				},
			},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "retry-note",
					Content: "Retry should not duplicate recovered events.",
					Topics:  []string{"recovery"},
				},
			},
			writeSummary(session.SummaryFilterKeyAllContents),
			{
				Kind:        OpUnsupportedProbe,
				Unsupported: CapabilityEventPage,
			},
		},
	}
}

func stateDeleteClearCase() ReplayCase {
	return ReplayCase{
		Name: "11_state_delete_clear_semantics",
		Key:  baseKey("state-delete-clear"),
		Operations: []Operation{
			setState("locale", raw(`"zh-CN"`)),
			setState("temp:step", raw(`2`)),
			deleteState("temp:step"),
			setState("profile", raw(`{"tier":"gold","active":true}`)),
			clearState(),
			setState("locale", raw(`"en-US"`)),
		},
	}
}

func memoryFactCase() ReplayCase {
	return ReplayCase{
		Name: "12_memory_fact_write_read",
		Key:  baseKey("memory-fact"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "fact", Query: "storage consistency", Limit: 5},
		},
		Operations: []Operation{
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "fact",
					Content: "User works on storage backend consistency.",
					Topics:  []string{"fact", "storage"},
					Metadata: &memory.Metadata{
						Kind: memory.KindFact,
					},
				},
			},
		},
	}
}

func duplicateEventRecoveryCase() ReplayCase {
	return ReplayCase{
		Name:        "13_duplicate_event_detection",
		Description: "keeps a clean partial-to-final retry path and lets snapshot validation catch duplicate final events",
		Key:         baseKey("duplicate-event"),
		Operations: []Operation{
			{
				Kind: OpRetryEvent,
				Event: &EventSpec{
					LogicalID:    "dupe-user",
					InvocationID: "inv-dupe",
					Author:       "user",
					Role:         model.RoleUser,
					Content:      "this retry should appear once",
				},
			},
			appendMsg("dupe-assistant", "assistant", model.RoleAssistant, "deduplicated"),
		},
	}
}

func repeatMemoryStoreCase() ReplayCase {
	return ReplayCase{
		Name: "14_repeat_memory_store",
		Key:  baseKey("repeat-memory"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "repeat", Query: "idempotent", Limit: 5},
		},
		Operations: []Operation{
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "repeat",
					Content: "Repeated stores should remain idempotent.",
					Topics:  []string{"idempotency"},
				},
			},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "repeat",
					Content: "Repeated stores should remain idempotent.",
					Topics:  []string{"idempotency"},
				},
			},
		},
	}
}

func partialEventCase() ReplayCase {
	return ReplayCase{
		Name: "15_partial_event_not_persisted",
		Key:  baseKey("partial-event"),
		Operations: []Operation{
			{
				Kind: OpAppendEvent,
				Event: &EventSpec{
					LogicalID:    "partial-a1",
					InvocationID: "inv-partial",
					Author:       "assistant",
					Role:         model.RoleAssistant,
					Content:      "streaming chunk",
					Partial:      true,
				},
			},
			appendMsg("partial-u1", "user", model.RoleUser, "finish the streamed answer"),
			appendMsg("partial-final", "assistant", model.RoleAssistant, "final streamed answer"),
		},
	}
}

func eventMetadataCase() ReplayCase {
	return ReplayCase{
		Name: "16_event_metadata_roundtrip",
		Key:  baseKey("event-metadata"),
		Operations: []Operation{
			{
				Kind: OpAppendEvent,
				Event: &EventSpec{
					LogicalID:          "meta-u1",
					InvocationID:       "inv-meta",
					ParentInvocationID: "parent-meta",
					ParentMetadata: &event.ParentInvocationMetadata{
						TriggerType: event.TriggerTypeToolCall,
						TriggerID:   "call-meta-parent",
						TriggerName: "metadata_probe",
					},
					Author:             "user",
					Role:               model.RoleUser,
					Content:            "preserve metadata",
					Branch:             "root/metadata",
					Tag:                "audit;replay",
					FilterKey:          "root/metadata",
					RequiresCompletion: true,
					LongRunningToolIDs: map[string]struct{}{
						"call-meta-parent": {},
					},
					StateDelta: map[string]json.RawMessage{
						"meta:last": raw(`{"step":1,"ok":true}`),
					},
					Extensions: map[string]any{
						"replay.metadata": map[string]any{
							"visible": true,
							"flags":   []string{"roundtrip", "stable"},
						},
					},
				},
			},
		},
	}
}

func multiPartEventCase() ReplayCase {
	args := map[string]any{"path": "/tmp/report.txt", "mode": "summary"}
	return ReplayCase{
		Name: "17_multi_part_event_roundtrip",
		Key:  baseKey("multi-part"),
		Operations: []Operation{
			appendMsg("multi-u1", "user", model.RoleUser, "combine text and tool evidence"),
			{
				Kind: OpAppendEvent,
				Event: &EventSpec{
					LogicalID:    "multi-a1",
					InvocationID: "inv-multi",
					Author:       "assistant",
					Role:         model.RoleAssistant,
					Content:      "I will inspect the file.",
					ToolCalls: []ToolCallSpec{{
						ID:        "call-read-1",
						Name:      "read_file",
						Arguments: args,
					}},
					Extensions: map[string]any{
						"replay.parts": []map[string]any{
							{"kind": "text", "text": "I will inspect the file."},
							{"kind": "function_call", "name": "read_file", "args": args},
						},
					},
				},
			},
			{
				Kind: OpAppendEvent,
				Event: &EventSpec{
					LogicalID:    "multi-tool-1",
					InvocationID: "inv-multi",
					Author:       "tool",
					Role:         model.RoleTool,
					Content:      `{"lines":12,"status":"ok"}`,
					ToolID:       "call-read-1",
					ToolName:     "read_file",
					Object:       model.ObjectTypeToolResponse,
				},
			},
		},
	}
}

func memoryAfterSummaryCase() ReplayCase {
	return ReplayCase{
		Name: "18_memory_after_summary_truncation",
		Key:  baseKey("memory-after-summary"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "after-summary", Query: "summary memory visible", Limit: 5},
		},
		Operations: []Operation{
			appendMsg("mas-u1", "user", model.RoleUser, "remember this before summary"),
			appendMsg("mas-a1", "assistant", model.RoleAssistant, "noted"),
			writeSummary(session.SummaryFilterKeyAllContents),
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "after-summary",
					Content: "Memory written after summary must remain visible.",
					Topics:  []string{"summary", "memory"},
				},
			},
			appendMsg("mas-u2", "user", model.RoleUser, "continue after summary"),
			appendMsg("mas-a2", "assistant", model.RoleAssistant, "context restored"),
		},
	}
}

func memoryMultiResultCase() ReplayCase {
	return ReplayCase{
		Name: "19_memory_multi_result_recall",
		Key:  baseKey("memory-multi-result"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "multi", Query: "replay", Limit: 10},
		},
		Operations: []Operation{
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "pref",
					Content: "User prefers deterministic replay tests.",
					Topics:  []string{"preference", "replay"},
				},
			},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "fact",
					Content: "User evaluates session summary filter keys.",
					Topics:  []string{"summary", "filter"},
				},
			},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "task",
					Content: "User compares InMemory and SQLite replay output.",
					Topics:  []string{"sqlite", "comparison"},
				},
			},
		},
	}
}

func memoryLifecycleCase() ReplayCase {
	return ReplayCase{
		Name: "20_memory_update_delete_clear_lifecycle",
		Key:  baseKey("memory-lifecycle"),
		MemoryQueries: []MemoryQuerySpec{
			{Name: "final", Query: "final memory survives", Limit: 5},
			{Name: "deleted", Query: "deterministic replay tests", Limit: 5},
		},
		Operations: []Operation{
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "pref",
					Content: "User likes deterministic tests.",
					Topics:  []string{"tests"},
				},
			},
			{
				Kind: OpUpdateMemory,
				Memory: &MemorySpec{
					ID:      "pref",
					Content: "User likes deterministic replay tests.",
					Topics:  []string{"tests", "replay"},
				},
			},
			{Kind: OpDeleteMemory, Memory: &MemorySpec{ID: "pref"}},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "task",
					Content: "Keep replay harness lightweight.",
					Topics:  []string{"task"},
				},
			},
			{Kind: OpClearMemory},
			{
				Kind: OpAddMemory,
				Memory: &MemorySpec{
					ID:      "final",
					Content: "Final memory survives lifecycle case.",
					Topics:  []string{"final"},
				},
			},
		},
	}
}

func sessionTTLCase() ReplayCase {
	return ReplayCase{
		Name: "21_session_ttl_expiration",
		Key:  baseKey("session-ttl"),
		Operations: []Operation{
			appendMsg("ttl-u1", "user", model.RoleUser, "create short lived session"),
			appendMsg("ttl-a1", "assistant", model.RoleAssistant, "session ttl probe requested"),
			{Kind: OpTTLProbe},
		},
	}
}

func appendMsg(id, author string, role model.Role, content string) Operation {
	return Operation{
		Kind: OpAppendEvent,
		Event: &EventSpec{
			LogicalID:    id,
			InvocationID: "inv-" + id,
			Author:       author,
			Role:         role,
			Content:      content,
		},
	}
}

func appendFiltered(id, author string, role model.Role, content, filterKey string) Operation {
	op := appendMsg(id, author, role, content)
	op.Event.FilterKey = filterKey
	op.Event.Branch = filterKey
	op.Event.Tag = "replay"
	return op
}

func setState(key string, value json.RawMessage) Operation {
	return Operation{Kind: OpSetState, State: &StateSpec{Key: key, Value: value}}
}

func deleteState(key string) Operation {
	return Operation{Kind: OpDeleteState, State: &StateSpec{Key: key}}
}

func clearState() Operation {
	return Operation{Kind: OpClearState}
}

func writeSummary(filterKey string) Operation {
	return Operation{Kind: OpWriteSummary, Summary: &SummarySpec{FilterKey: filterKey, Force: true}}
}

func track(name session.Track, payload map[string]any) Operation {
	return Operation{Kind: OpAppendTrack, Track: &TrackSpec{Name: name, Payload: payload}}
}

func trackAt(sequence int, name session.Track, payload map[string]any) Operation {
	return Operation{
		Kind: OpAppendTrack,
		Track: &TrackSpec{
			Name:      name,
			Payload:   payload,
			Timestamp: deterministicEventTime(sequence),
		},
	}
}

func raw(s string) json.RawMessage {
	return json.RawMessage(s)
}
