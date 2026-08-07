//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

// GetDefaultReplayCases returns 10 required replay test cases.
func GetDefaultReplayCases() []ReplayCase {
	return []ReplayCase{
		{
			ID:          "CASE_01_SINGLE_ROUND",
			Name:        "Single Round Dialogue",
			Description: "Simple user message and assistant response",
			Ops: []ReplayOp{
				{Type: OpAddEvent, SessionID: "sess-01", Role: "user", Content: "Hello Agent"},
				{Type: OpAddEvent, SessionID: "sess-01", Role: "assistant", Content: "Hello User! How can I help?"},
			},
		},
		{
			ID:          "CASE_02_MULTI_ROUND",
			Name:        "Multi-round Dialogue",
			Description: "Multiple turns of dialogue preserving sequence order",
			Ops: []ReplayOp{
				{Type: OpAddEvent, SessionID: "sess-02", Role: "user", Content: "Turn 1"},
				{Type: OpAddEvent, SessionID: "sess-02", Role: "assistant", Content: "Response 1"},
				{Type: OpAddEvent, SessionID: "sess-02", Role: "user", Content: "Turn 2"},
				{Type: OpAddEvent, SessionID: "sess-02", Role: "assistant", Content: "Response 2"},
			},
		},
		{
			ID:          "CASE_03_TOOL_CALL",
			Name:        "Tool Call Conversation",
			Description: "Tool call, arguments extension and tool response event",
			Ops: []ReplayOp{
				{Type: OpAddEvent, SessionID: "sess-03", Role: "user", Content: "What is 2+2?"},
				{Type: OpAddEvent, SessionID: "sess-03", Role: "assistant", Content: "ToolCall: calculator(2+2)"},
				{Type: OpAddEvent, SessionID: "sess-03", Role: "tool", Content: "Result: 4"},
			},
		},
		{
			ID:          "CASE_04_STATE_UPDATES",
			Name:        "State Read/Write/Delete",
			Description: "State key creation, value overwrite, and deletion",
			Ops: []ReplayOp{
				{Type: OpSetState, SessionID: "sess-04", StateKey: "user_name", StateVal: "Alice"},
				{Type: OpSetState, SessionID: "sess-04", StateKey: "user_name", StateVal: "Alice Bob"},
				{Type: OpDeleteState, SessionID: "sess-04", StateKey: "temp_token"},
			},
		},
		{
			ID:          "CASE_05_MEMORY_RW",
			Name:        "Memory Persistence",
			Description: "Writing and retrieving long-term preference memory",
			Ops: []ReplayOp{
				{Type: OpWriteMemory, SessionID: "sess-05", MemoryID: "mem-101", Content: "User prefers concise answers"},
			},
		},
		{
			ID:          "CASE_06_SUMMARY_FILTER_KEY",
			Name:        "Summary Filter-Key Update",
			Description: "Updating summary text with specific filter-key scope",
			Ops: []ReplayOp{
				{Type: OpUpdateSummary, SessionID: "sess-06", FilterKey: "topic_math", Content: "Summary of math questions discussed"},
			},
		},
		{
			ID:          "CASE_07_SUMMARY_TRUNCATION",
			Name:        "Summary Truncation & Compression",
			Description: "Simulating long context compression with retained events",
			Ops: []ReplayOp{
				{Type: OpAddEvent, SessionID: "sess-07", Role: "user", Content: "Long text 1"},
				{Type: OpUpdateSummary, SessionID: "sess-07", FilterKey: "history", Content: "Compressed conversation before turn 5"},
				{Type: OpAddEvent, SessionID: "sess-07", Role: "user", Content: "New question after summary"},
			},
		},
		{
			ID:          "CASE_08_TRACK_EVENTS",
			Name:        "Track Event Telemetry",
			Description: "Logging execution latency and invocation track events",
			Ops: []ReplayOp{
				{Type: OpAddTrack, SessionID: "sess-08", TrackName: "tool_latency_ms:42"},
			},
		},
		{
			ID:          "CASE_09_CONCURRENT_WRITES",
			Name:        "Concurrent Write Ordering",
			Description: "Interleaved sub-agent events in parallel",
			Ops: []ReplayOp{
				{Type: OpAddEvent, SessionID: "sess-09", Role: "agent_a", Content: "Subtask A started"},
				{Type: OpAddEvent, SessionID: "sess-09", Role: "agent_b", Content: "Subtask B started"},
			},
		},
		{
			ID:          "CASE_10_EXCEPTION_RECOVERY",
			Name:        "Exception Recovery & Trap Test",
			Description: "Handling retry and verifying trap injection detection",
			Ops: []ReplayOp{
				{Type: OpAddEvent, SessionID: "sess-10", Role: "user", Content: "Retry question"},
			},
		},
	}
}
