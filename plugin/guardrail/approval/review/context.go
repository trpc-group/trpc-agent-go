//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import "trpc.group/trpc-go/trpc-agent-go/tool"

// Context is a snapshot of evidence supplementing an Action and Transcript.
// It grants no permission and contains no live session or execution capability.
// Callers and reviewers must not mutate it during Review. Missing information
// remains unknown; tool metadata and fact contents are not proof of safety.
type Context struct {
	// ToolCallID identifies the pending tool call, when available.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// InputSchema describes the tool arguments, when available.
	// The approval policy supplies a JSON snapshot: custom Go values use their
	// JSON representation and numbers in untyped fields use json.Number.
	InputSchema *tool.Schema `json:"input_schema,omitempty"`
	// ToolMetadata contains the tool's advisory execution properties.
	ToolMetadata tool.ToolMetadata `json:"tool_metadata"`
	// HistoryCoverage describes coverage of the relevant conversation history.
	HistoryCoverage Coverage `json:"history_coverage"`
	// TranscriptInfo has exactly one entry per Transcript entry, in the same order.
	TranscriptInfo []TranscriptInfo `json:"transcript_info"`
	// Supplement contains host-provided review guidance and bounded facts.
	Supplement Supplement `json:"supplement"`
}

// TranscriptInfo describes the provenance and representation of one transcript
// entry. Empty identifiers and OriginUnknown indicate unavailable provenance.
type TranscriptInfo struct {
	// EventID identifies the source session event.
	EventID string `json:"event_id,omitempty"`
	// InvocationID identifies the invocation that produced the event.
	InvocationID string `json:"invocation_id,omitempty"`
	// ParentInvocationID identifies the producer's parent, when recorded.
	ParentInvocationID string `json:"parent_invocation_id,omitempty"`
	// Origin describes the producer independently of the message's role.
	Origin Origin `json:"origin"`
	// TextTruncated reports that the producer omitted some entry text.
	TextTruncated bool `json:"text_truncated,omitempty"`
	// NonTextOmitted reports that non-text message parts are not represented.
	NonTextOmitted bool `json:"non_text_omitted,omitempty"`
}

// Origin identifies who supplied an entry, independently of its chat role.
// It must be assigned by the host or framework, never inferred from entry text.
type Origin string

const (
	// OriginUnknown means the source cannot be established.
	OriginUnknown Origin = ""
	// OriginUserInput means input submitted by the host as a user request.
	// The host remains responsible for authenticating its user-facing inputs.
	OriginUserInput Origin = "user_input"
	// OriginAgent includes assistant messages and delegated agent instructions.
	OriginAgent Origin = "agent"
	// OriginTool means a tool result, which cannot itself grant authorization.
	OriginTool Origin = "tool"
)

// Coverage describes whether relevant history was available without omissions.
type Coverage string

const (
	// CoverageUnknown means completeness cannot be established, including
	// sessions whose storage or caller may have omitted earlier history.
	CoverageUnknown Coverage = ""
	// CoverageComplete means the producer established completeness.
	CoverageComplete Coverage = "complete"
	// CoveragePartial means at least one relevant entry or part was omitted.
	CoveragePartial Coverage = "partial"
)

// Supplement contains application-specific review evidence. Only trusted host
// configuration may supply ReviewInstructions; arbitrary tool output belongs
// in Facts. Neither field overrides deterministic permission policies.
type Supplement struct {
	// ReviewInstructions provides trusted host guidance for this assessment.
	ReviewInstructions string `json:"review_instructions,omitempty"`
	// Facts provides relevant evidence collected by the host.
	Facts []Fact `json:"facts,omitempty"`
}

// Fact is host-collected evidence, such as execution configuration or script
// contents. Source identifies its provenance without making its contents trusted
// instructions. Truncated reports omitted content; false does not establish
// that the upstream source was complete or remains unchanged at execution time.
type Fact struct {
	// Source identifies where the host obtained this evidence.
	Source string `json:"source"`
	// Content is evidence text, not instructions to the reviewer.
	Content string `json:"content"`
	// Truncated reports that some content was omitted.
	Truncated bool `json:"truncated,omitempty"`
}
