//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestPublicCases(t *testing.T) {
	cases := PublicCases()
	if len(cases) < 10 {
		t.Fatalf("PublicCases() returned %d cases, want at least 10", len(cases))
	}
	names := make(map[string]struct{}, len(cases))
	faults := make(map[FaultKind]struct{}, len(cases))
	for _, replayCase := range cases {
		if err := validateCase(replayCase); err != nil {
			t.Fatalf("case %q is invalid: %v", replayCase.Name, err)
		}
		if _, ok := names[replayCase.Name]; ok {
			t.Fatalf("duplicate case %q", replayCase.Name)
		}
		names[replayCase.Name] = struct{}{}
		if replayCase.Fault == "" {
			t.Fatalf("case %q has no acceptance fault", replayCase.Name)
		}
		faults[replayCase.Fault] = struct{}{}
	}
	if len(faults) < 10 {
		t.Fatalf("PublicCases() exercise %d distinct faults, want at least 10", len(faults))
	}
}

func TestCaseValidationRejectsAmbiguousInputs(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	validStep := messageStep("valid", "valid", 1, "user", "user", "hello", "")
	trackPayloadCase := func(name string, payload json.RawMessage) Case {
		return Case{
			Name:     name,
			Requires: []Capability{CapabilitySession, CapabilityTrack},
			Steps: []Step{{
				Name: "track",
				Kind: StepAppendTrack,
				Track: &TrackInput{Event: &session.TrackEvent{
					Track:   "tools",
					Payload: payload,
				}},
			}},
		}
	}
	eventExtensionCase := func(name string, raw json.RawMessage) Case {
		step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
		step.Event.Event.Extensions = map[string]json.RawMessage{"custom.example/v1": raw}
		return Case{
			Name:     name,
			Requires: []Capability{CapabilitySession},
			Steps:    []Step{step},
		}
	}
	toolArgumentsCase := func(name string, arguments []byte, delta bool) Case {
		message := model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "tool",
					Arguments: arguments,
				},
			}},
		}
		choice := model.Choice{Message: message}
		if delta {
			choice = model.Choice{Delta: message}
		}
		step := responseEvent("tool-call", 1, "assistant", model.Response{
			Choices: []model.Choice{choice},
		})
		return Case{
			Name:     name,
			Requires: []Capability{CapabilitySession},
			Steps:    []Step{step},
		}
	}
	tests := []Case{
		{
			Name:       "unknown-order",
			Requires:   []Capability{CapabilitySession},
			EventOrder: "unordered",
			Steps:      []Step{validStep},
		},
		{
			Name:     "multiple-payloads",
			Requires: []Capability{CapabilitySession},
			Steps: []Step{{
				Name:  "ambiguous",
				Kind:  StepAppendEvent,
				Event: validStep.Event,
				State: &StateInput{Scope: StateScopeSession},
			}},
		},
		{
			Name:     "session-delete",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps: []Step{{
				Name:  "delete",
				Kind:  StepUpdateState,
				State: &StateInput{Scope: StateScopeSession, DeleteKeys: []string{"key"}},
			}},
		},
		{
			Name:     "empty-state-step",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps: []Step{{
				Name:  "empty-state",
				Kind:  StepUpdateState,
				State: &StateInput{Scope: StateScopeSession},
			}},
		},
		{
			Name:     "missing-session-capability",
			Requires: nil,
			Steps:    []Step{validStep},
		},
		{
			Name:     "duplicate-capability",
			Requires: []Capability{CapabilitySession, CapabilitySession},
			Steps:    []Step{validStep},
		},
		{
			Name:     "unknown-capability",
			Requires: []Capability{CapabilitySession, "not-a-capability"},
			Steps:    []Step{validStep},
		},
		{
			Name:     "undeclared-memory-capability",
			Requires: []Capability{CapabilitySession},
			Steps: []Step{{
				Name:   "memory",
				Kind:   StepAddMemory,
				Memory: &MemoryInput{Memory: "value"},
			}},
		},
		{
			Name:     "undeclared-memory-search-capability",
			Requires: []Capability{CapabilitySession, CapabilityMemory},
			Steps: []Step{{
				Name:         "search",
				Kind:         StepSearchMemory,
				MemorySearch: &MemorySearchInput{Query: "value"},
			}},
		},
		{
			Name: "duplicate-memory-search-name",
			Requires: []Capability{
				CapabilitySession,
				CapabilityMemory,
				CapabilityMemorySearch,
			},
			Steps: []Step{
				{Name: "search", Kind: StepSearchMemory, MemorySearch: &MemorySearchInput{Query: "one"}},
				{Name: "search", Kind: StepSearchMemory, MemorySearch: &MemorySearchInput{Query: "two"}},
			},
		},
		{
			Name:     "duplicate-logical-event-id",
			Requires: []Capability{CapabilitySession, CapabilityConcurrent},
			Steps: []Step{{
				Name: "branches",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("left", "duplicate", 1, "user", "user", "left", "left")},
					{messageStep("right", "duplicate", 2, "user", "user", "right", "right")},
				},
			}},
		},
		{
			Name:     "empty-track-name",
			Requires: []Capability{CapabilitySession, CapabilityTrack},
			Steps: []Step{{
				Name:  "track",
				Kind:  StepAppendTrack,
				Track: &TrackInput{Event: &session.TrackEvent{}},
			}},
		},
		{
			Name:     "invalid-utf8-track-name",
			Requires: []Capability{CapabilitySession, CapabilityTrack},
			Steps: []Step{{
				Name:  "track",
				Kind:  StepAppendTrack,
				Track: &TrackInput{Event: &session.TrackEvent{Track: session.Track(invalidUTF8)}},
			}},
		},
		{
			Name:     "invalid-utf8-summary-filter-key",
			Requires: []Capability{CapabilitySession, CapabilitySummary},
			Steps: []Step{{
				Name:    "summary",
				Kind:    StepCreateSummary,
				Summary: &SummaryInput{FilterKey: invalidUTF8},
			}},
		},
		trackPayloadCase("empty-track-payload", json.RawMessage{}),
		trackPayloadCase("malformed-track-payload", json.RawMessage("{")),
		trackPayloadCase("invalid-utf8-track-payload", json.RawMessage{'"', 0xff, '"'}),
		trackPayloadCase("unpaired-surrogate-track-payload", json.RawMessage(`"\ud800"`)),
		trackPayloadCase("duplicate-track-object-key", json.RawMessage(`{"value":1,"value":2}`)),
		eventExtensionCase("empty-event-extension", json.RawMessage{}),
		eventExtensionCase("malformed-event-extension", json.RawMessage("{")),
		eventExtensionCase("invalid-utf8-event-extension", json.RawMessage{'"', 0xff, '"'}),
		eventExtensionCase("unpaired-surrogate-event-extension", json.RawMessage(`"\ud800"`)),
		eventExtensionCase("duplicate-event-extension-object-key", json.RawMessage(`{"value":1,"value":2}`)),
		toolArgumentsCase("malformed-tool-call-arguments", []byte(`{"value":`), false),
		toolArgumentsCase("malformed-delta-tool-call-arguments", []byte(`{"value":`), true),
		toolArgumentsCase("invalid-utf8-tool-call-arguments", []byte{'"', 0xff, '"'}, false),
		toolArgumentsCase("unpaired-surrogate-tool-call-arguments", []byte(`"\ud800"`), false),
		toolArgumentsCase("duplicate-tool-call-argument-key", []byte(`{"value":1,"value":2}`), false),
		{
			Name:     "empty-event-extension-key",
			Requires: []Capability{CapabilitySession},
			Steps: []Step{func() Step {
				step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
				step.Event.Event.Extensions = map[string]json.RawMessage{"": json.RawMessage(`true`)}
				return step
			}()},
		},
		{
			Name:     "reserved-event-extension-key",
			Requires: []Capability{CapabilitySession},
			Steps: []Step{func() Step {
				step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
				step.Event.Event.Extensions = map[string]json.RawMessage{
					logicalEventIDExtension: json.RawMessage(`"spoofed"`),
				}
				return step
			}()},
		},
	}
	for _, replayCase := range tests {
		if err := validateCase(replayCase); err == nil {
			t.Fatalf("case %q unexpectedly validated", replayCase.Name)
		}
	}
}

func TestCaseValidationAllowsNilJSONPayloads(t *testing.T) {
	eventStep := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	eventStep.Event.Event.Extensions = map[string]json.RawMessage{"custom.example/v1": nil}
	replayCase := Case{
		Name:     "nil-json-payloads",
		Requires: []Capability{CapabilitySession, CapabilityTrack},
		Steps: []Step{
			eventStep,
			{
				Name: "track",
				Kind: StepAppendTrack,
				Track: &TrackInput{Event: &session.TrackEvent{
					Track:   "tools",
					Payload: nil,
				}},
			},
		},
	}
	if err := validateCase(replayCase); err != nil {
		t.Fatalf("validateCase() rejected nil JSON payloads: %v", err)
	}
}

func TestDecodeJSONRejectsDuplicateObjectKeys(t *testing.T) {
	for _, raw := range []string{
		`{"value":1,"value":2}`,
		`{"outer":{"value":1,"value":2}}`,
		`[{"value":1,"value":2}]`,
		`{"value":1,"\u0076alue":2}`,
	} {
		var output any
		if err := decodeJSON([]byte(raw), &output); err == nil {
			t.Fatalf("decodeJSON(%s) accepted a duplicate object key", raw)
		}
	}

	var output any
	if err := decodeJSON([]byte(`{"left":{"value":1},"right":{"value":2}}`), &output); err != nil {
		t.Fatalf("decodeJSON() rejected keys scoped to different objects: %v", err)
	}
}

func TestCaseValidationRejectsMismatchedStepPayloads(t *testing.T) {
	validEvent := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "").Event
	tests := []Step{
		{Name: "event-kind-with-state", Kind: StepAppendEvent, State: &StateInput{Scope: StateScopeSession}},
		{Name: "state-kind-with-event", Kind: StepUpdateState, Event: validEvent},
		{Name: "memory-kind-with-summary", Kind: StepAddMemory, Summary: &SummaryInput{}},
		{Name: "memory-search-kind-with-memory", Kind: StepSearchMemory, Memory: &MemoryInput{Memory: "value"}},
		{Name: "summary-kind-with-memory", Kind: StepCreateSummary, Memory: &MemoryInput{Memory: "value"}},
		{Name: "track-kind-with-event", Kind: StepAppendTrack, Event: validEvent},
		{Name: "concurrent-kind-with-summary", Kind: StepConcurrent, Summary: &SummaryInput{}},
	}
	for _, step := range tests {
		t.Run(step.Name, func(t *testing.T) {
			replayCase := Case{
				Name:     step.Name,
				Requires: []Capability{CapabilitySession},
				Steps:    []Step{step},
			}
			if err := validateCase(replayCase); err == nil {
				t.Fatal("validateCase() accepted a mismatched step payload")
			}
		})
	}
}

func TestCaseValidationRejectsInvalidUTF8MemoryInput(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*MemoryInput)
	}{
		{name: "content", mutate: func(input *MemoryInput) { input.Memory = invalid }},
		{name: "topic", mutate: func(input *MemoryInput) { input.Topics = []string{invalid} }},
		{name: "kind", mutate: func(input *MemoryInput) {
			input.Metadata = &memory.Metadata{Kind: memory.Kind(invalid)}
		}},
		{name: "participant", mutate: func(input *MemoryInput) {
			input.Metadata = &memory.Metadata{Participants: []string{invalid}}
		}},
		{name: "location", mutate: func(input *MemoryInput) {
			input.Metadata = &memory.Metadata{Location: invalid}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := &MemoryInput{Memory: "memory"}
			test.mutate(input)
			err := validateCase(Case{
				Name:     "invalid-memory-" + test.name,
				Requires: []Capability{CapabilitySession, CapabilityMemory},
				Steps:    []Step{{Name: "memory", Kind: StepAddMemory, Memory: input}},
			})
			if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("validateCase() error = %v, want invalid UTF-8", err)
			}
		})
	}
}

func TestEventStringsRequireValidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*event.Event)
	}{
		{name: "request id", mutate: func(evt *event.Event) { evt.RequestID = invalid }},
		{name: "invocation id", mutate: func(evt *event.Event) { evt.InvocationID = invalid }},
		{name: "parent invocation id", mutate: func(evt *event.Event) { evt.ParentInvocationID = invalid }},
		{name: "author", mutate: func(evt *event.Event) { evt.Author = invalid }},
		{name: "branch", mutate: func(evt *event.Event) { evt.Branch = invalid }},
		{name: "tag", mutate: func(evt *event.Event) { evt.Tag = invalid }},
		{name: "filter key", mutate: func(evt *event.Event) { evt.FilterKey = invalid }},
		{name: "parent trigger type", mutate: func(evt *event.Event) {
			evt.ParentMetadata = &event.ParentInvocationMetadata{TriggerType: invalid}
		}},
		{name: "parent trigger id", mutate: func(evt *event.Event) {
			evt.ParentMetadata = &event.ParentInvocationMetadata{TriggerID: invalid}
		}},
		{name: "parent trigger name", mutate: func(evt *event.Event) {
			evt.ParentMetadata = &event.ParentInvocationMetadata{TriggerName: invalid}
		}},
		{name: "long running tool id", mutate: func(evt *event.Event) {
			evt.LongRunningToolIDs = map[string]struct{}{invalid: {}}
		}},
		{name: "response id", mutate: func(evt *event.Event) { evt.Response.ID = invalid }},
		{name: "response object", mutate: func(evt *event.Event) { evt.Response.Object = invalid }},
		{name: "response model", mutate: func(evt *event.Event) { evt.Response.Model = invalid }},
		{name: "system fingerprint", mutate: func(evt *event.Event) {
			evt.Response.SystemFingerprint = &invalid
		}},
		{name: "response error message", mutate: func(evt *event.Event) {
			evt.Response.Error = &model.ResponseError{Message: invalid}
		}},
		{name: "response error type", mutate: func(evt *event.Event) {
			evt.Response.Error = &model.ResponseError{Type: invalid}
		}},
		{name: "response error param", mutate: func(evt *event.Event) {
			evt.Response.Error = &model.ResponseError{Param: &invalid}
		}},
		{name: "response error code", mutate: func(evt *event.Event) {
			evt.Response.Error = &model.ResponseError{Code: &invalid}
		}},
		{name: "finish reason", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].FinishReason = &invalid
		}},
		{name: "logprob token", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Logprobs = &model.Logprobs{Content: []model.TokenLogprob{{Token: invalid}}}
		}},
		{name: "top logprob token", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Logprobs = &model.Logprobs{Content: []model.TokenLogprob{{
				TopLogprobs: []model.TopLogprob{{Token: invalid}},
			}}}
		}},
		{name: "message content", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.Content = invalid
		}},
		{name: "delta role", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Delta.Role = model.Role(invalid)
		}},
		{name: "tool response id", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ToolID = invalid
		}},
		{name: "content part text", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ContentParts = []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &invalid,
			}}
		}},
		{name: "content part image", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ContentParts = []model.ContentPart{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: invalid},
			}}
		}},
		{name: "content part audio", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ContentParts = []model.ContentPart{{
				Type:  model.ContentTypeAudio,
				Audio: &model.Audio{Format: invalid},
			}}
		}},
		{name: "content part file", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ContentParts = []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{MimeType: invalid},
			}}
		}},
		{name: "content reference", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ContentParts = []model.ContentPart{{
				Type:       model.ContentTypeFile,
				ContentRef: &model.ContentRef{RequestID: invalid},
			}}
		}},
		{name: "tool call name", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      invalid,
					Arguments: []byte(`{}`),
				},
			}}
		}},
		{name: "tool call extra field key", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
				ExtraFields: map[string]any{invalid: "value"},
			}}
		}},
		{name: "tool call extra field value", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
				ExtraFields: map[string]any{"nested": []any{map[string]any{"value": invalid}}},
			}}
		}},
		{name: "tool call arguments", mutate: func(evt *event.Event) {
			evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
				Function: model.FunctionDefinitionParam{Arguments: []byte{0xff}},
			}}
		}},
		{name: "extension key", mutate: func(evt *event.Event) {
			evt.Extensions = map[string]json.RawMessage{invalid: json.RawMessage(`true`)}
		}},
		{name: "extension value", mutate: func(evt *event.Event) {
			evt.Extensions = map[string]json.RawMessage{"custom.example/v1": {'"', 0xff, '"'}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
			test.mutate(step.Event.Event)
			replayCase := Case{
				Name:     "invalid-event-string",
				Requires: []Capability{CapabilitySession},
				Steps:    []Step{step},
			}
			if err := validateCase(replayCase); err == nil ||
				!strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("validateCase() error = %v, want invalid UTF-8", err)
			}

			if err := event.SetExtension(step.Event.Event, logicalEventIDExtension, "event"); err != nil {
				t.Fatalf("SetExtension() error = %v", err)
			}
			if _, _, _, err := normalizeEvents(
				[]event.Event{*step.Event.Event},
				EventOrderGlobal,
				nil,
				caseEpoch,
			); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("normalizeEvents() error = %v, want invalid UTF-8", err)
			}
		})
	}
}

func TestEventRejectsNonJSONToolCallExtraFields(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "unsupported value", value: func() {}},
		{name: "duplicate custom object key", value: customJSON(`{"key":1,"key":2}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := messageStep("event", "event", 1, "assistant", model.RoleAssistant, "hello", "")
			step.Event.Event.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
				ExtraFields: map[string]any{"value": test.value},
			}}
			replayCase := Case{
				Name:     "invalid-extra-fields",
				Requires: []Capability{CapabilitySession},
				Steps:    []Step{step},
			}
			if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), "JSON") {
				t.Fatalf("validateCase() error = %v, want invalid JSON extra fields", err)
			}
		})
	}
}

type customJSON string

func (value customJSON) MarshalJSON() ([]byte, error) {
	return []byte(value), nil
}

func TestPartialToolCallArgumentsRequireValidUTF8(t *testing.T) {
	step := responseEvent("partial-tool-call", 1, "assistant", model.Response{
		IsPartial: true,
		Choices: []model.Choice{{
			Delta: model.Message{ToolCalls: []model.ToolCall{{
				Function: model.FunctionDefinitionParam{Arguments: []byte{0xff}},
			}}},
		}},
	})
	replayCase := Case{
		Name:     "invalid-partial-arguments",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}
	if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("validateCase() error = %v, want invalid UTF-8", err)
	}
}

func TestTrackPayloadRequiresValidUTF8(t *testing.T) {
	invalidPayload := json.RawMessage{'"', 0xff, '"'}
	step := trackStep("track", "audit", 1, map[string]any{"value": "valid"})
	step.Track.Event.Payload = invalidPayload
	replayCase := Case{
		Name:     "invalid-track-payload",
		Requires: []Capability{CapabilitySession, CapabilityTrack},
		Steps:    []Step{step},
	}
	if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("validateCase() error = %v, want invalid UTF-8", err)
	}
	sess := &session.Session{
		CreatedAt: caseEpoch,
		Tracks: map[session.Track]*session.TrackEvents{
			"audit": {
				Track: "audit",
				Events: []session.TrackEvent{{
					Track:   "audit",
					Payload: invalidPayload,
				}},
			},
		},
	}
	if _, err := normalizeTracks(sess, caseEpoch); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("normalizeTracks() error = %v, want invalid UTF-8", err)
	}
}

func TestCaseValidationRejectsNonPortableStateKeys(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	stateCase := func(name string, scope StateScope, values session.StateMap, deletes []string) Case {
		capability := CapabilitySessionState
		if scope == StateScopeApp {
			capability = CapabilityAppState
		} else if scope == StateScopeUser {
			capability = CapabilityUserState
		}
		return Case{
			Name:     name,
			Requires: []Capability{CapabilitySession, capability},
			Steps: []Step{{
				Name:  "state",
				Kind:  StepUpdateState,
				State: &StateInput{Scope: scope, Values: values, DeleteKeys: deletes},
			}},
		}
	}
	tests := []Case{
		stateCase("app-invalid-utf8-value-key", StateScopeApp, session.StateMap{invalidUTF8: []byte("x")}, nil),
		stateCase("user-invalid-utf8-delete-key", StateScopeUser, nil, []string{invalidUTF8}),
		stateCase("app-prefixed-value", StateScopeApp, session.StateMap{"app:key": []byte("x")}, nil),
		stateCase("user-temp-value", StateScopeUser, session.StateMap{"temp:key": []byte("x")}, nil),
		stateCase("app-empty-delete", StateScopeApp, nil, []string{""}),
		stateCase("session-prefixed-value", StateScopeSession, session.StateMap{"user:key": []byte("x")}, nil),
		stateCase("session-reserved-track-index", StateScopeSession, session.StateMap{replayTrackStateKey: []byte("x")}, nil),
		{
			Name:         "prefixed-initial-state",
			InitialState: session.StateMap{"app:key": []byte("x")},
			Requires:     []Capability{CapabilitySession, CapabilitySessionState},
			Steps:        []Step{messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")},
		},
		{
			Name:     "reserved-track-index-event-delta",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps: []Step{func() Step {
				step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
				step.Event.Event.StateDelta = session.StateMap{replayTrackStateKey: []byte("x")}
				return step
			}()},
		},
		{
			Name:     "invalid-utf8-event-delta-key",
			Requires: []Capability{CapabilitySession, CapabilitySessionState},
			Steps: []Step{func() Step {
				step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
				step.Event.Event.StateDelta = session.StateMap{invalidUTF8: []byte("x")}
				return step
			}()},
		},
	}
	for _, replayCase := range tests {
		t.Run(replayCase.Name, func(t *testing.T) {
			if err := validateCase(replayCase); err == nil {
				t.Fatal("validateCase() accepted a non-portable state key")
			}
		})
	}
}

func TestCaseValidationAllowsScopedEventStateDelta(t *testing.T) {
	step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	step.Event.Event.StateDelta = session.StateMap{
		"app:shared":    []byte(`true`),
		"user:profile":  []byte(`"active"`),
		"session_value": []byte(`1`),
		"temp:scratch":  []byte(`2`),
	}
	replayCase := Case{
		Name: "scoped-event-state-delta",
		Requires: []Capability{
			CapabilitySession,
			CapabilityAppState,
			CapabilityUserState,
			CapabilitySessionState,
		},
		Steps: []Step{step},
	}
	if err := validateCase(replayCase); err != nil {
		t.Fatalf("validateCase() rejected scoped event state delta: %v", err)
	}
}

func TestNormalizationRejectsInvalidUTF8StateKeys(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name      string
		required  Capability
		appState  session.StateMap
		userState session.StateMap
		state     session.StateMap
	}{
		{name: "app", required: CapabilityAppState, appState: session.StateMap{invalid: []byte("x")}},
		{name: "user", required: CapabilityUserState, userState: session.StateMap{invalid: []byte("x")}},
		{name: "session", required: CapabilitySessionState, state: session.StateMap{invalid: []byte("x")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sess := session.NewSession("app", "user", "session")
			for key, value := range test.state {
				sess.SetState(key, value)
			}
			_, err := normalizeSnapshot(
				"backend",
				"invalid-state-key",
				EventOrderGlobal,
				nil,
				Capabilities{test.required: true},
				nil,
				sess,
				test.appState,
				test.userState,
				nil,
				nil,
			)
			if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("normalizeSnapshot() error = %v, want invalid UTF-8", err)
			}
		})
	}

	eventStep := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	eventStep.Event.Event.StateDelta = session.StateMap{invalid: []byte("x")}
	if err := event.SetExtension(eventStep.Event.Event, logicalEventIDExtension, "event"); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	if _, _, _, err := normalizeEvents(
		[]event.Event{*eventStep.Event.Event},
		EventOrderGlobal,
		nil,
		caseEpoch,
	); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("normalizeEvents() error = %v, want invalid UTF-8", err)
	}
}

func TestNormalizationRejectsInvalidSummaryAndTrackStrings(t *testing.T) {
	invalid := string([]byte{0xff})
	summaryTests := []struct {
		name      string
		filterKey string
		summary   *session.Summary
	}{
		{name: "filter key", filterKey: invalid},
		{name: "text", filterKey: "summary", summary: &session.Summary{Summary: invalid}},
		{name: "topic", filterKey: "summary", summary: &session.Summary{Topics: []string{invalid}}},
		{
			name:      "boundary filter key",
			filterKey: "summary",
			summary: &session.Summary{Boundary: &session.SummaryBoundary{
				FilterKey: invalid,
			}},
		},
		{
			name:      "boundary event id",
			filterKey: "summary",
			summary: &session.Summary{Boundary: &session.SummaryBoundary{
				LastEventID: invalid,
			}},
		},
	}
	for _, test := range summaryTests {
		t.Run("summary "+test.name, func(t *testing.T) {
			sess := &session.Session{Summaries: map[string]*session.Summary{
				test.filterKey: test.summary,
			}}
			if _, err := normalizeSummaries(sess, nil, nil); err == nil ||
				!strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("normalizeSummaries() error = %v, want invalid UTF-8", err)
			}
		})
	}

	trackTests := []struct {
		name   string
		tracks map[session.Track]*session.TrackEvents
		want   string
	}{
		{
			name:   "invalid map key",
			tracks: map[session.Track]*session.TrackEvents{session.Track(invalid): nil},
			want:   "invalid UTF-8",
		},
		{
			name: "history mismatch",
			tracks: map[session.Track]*session.TrackEvents{
				"expected": {Track: "actual"},
			},
			want: `contains history for "actual"`,
		},
		{
			name: "invalid event name",
			tracks: map[session.Track]*session.TrackEvents{
				"track": {Track: "track", Events: []session.TrackEvent{{Track: session.Track(invalid)}}},
			},
			want: "invalid UTF-8",
		},
		{
			name: "event mismatch",
			tracks: map[session.Track]*session.TrackEvents{
				"expected": {Track: "expected", Events: []session.TrackEvent{{Track: "actual"}}},
			},
			want: `belongs to "actual"`,
		},
	}
	for _, test := range trackTests {
		t.Run("track "+test.name, func(t *testing.T) {
			sess := &session.Session{Tracks: test.tracks}
			if _, err := normalizeTracks(sess, time.Time{}); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalizeTracks() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCaseValidationRequiresSessionStateForScopedEventDelta(t *testing.T) {
	step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
	step.Event.Event.StateDelta = session.StateMap{"app:shared": []byte(`true`)}
	replayCase := Case{
		Name:     "scoped-event-state-delta",
		Requires: []Capability{CapabilitySession, CapabilityAppState},
		Steps:    []Step{step},
	}
	if err := validateCase(replayCase); err == nil ||
		!strings.Contains(err.Error(), string(CapabilitySessionState)) {
		t.Fatalf("validateCase() error = %v, want missing session-state capability", err)
	}
	replayCase.Requires = append(replayCase.Requires, CapabilitySessionState)
	if err := validateCase(replayCase); err != nil {
		t.Fatalf("validateCase() rejected declared session-state capability: %v", err)
	}
}

func TestCaseValidationRejectsMalformedEventStateDeltaKeys(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		capability Capability
	}{
		{name: "empty", key: "", capability: CapabilitySessionState},
		{name: "empty app key", key: session.StateAppPrefix, capability: CapabilityAppState},
		{name: "empty user key", key: session.StateUserPrefix, capability: CapabilityUserState},
		{name: "empty temp key", key: session.StateTempPrefix, capability: CapabilitySessionState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			step := messageStep("event", "event", 1, "user", model.RoleUser, "hello", "")
			step.Event.Event.StateDelta = session.StateMap{test.key: []byte(`1`)}
			replayCase := Case{
				Name:     test.name,
				Requires: []Capability{CapabilitySession, test.capability},
				Steps:    []Step{step},
			}
			if err := validateCase(replayCase); err == nil {
				t.Fatal("validateCase() accepted a malformed event state key")
			}
		})
	}
}

func TestCaseValidationRejectsNonPortableConcurrency(t *testing.T) {
	branch := func(id, lane string) []Step {
		return []Step{messageStep(id, id, 1, "assistant", model.RoleAssistant, id, lane)}
	}
	stateDeltaBranch := branch("state-delta", "state-delta")
	stateDeltaBranch[0].Event.Event.StateDelta = session.StateMap{"key": []byte("value")}
	tests := []Case{
		{
			Name: "global-order",
			Requires: []Capability{
				CapabilitySession,
				CapabilityConcurrent,
			},
			Steps: []Step{{
				Name:       "parallel",
				Kind:       StepConcurrent,
				Concurrent: [][]Step{branch("a", "a"), branch("b", "b")},
			}},
		},
		{
			Name:       "concurrent-state",
			Requires:   []Capability{CapabilitySession, CapabilitySessionState, CapabilityConcurrent},
			EventOrder: EventOrderCausal,
			Steps: []Step{{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					branch("a", "a"),
					{{Name: "state", Kind: StepUpdateState, State: &StateInput{
						Scope:  StateScopeSession,
						Values: session.StateMap{"key": []byte("value")},
					}}},
				},
			}},
		},
		{
			Name:       "concurrent-event-state-delta",
			Requires:   []Capability{CapabilitySession, CapabilitySessionState, CapabilityConcurrent},
			EventOrder: EventOrderCausal,
			Steps: []Step{{
				Name:       "parallel",
				Kind:       StepConcurrent,
				Concurrent: [][]Step{branch("a", "a"), stateDeltaBranch},
			}},
		},
	}
	for _, replayCase := range tests {
		t.Run(replayCase.Name, func(t *testing.T) {
			if err := validateCase(replayCase); err == nil {
				t.Fatal("validateCase() accepted non-portable concurrency")
			}
		})
	}
}

func TestCaseValidationRejectsConcurrentWriteConflicts(t *testing.T) {
	base := messageStep("user", "user", 1, "user", model.RoleUser, "start", "")
	tests := []struct {
		name     string
		requires []Capability
		step     Step
		want     string
	}{
		{
			name:     "state",
			requires: []Capability{CapabilitySessionState, CapabilityConcurrentState},
			step: Step{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{stateStep("left", StateScopeSession, session.StateMap{"same": []byte("left")}, nil)},
					{stateStep("right", StateScopeSession, session.StateMap{"same": []byte("right")}, nil)},
				},
			},
			want: "state conflict",
		},
		{
			name:     "memory",
			requires: []Capability{CapabilityMemory, CapabilityConcurrentMemory},
			step: Step{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{{Name: "left", Kind: StepAddMemory, Memory: &MemoryInput{Memory: "same"}}},
					{{Name: "right", Kind: StepAddMemory, Memory: &MemoryInput{Memory: "same"}}},
				},
			},
			want: "memory conflict",
		},
		{
			name:     "summary",
			requires: []Capability{CapabilitySummary, CapabilityConcurrentSummary},
			step: Step{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{{Name: "left", Kind: StepCreateSummary, Summary: &SummaryInput{FilterKey: "same"}}},
					{{Name: "right", Kind: StepCreateSummary, Summary: &SummaryInput{FilterKey: "same"}}},
				},
			},
			want: "summary conflict",
		},
		{
			name:     "track",
			requires: []Capability{CapabilityTrack, CapabilityConcurrentTrack},
			step: Step{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{trackStep("left", "same", 2, map[string]any{"side": "left"})},
					{trackStep("right", "same", 3, map[string]any{"side": "right"})},
				},
			},
			want: "track conflict",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requires := []Capability{CapabilitySession, CapabilityConcurrent}
			requires = append(requires, test.requires...)
			replayCase := Case{
				Name:       "conflict-" + test.name,
				EventOrder: EventOrderCausal,
				Requires:   requires,
				Steps:      []Step{base, test.step},
			}
			if err := validateCase(replayCase); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateCase() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCaseValidationRejectsMixedConcurrentWriteDomains(t *testing.T) {
	eventStep := messageStep("event", "event", 2, "assistant", model.RoleAssistant, "event", "branch/event")
	tests := []struct {
		name     string
		requires []Capability
		step     Step
	}{
		{
			name: "event and memory",
			requires: []Capability{
				CapabilitySession,
				CapabilityMemory,
				CapabilityConcurrent,
				CapabilityConcurrentMemory,
			},
			step: Step{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{eventStep},
					{{Name: "memory", Kind: StepAddMemory, Memory: &MemoryInput{Memory: "memory"}}},
				},
			},
		},
		{
			name: "state and track",
			requires: []Capability{
				CapabilitySession,
				CapabilitySessionState,
				CapabilityTrack,
				CapabilityConcurrent,
				CapabilityConcurrentState,
				CapabilityConcurrentTrack,
			},
			step: Step{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{stateStep("state", StateScopeSession, session.StateMap{"key": []byte("value")}, nil)},
					{trackStep("track", "tool", 2, map[string]any{"status": "ok"})},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			replayCase := Case{
				Name:       "mixed-" + strings.ReplaceAll(test.name, " ", "-"),
				EventOrder: EventOrderCausal,
				Requires:   test.requires,
				Steps: []Step{
					messageStep("user", "user", 1, "user", model.RoleUser, "start", ""),
					test.step,
				},
			}
			err := validateCase(replayCase)
			if err == nil || !strings.Contains(err.Error(), "cannot mix concurrent") {
				t.Fatalf("validateCase() error = %v, want mixed concurrent write rejection", err)
			}
		})
	}
}

func TestConcurrentBranchesMayShareFilterKey(t *testing.T) {
	replayCase := Case{
		Name:       "shared-filter-key",
		Requires:   []Capability{CapabilitySession, CapabilityConcurrent},
		EventOrder: EventOrderCausal,
		Steps: []Step{
			messageStep("user", "user", 1, "user", model.RoleUser, "start", ""),
			{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("a", "a", 2, "assistant", model.RoleAssistant, "a", "shared")},
					{messageStep("b", "b", 3, "assistant", model.RoleAssistant, "b", "shared")},
				},
			},
		},
	}
	snapshot, err := Replay(context.Background(), replayCase, InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot.EventOrder["concurrent:1/0"], []string{"a"}) ||
		!reflect.DeepEqual(snapshot.EventOrder["concurrent:1/1"], []string{"b"}) {
		t.Fatalf("concurrent event order = %v", snapshot.EventOrder)
	}
}

func TestCausalEventNormalizationSeparatesLaneNamespaces(t *testing.T) {
	root := causalEvent(t, "root", "", "root")
	businessRoot := causalEvent(t, "business-root", "<root>", "business root")
	businessConcurrent := causalEvent(t, "business-concurrent", "concurrent:1/0", "business concurrent")
	_, order, _, err := normalizeEvents(
		[]event.Event{root, businessRoot, businessConcurrent},
		EventOrderCausal,
		nil,
		caseEpoch,
	)
	if err != nil {
		t.Fatalf("normalizeEvents() error = %v", err)
	}
	if !reflect.DeepEqual(order["root"], []string{"root"}) {
		t.Fatalf("root event order = %v, want root lane", order)
	}
	if !reflect.DeepEqual(order["filter:<root>"], []string{"business-root"}) {
		t.Fatalf("business root event order = %v, want filter lane", order)
	}
	if !reflect.DeepEqual(order["filter:concurrent:1/0"], []string{"business-concurrent"}) {
		t.Fatalf("business concurrent-key event order = %v, want filter lane", order)
	}
	if _, ok := order["<root>"]; ok {
		t.Fatalf("event order retained untyped root lane: %v", order)
	}
}

func TestCausalEventNormalizationSeparatesConcurrentLaneNamespace(t *testing.T) {
	replayCase := Case{
		Name:       "concurrent-lane-namespace",
		Requires:   []Capability{CapabilitySession, CapabilityConcurrent},
		EventOrder: EventOrderCausal,
		Steps: []Step{
			messageStep("anchor", "anchor", 1, "user", model.RoleUser, "anchor", "concurrent:1/0"),
			{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("branch", "branch", 2, "assistant", model.RoleAssistant, "branch", "branch")},
					{messageStep("other", "other", 3, "assistant", model.RoleAssistant, "other", "other")},
				},
			},
		},
	}
	snapshot, err := Replay(context.Background(), replayCase, InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if !reflect.DeepEqual(snapshot.EventOrder["filter:concurrent:1/0"], []string{"anchor"}) {
		t.Fatalf("business concurrent-key event order = %v", snapshot.EventOrder)
	}
	if !reflect.DeepEqual(snapshot.EventOrder["concurrent:1/0"], []string{"branch"}) {
		t.Fatalf("internal concurrent event order = %v", snapshot.EventOrder)
	}
}

func TestCausalEventNormalizationUsesLegacyBranch(t *testing.T) {
	legacyEvent := func(id, filterKey string) event.Event {
		evt := messageStep(id, id, 1, "assistant", model.RoleAssistant, id, "").Event.Event.Clone()
		evt.ID = "physical-" + id
		evt.Version = event.InitVersion
		evt.Branch = "legacy-branch"
		evt.FilterKey = filterKey
		if err := event.SetExtension(evt, logicalEventIDExtension, id); err != nil {
			t.Fatalf("SetExtension() error = %v", err)
		}
		return *evt
	}
	normalize := func(events []event.Event, backend string) Snapshot {
		normalized, order, _, err := normalizeEvents(events, EventOrderCausal, nil, caseEpoch)
		if err != nil {
			t.Fatalf("normalizeEvents() error = %v", err)
		}
		return Snapshot{
			Backend:    backend,
			Case:       "legacy-branch-order",
			Events:     normalized,
			EventOrder: order,
			State:      map[string]CanonicalMap{},
			Summaries:  map[string]CanonicalMap{},
			Tracks:     map[string][]CanonicalMap{},
		}
	}
	baseline := normalize([]event.Event{
		legacyEvent("first", "filter-a"),
		legacyEvent("second", "filter-b"),
	}, "baseline")
	actual := normalize([]event.Event{
		legacyEvent("second", "filter-b"),
		legacyEvent("first", "filter-a"),
	}, "actual")
	diffs, err := Compare("legacy-branch-order", baseline, actual, nil)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	blocking, _ := countDiffs(diffs)
	if blocking == 0 {
		t.Fatalf("Compare() reported no blocking diff for a causal reorder: %+v", diffs)
	}
}

func TestReplayRejectsUncloneableConcurrentSession(t *testing.T) {
	backend := InMemoryBackend()
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &nilTrackHistoryService{Service: services.Session}
		return services, nil
	}
	_, err := Replay(context.Background(), concurrentCase(), backend)
	if err == nil || !strings.Contains(err.Error(), `session track "broken" has nil history`) {
		t.Fatalf("Replay() error = %v, want nil track history rejection", err)
	}
}

func TestCausalPlanKeepsEventsBeforeUserAnchor(t *testing.T) {
	replayCase := Case{
		Name:       "pre-user-event",
		Requires:   []Capability{CapabilitySession, CapabilityConcurrent},
		EventOrder: EventOrderCausal,
		Steps: []Step{
			messageStep("assistant", "assistant", 1, "assistant", model.RoleAssistant, "ready", ""),
			messageStep("user", "user", 2, "user", model.RoleUser, "start", ""),
			{
				Name: "parallel",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("a", "a", 3, "assistant", model.RoleAssistant, "a", "branch/a")},
					{messageStep("b", "b", 4, "assistant", model.RoleAssistant, "b", "branch/b")},
				},
			},
		},
	}
	if err := validateCase(replayCase); err != nil {
		t.Fatalf("validateCase() error = %v", err)
	}
	plan := buildCausalOrderPlan(replayCase.Steps)
	persistedEvent := func(step Step) event.Event {
		evt := step.Event.Event.Clone()
		evt.ID = "physical-" + step.Event.LogicalID
		if err := event.SetExtension(evt, logicalEventIDExtension, step.Event.LogicalID); err != nil {
			t.Fatalf("SetExtension() error = %v", err)
		}
		return *evt
	}
	events := []event.Event{
		persistedEvent(replayCase.Steps[0]),
		persistedEvent(replayCase.Steps[1]),
		persistedEvent(replayCase.Steps[2].Concurrent[0][0]),
		persistedEvent(replayCase.Steps[2].Concurrent[1][0]),
	}
	normalized, order, _, err := normalizeEvents(events, EventOrderCausal, plan, caseEpoch)
	if err != nil {
		t.Fatalf("normalizeEvents() error = %v", err)
	}
	if len(normalized) != 4 {
		t.Fatalf("normalizeEvents() retained %d events, want 4", len(normalized))
	}
	if !reflect.DeepEqual(order["root"], []string{"assistant", "user"}) {
		t.Fatalf("root event order = %v", order["root"])
	}
}

func TestInMemoryBackendSupportsCustomSummaryFilter(t *testing.T) {
	snapshot, err := Replay(context.Background(), summaryFilterKeyCase(), InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	summary, ok := snapshot.Summaries[customSummaryFilterKey]
	if !ok {
		t.Fatalf("summary %q is missing: %v", customSummaryFilterKey, snapshot.Summaries)
	}
	text, ok := summary["text"].(string)
	if !ok || !strings.Contains(text, "question") || !strings.Contains(text, "answer") {
		t.Fatalf("summary text = %v, want custom branch contents", summary["text"])
	}
	if strings.Contains(text, "unrelated") {
		t.Fatalf("summary text includes unrelated branch: %q", text)
	}
}

func TestStateCasePersistsScopedEventDelta(t *testing.T) {
	snapshot, err := Replay(context.Background(), stateCRUDCase(), InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(snapshot.Events) != 2 {
		t.Fatalf("state case events = %d, want 2", len(snapshot.Events))
	}
	stateDelta, ok := snapshot.Events[1]["stateDelta"].(CanonicalMap)
	if !ok {
		t.Fatalf("state event delta = %#v, want CanonicalMap", snapshot.Events[1]["stateDelta"])
	}
	for _, key := range []string{"event_counter", "app:event_counter", "user:event_counter"} {
		if _, exists := stateDelta[key]; !exists {
			t.Fatalf("state event delta omitted %q: %#v", key, stateDelta)
		}
	}
	if _, exists := snapshot.State["session"]["event_counter"]; !exists {
		t.Fatalf("session state omitted event delta: %#v", snapshot.State["session"])
	}
	if len(snapshot.State["app"]) != 0 {
		t.Fatalf("app state was not cleared: %#v", snapshot.State["app"])
	}
	if len(snapshot.State["user"]) != 0 {
		t.Fatalf("user state was not cleared: %#v", snapshot.State["user"])
	}
}

func TestRunnerRejectsDuplicateCases(t *testing.T) {
	replayCase := PublicCases()[0]
	left := InMemoryBackend()
	left.Name = "left"
	right := InMemoryBackend()
	right.Name = "right"
	_, err := (Runner{}).Run(
		context.Background(),
		[]Case{replayCase, replayCase},
		[]Backend{left, right},
	)
	if err == nil {
		t.Fatal("Run() unexpectedly accepted duplicate cases")
	}
}

func TestRunnerInMemoryMatrix(t *testing.T) {
	reference := InMemoryBackend()
	reference.Name = "inmemory-reference"
	comparison := InMemoryBackend()
	comparison.Name = "inmemory-comparison"

	started := time.Now()
	report, err := (Runner{Reference: reference.Name}).Run(
		context.Background(),
		PublicCases(),
		[]Backend{reference, comparison},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !report.IsClean() {
		t.Fatalf("Run() produced blocking differences: %+v", report)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.PassedCases != len(PublicCases()) {
		t.Fatalf("PassedCases = %d, want %d", report.PassedCases, len(PublicCases()))
	}
	if elapsed := time.Since(started); elapsed >= 30*time.Second {
		t.Fatalf("lightweight in-memory matrix took %v, want < 30s", elapsed)
	}
}

func TestPublicMatrixFalsePositiveRate(t *testing.T) {
	left := InMemoryBackend()
	left.Name = "inmemory-clean-a"
	right := InMemoryBackend()
	right.Name = "inmemory-clean-b"
	report, err := (Runner{Reference: left.Name}).Run(
		context.Background(),
		PublicCases(),
		[]Backend{left, right},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	falsePositiveRate := float64(report.BlockingDiffs) / float64(report.TotalCases)
	if falsePositiveRate > 0.05 {
		t.Fatalf("normal matrix false-positive rate = %.2f%%, want <= 5%%", falsePositiveRate*100)
	}
}

func TestCaseValidationRejectsSummaryWithConcurrency(t *testing.T) {
	replayCase := Case{
		Name:       "concurrent_then_summary",
		Requires:   []Capability{CapabilitySession, CapabilitySummary, CapabilityConcurrent},
		EventOrder: EventOrderCausal,
		Steps: []Step{
			messageStep("user", "user", 1, "user", model.RoleUser, "run both branches", ""),
			{
				Name: "parallel-events",
				Kind: StepConcurrent,
				Concurrent: [][]Step{
					{messageStep("branch-a", "branch-a", 2, "assistant", model.RoleAssistant, "alpha", "branch/a")},
					{messageStep("branch-b", "branch-b", 3, "assistant", model.RoleAssistant, "beta", "branch/b")},
				},
			},
			{Name: "summary", Kind: StepCreateSummary, Summary: &SummaryInput{Force: true}},
		},
	}

	if err := validateCase(replayCase); err == nil ||
		!strings.Contains(err.Error(), "concurrent cases cannot contain summary steps") {
		t.Fatalf("validateCase() error = %v, want concurrent summary rejection", err)
	}
}

func TestRunnerConsensusIdentifiesSingleOutlier(t *testing.T) {
	goodA := InMemoryBackend()
	goodA.Name = "good-a"
	goodB := InMemoryBackend()
	goodB.Name = "good-b"
	outlier := eventAuthorDriftBackend("outlier")
	backends := []Backend{outlier, goodB, goodA}

	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		backends,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.ComparisonMode != ComparisonConsensus || report.Reference != "" {
		t.Fatalf("Run() report mode/reference = %q/%q", report.ComparisonMode, report.Reference)
	}
	if report.FailedCases != 1 || len(report.Cases) != 1 {
		t.Fatalf("Run() report counters = %+v", report)
	}
	consensus := report.Cases[0].Consensus
	if consensus == nil {
		t.Fatal("Run() did not emit consensus analysis")
	}
	if consensus.Verdict != ConsensusOutlier || !reflect.DeepEqual(consensus.Outliers, []string{"outlier"}) {
		t.Fatalf("consensus verdict/outliers = %q/%v", consensus.Verdict, consensus.Outliers)
	}
	if !reflect.DeepEqual(consensus.ComparableBackends, []string{"good-a", "good-b", "outlier"}) {
		t.Fatalf("consensus backends = %v", consensus.ComparableBackends)
	}
	if len(consensus.Pairs) != 3 {
		t.Fatalf("consensus pairs = %d, want 3", len(consensus.Pairs))
	}
	for _, pair := range consensus.Pairs {
		if pair.BackendA == "good-a" && pair.BackendB == "good-b" {
			if pair.BlockingDiffs != 0 {
				t.Fatalf("agreeing pair has %d blocking diffs", pair.BlockingDiffs)
			}
			continue
		}
		if pair.BlockingDiffs == 0 {
			t.Fatalf("outlier pair %+v has no blocking diff", pair)
		}
	}

	referenceReport, err := (Runner{Reference: "outlier"}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		backends,
	)
	if err != nil {
		t.Fatalf("reference Run() error = %v", err)
	}
	if referenceReport.FailedCases != 1 || referenceReport.Cases[0].Consensus != nil {
		t.Fatalf("reference report = %+v", referenceReport)
	}
	comparedBackends := make(map[string]struct{})
	for _, diff := range referenceReport.Cases[0].Diffs {
		if !diff.Allowed {
			comparedBackends[diff.BackendB] = struct{}{}
		}
	}
	if len(comparedBackends) != 2 {
		t.Fatalf("faulty reference implicated %d backends, want 2", len(comparedBackends))
	}

	report.Cases[0].Consensus.Pairs[0].BlockingDiffs++
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted tampered consensus counters")
	}
}

func TestRunnerConsensusDoesNotGuessWithTwoBackends(t *testing.T) {
	good := InMemoryBackend()
	good.Name = "good"
	outlier := eventAuthorDriftBackend("different")
	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		[]Backend{good, outlier},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	consensus := report.Cases[0].Consensus
	if consensus == nil || consensus.Verdict != ConsensusAmbiguous || len(consensus.Outliers) != 0 {
		t.Fatalf("two-backend consensus = %+v", consensus)
	}
	consensus.Outliers = []string{}
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() rejected an empty outliers array: %v", err)
	}
}

func TestRunnerConsensusRejectsReference(t *testing.T) {
	left := InMemoryBackend()
	left.Name = "left"
	right := InMemoryBackend()
	right.Name = "right"
	_, err := (Runner{Mode: ComparisonConsensus, Reference: left.Name}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		[]Backend{left, right},
	)
	if err == nil {
		t.Fatal("Run() unexpectedly accepted a consensus reference")
	}
}

func TestRunnerConsensusRecordsExcludedBackendEvidence(t *testing.T) {
	caseUnderTest := PublicCases()[0]
	goodA := InMemoryBackend()
	goodA.Name = "good-a"
	goodB := InMemoryBackend()
	goodB.Name = "good-b"

	t.Run("execution failure", func(t *testing.T) {
		failed := openFailureBackend("failed")
		report, err := (Runner{Mode: ComparisonConsensus}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{goodA, failed, goodB},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusFailed || report.Cases[0].Consensus.Verdict != ConsensusUnanimous {
			t.Fatalf("execution failure report = %+v", report)
		}
		if countEvidence(report.Cases[0].Diffs, "failed", "/execution") != 1 {
			t.Fatalf("execution evidence = %+v", report.Cases[0].Diffs)
		}
	})

	t.Run("unsupported capability", func(t *testing.T) {
		unsupported := missingCapabilityBackend("unsupported", CapabilitySession)
		report, err := (Runner{Mode: ComparisonConsensus}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{unsupported, goodB, goodA},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusUnsupported || report.Cases[0].Consensus.Verdict != ConsensusUnanimous {
			t.Fatalf("unsupported report = %+v", report)
		}
		if countEvidence(report.Cases[0].Diffs, "unsupported", "/capabilities/session") != 1 {
			t.Fatalf("capability evidence = %+v", report.Cases[0].Diffs)
		}
		report.PassedCases = 1
		report.UnsupportedCases = 0
		report.Cases[0].Status = StatusPassed
		if err := report.Validate(); err == nil {
			t.Fatal("Validate() accepted passed status with capability evidence")
		}
	})

	t.Run("insufficient comparable backends", func(t *testing.T) {
		unsupported := missingCapabilityBackend("unsupported", CapabilitySession)
		report, err := (Runner{Mode: ComparisonConsensus}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{goodA, unsupported},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusUnsupported ||
			report.Cases[0].Consensus.Verdict != ConsensusInsufficient {
			t.Fatalf("insufficient report = %+v", report)
		}
	})
}

func TestRunnerReferenceDoesNotDuplicateMissingBaselineEvidence(t *testing.T) {
	caseUnderTest := PublicCases()[0]
	good := InMemoryBackend()
	good.Name = "good"

	t.Run("execution failure", func(t *testing.T) {
		failed := openFailureBackend("failed")
		report, err := (Runner{Reference: failed.Name}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{failed, good},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusFailed ||
			countEvidence(report.Cases[0].Diffs, failed.Name, "/execution") != 1 {
			t.Fatalf("reference execution evidence = %+v", report.Cases[0])
		}
	})

	t.Run("unsupported capability", func(t *testing.T) {
		unsupported := missingCapabilityBackend("unsupported", CapabilitySession)
		report, err := (Runner{Reference: unsupported.Name}).Run(
			context.Background(),
			[]Case{caseUnderTest},
			[]Backend{unsupported, good},
		)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if report.Cases[0].Status != StatusUnsupported || report.BlockingDiffs != 0 {
			t.Fatalf("reference unsupported report = %+v", report)
		}
	})
}

func TestClassifyConsensus(t *testing.T) {
	tests := []struct {
		name     string
		backends []string
		pairs    []PairComparison
		verdict  ConsensusVerdict
		outliers []string
	}{
		{
			name:     "insufficient",
			backends: []string{"a"},
			verdict:  ConsensusInsufficient,
		},
		{
			name:     "unanimous",
			backends: []string{"a", "b", "c"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c"},
				{BackendA: "b", BackendB: "c"},
			},
			verdict: ConsensusUnanimous,
		},
		{
			name:     "strict outlier",
			backends: []string{"a", "b", "c"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "c", BlockingDiffs: 1},
			},
			verdict:  ConsensusOutlier,
			outliers: []string{"c"},
		},
		{
			name:     "two backend disagreement",
			backends: []string{"a", "b"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b", BlockingDiffs: 1},
			},
			verdict: ConsensusAmbiguous,
		},
		{
			name:     "split vote",
			backends: []string{"a", "b", "c", "d"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "a", BackendB: "d", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "d", BlockingDiffs: 1},
				{BackendA: "c", BackendB: "d"},
			},
			verdict: ConsensusAmbiguous,
		},
		{
			name:     "non-transitive agreement",
			backends: []string{"a", "b", "c"},
			pairs: []PairComparison{
				{BackendA: "a", BackendB: "b"},
				{BackendA: "a", BackendB: "c", BlockingDiffs: 1},
				{BackendA: "b", BackendB: "c"},
			},
			verdict: ConsensusAmbiguous,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict, outliers := classifyConsensus(test.backends, test.pairs)
			if verdict != test.verdict || !reflect.DeepEqual(outliers, test.outliers) {
				t.Fatalf("classifyConsensus() = %q/%v, want %q/%v", verdict, outliers, test.verdict, test.outliers)
			}
		})
	}
}

func TestConsensusValidationRequiresBackendExclusionEvidence(t *testing.T) {
	backends := make([]Backend, 3)
	for index, name := range []string{"a", "b", "c"} {
		backends[index] = InMemoryBackend()
		backends[index].Name = name
	}
	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		backends,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	consensus := report.Cases[0].Consensus
	consensus.ComparableBackends = []string{"a", "b"}
	consensus.Pairs = []PairComparison{{BackendA: "a", BackendB: "b"}}
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted a silently excluded backend")
	}
}

func TestConsensusValidationRejectsReversePairDiff(t *testing.T) {
	left := InMemoryBackend()
	left.Name = "a"
	right := InMemoryBackend()
	right.Name = "b"
	report, err := (Runner{Mode: ComparisonConsensus}).Run(
		context.Background(),
		[]Case{PublicCases()[0]},
		[]Backend{left, right},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	report.PassedCases = 0
	report.FailedCases = 1
	report.BlockingDiffs = 1
	report.Cases[0].Status = StatusFailed
	report.Cases[0].Diffs = append(report.Cases[0].Diffs, Diff{
		Case:        report.Cases[0].Name,
		BackendA:    "b",
		BackendB:    "a",
		SessionID:   report.Cases[0].Name,
		Path:        "/session/id",
		Baseline:    "b",
		Actual:      "a",
		Explanation: "tampered reverse pair",
	})
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted a reverse-direction consensus diff")
	}
}

func TestConsensusValidationRejectsReservedPairEvidence(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		allowed bool
	}{
		{name: "execution", path: "/execution"},
		{name: "capability", path: "/capabilities/session", allowed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := InMemoryBackend()
			left.Name = "a"
			right := InMemoryBackend()
			right.Name = "b"
			report, err := (Runner{Mode: ComparisonConsensus}).Run(
				context.Background(),
				[]Case{PublicCases()[0]},
				[]Backend{left, right},
			)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			diff := Diff{
				Case:        report.Cases[0].Name,
				BackendA:    "a",
				BackendB:    "b",
				SessionID:   report.Cases[0].Name,
				Path:        test.path,
				Baseline:    "success",
				Actual:      "tampered",
				Allowed:     test.allowed,
				Explanation: "tampered reserved path",
			}
			report.Cases[0].Diffs = append(report.Cases[0].Diffs, diff)
			if test.allowed {
				report.AllowedDiffs = 1
				report.UnsupportedCases = 1
				report.PassedCases = 0
				report.Cases[0].Status = StatusUnsupported
				report.Cases[0].Consensus.Pairs[0].AllowedDiffs = 1
			} else {
				report.BlockingDiffs = 1
				report.FailedCases = 1
				report.PassedCases = 0
				report.Cases[0].Status = StatusFailed
				report.Cases[0].Consensus.Pairs[0].BlockingDiffs = 1
			}
			if err := report.Validate(); err == nil {
				t.Fatalf("Validate() accepted pairwise %s evidence", test.name)
			}
		})
	}
}

func TestReplayClosesIncompleteServices(t *testing.T) {
	reference := InMemoryBackend()
	cleaned := false
	backend := Backend{
		Name: "incomplete",
		Capabilities: Capabilities{
			CapabilitySession: true,
			CapabilityMemory:  true,
		},
		Open: func(ctx context.Context, caseName string) (*Services, error) {
			services, err := reference.Open(ctx, caseName)
			if err != nil {
				return nil, err
			}
			memoryService := services.Memory
			services.Memory = nil
			services.Cleanup = func() error {
				cleaned = true
				return memoryService.Close()
			}
			return services, nil
		},
	}
	if _, err := Replay(context.Background(), memoryCase(), backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted incomplete services")
	}
	if !cleaned {
		t.Fatal("Replay() did not clean up incomplete services")
	}
}

func TestReplayReadsOnlyRequiredCapabilities(t *testing.T) {
	reference := InMemoryBackend()
	unexpectedCall := errors.New("unexpected unsupported capability call")
	backend := Backend{
		Name:         "session-only",
		Capabilities: Capabilities{CapabilitySession: true},
		Open: func(ctx context.Context, caseName string) (*Services, error) {
			services, err := reference.Open(ctx, caseName)
			if err != nil {
				return nil, err
			}
			memoryService := services.Memory
			services.Memory = nil
			services.Session = &unexpectedStateReadService{
				Service: services.Session,
				err:     unexpectedCall,
			}
			services.Cleanup = memoryService.Close
			return services, nil
		},
	}
	snapshot, err := Replay(context.Background(), singleTurnCase(), backend)
	if err != nil {
		t.Fatalf("Replay() called an unrequired capability: %v", err)
	}
	if len(snapshot.Memories) != 0 || len(snapshot.Summaries) != 0 || len(snapshot.Tracks) != 0 {
		t.Fatalf("snapshot contains unrequired domains: %+v", snapshot)
	}
	for scope, state := range snapshot.State {
		if len(state) != 0 {
			t.Fatalf("snapshot %s state = %v, want empty", scope, state)
		}
	}
}

func TestReplayRejectsMemoryOwnershipDrift(t *testing.T) {
	tests := []struct {
		name       string
		replayCase Case
		catalog    bool
		search     bool
		content    bool
		invalid    bool
		want       string
	}{
		{
			name:       "catalog",
			replayCase: memoryCase(),
			catalog:    true,
			want:       "memory catalog 0 belongs to",
		},
		{
			name:       "search",
			replayCase: memorySearchCase(),
			search:     true,
			want:       `memory search "rank-replay-reports" 0 belongs to`,
		},
		{
			name:       "search content",
			replayCase: memorySearchCase(),
			content:    true,
			want:       `memory search "rank-replay-reports" result id "`,
		},
		{
			name:       "search invalid UTF-8",
			replayCase: memorySearchCase(),
			invalid:    true,
			want:       "contains invalid UTF-8",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := InMemoryBackend()
			open := backend.Open
			backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
				services, err := open(ctx, caseName)
				if err != nil {
					return nil, err
				}
				services.Memory = &memoryDriftService{
					Service: services.Memory,
					catalog: test.catalog,
					search:  test.search,
					content: test.content,
					invalid: test.invalid,
				}
				return services, nil
			}
			_, err := Replay(context.Background(), test.replayCase, backend)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Replay() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReplayRejectsCrossSessionSummaryLeak(t *testing.T) {
	base := InMemoryBackend()
	backend := base
	backend.Name = "summary-leak"
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := base.Open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &summaryLeakService{Service: services.Session}
		return services, nil
	}
	if _, err := Replay(context.Background(), summaryUpdateCase(), backend); err == nil {
		t.Fatal("Replay() unexpectedly accepted a cross-session summary leak")
	}
}

func TestRunnerDetectsIgnoredSummaryUpdate(t *testing.T) {
	baseline := InMemoryBackend()
	baseline.Name = "baseline"
	stale := InMemoryBackend()
	stale.Name = "stale-summary"
	open := stale.Open
	stale.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &ignoredSummaryUpdateService{Service: services.Session}
		return services, nil
	}
	report, err := (Runner{Reference: baseline.Name}).Run(
		context.Background(),
		[]Case{summaryUpdateCase()},
		[]Backend{baseline, stale},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.FailedCases != 1 || report.BlockingDiffs == 0 {
		t.Fatalf("ignored summary update report = %+v", report)
	}
	for _, diff := range report.Cases[0].Diffs {
		if strings.HasPrefix(diff.Path, "/summaries/") && diff.SummaryFilterKey != nil {
			return
		}
	}
	t.Fatalf("ignored summary update lacks a summary locator: %+v", report.Cases[0].Diffs)
}

func TestSummaryTextFaultProducesTextDiff(t *testing.T) {
	replayCase := summaryUpdateCase()
	baseline, err := Replay(context.Background(), replayCase, InMemoryBackend())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	faulted, err := InjectFault(baseline, FaultSummaryText)
	if err != nil {
		t.Fatalf("InjectFault() error = %v", err)
	}
	diffs, err := Compare(replayCase.Name, baseline, faulted, nil)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	for _, diff := range diffs {
		if diff.Path == "/summaries//text" &&
			!diff.Allowed &&
			diff.SummaryFilterKey != nil &&
			*diff.SummaryFilterKey == "" {
			return
		}
	}
	t.Fatalf("summary text fault lacks a blocking text diff: %+v", diffs)
}

func TestRunnerDetectsMemorySearchOrderDrift(t *testing.T) {
	baseline := InMemoryBackend()
	baseline.Name = "baseline"
	reversed := InMemoryBackend()
	reversed.Name = "reversed-search"
	open := reversed.Open
	reversed.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Memory = &reversedMemorySearchService{Service: services.Memory}
		return services, nil
	}
	report, err := (Runner{Reference: baseline.Name}).Run(
		context.Background(),
		[]Case{memorySearchCase()},
		[]Backend{baseline, reversed},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.FailedCases != 1 || report.BlockingDiffs == 0 {
		t.Fatalf("reversed memory search report = %+v", report)
	}
	for _, diff := range report.Cases[0].Diffs {
		if strings.HasPrefix(diff.Path, "/memory_searches/") && diff.MemoryID != "" {
			return
		}
	}
	t.Fatalf("memory search order drift lacks a memory locator: %+v", report.Cases[0].Diffs)
}

func TestReplayAllowsMemoryUpsertAfterSearch(t *testing.T) {
	replayCase := Case{
		Name: "memory-search-before-upsert",
		Requires: []Capability{
			CapabilitySession,
			CapabilityMemory,
			CapabilityMemorySearch,
		},
		Steps: []Step{
			{Name: "add", Kind: StepAddMemory, Memory: &MemoryInput{
				Memory: "same memory",
				Topics: []string{"before"},
			}},
			{Name: "search", Kind: StepSearchMemory, MemorySearch: &MemorySearchInput{
				Query: "same memory",
			}},
			{Name: "upsert", Kind: StepAddMemory, Memory: &MemoryInput{
				Memory: "same memory",
				Topics: []string{"after"},
			}},
		},
	}
	if _, err := Replay(context.Background(), replayCase, InMemoryBackend()); err != nil {
		t.Fatalf("Replay() rejected a search followed by an upsert: %v", err)
	}
}

func TestEveryPublicCaseDetectsInjectedFault(t *testing.T) {
	for _, replayCase := range PublicCases() {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			t.Parallel()
			backend := InMemoryBackend()
			backend.Name = "baseline"
			baseline, err := Replay(context.Background(), replayCase, backend)
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			faulted, err := InjectFault(baseline, replayCase.Fault)
			if err != nil {
				t.Fatalf("InjectFault(%q) error = %v", replayCase.Fault, err)
			}
			diffs, err := Compare(replayCase.Name, baseline, faulted, nil)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			blocking, _ := countDiffs(diffs)
			if blocking == 0 {
				t.Fatalf("fault %q was not detected", replayCase.Fault)
			}
			for _, diff := range diffs {
				if diff.Case != replayCase.Name || diff.SessionID == "" || diff.Path == "" {
					t.Fatalf("diff lacks required locator: %+v", diff)
				}
				if strings.HasPrefix(diff.Path, "/memory_searches/") && diff.MemoryID == "" {
					t.Fatalf("memory search diff lacks a memory id: %+v", diff)
				}
			}
		})
	}
}

func TestRunnerDetectsDroppedNonPersistedEventStateDelta(t *testing.T) {
	step := messageStep("partial-state", "partial-state", 1, "assistant", model.RoleAssistant, "partial", "")
	step.Event.Event.IsPartial = true
	step.Event.Event.StateDelta = session.StateMap{"app:partial": []byte(`true`)}
	replayCase := Case{
		Name: "nonpersisted_event_state_delta",
		Requires: []Capability{
			CapabilitySession,
			CapabilityAppState,
			CapabilitySessionState,
		},
		Steps: []Step{step},
	}
	report, err := (Runner{Reference: "inmemory"}).Run(
		context.Background(),
		[]Case{replayCase},
		[]Backend{
			InMemoryBackend(),
			droppedNonPersistedStateDeltaBackend("drops-partial-state"),
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if report.IsClean() {
		t.Fatal("Run() did not detect the dropped non-persisted event state delta")
	}
	for _, diff := range report.Cases[0].Diffs {
		if diff.Path == "/state/session/app:partial" && !diff.Allowed {
			return
		}
	}
	t.Fatalf("Run() diffs = %#v, want blocking session-state diff", report.Cases[0].Diffs)
}

func TestCompareAllowedDiff(t *testing.T) {
	baseline := minimalSnapshot("baseline", `{"score":1}`)
	actual := minimalSnapshot("actual", `{"score":2}`)
	rules := []AllowedDiff{{
		BackendA: "baseline",
		BackendB: "actual",
		Path:     "/state/session/score",
		Rule:     AllowedIgnore,
		Reason:   "the fixture intentionally demonstrates an allowed backend-private value",
	}}
	diffs, err := Compare("allowed", baseline, actual, rules)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(diffs) != 1 || !diffs[0].Allowed || diffs[0].Explanation != rules[0].Reason {
		t.Fatalf("Compare() diffs = %+v, want one documented allowed diff", diffs)
	}
}

func TestCompareAllowedDiffBackendPairIsUnordered(t *testing.T) {
	baseline := minimalSnapshot("baseline", `{"score":1}`)
	actual := minimalSnapshot("actual", `{"score":2}`)
	baseline.Case = "allowed-reverse"
	actual.Case = "allowed-reverse"
	rules := []AllowedDiff{{
		BackendA: "actual",
		BackendB: "baseline",
		Path:     "/state/session/score",
		Rule:     AllowedIgnore,
		Reason:   "backend pairs are unordered in consensus comparisons",
	}}
	diffs, err := Compare("allowed-reverse", baseline, actual, rules)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(diffs) != 1 || !diffs[0].Allowed {
		t.Fatalf("Compare() diffs = %+v, want one allowed diff", diffs)
	}
}

func TestCompareAllowedDiffBackendWildcardPairIsUnordered(t *testing.T) {
	tests := []struct {
		name     string
		baseline string
		actual   string
		allowed  bool
	}{
		{name: "sqlite first", baseline: "sqlite", actual: "zeta", allowed: true},
		{name: "sqlite second", baseline: "alpha", actual: "sqlite", allowed: true},
		{name: "sqlite absent", baseline: "mysql", actual: "postgres", allowed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := minimalSnapshot(test.baseline, `{"score":1}`)
			actual := minimalSnapshot(test.actual, `{"score":2}`)
			baseline.Case = test.name
			actual.Case = test.name
			rules := []AllowedDiff{{
				BackendA: "*",
				BackendB: "sqlite",
				Path:     "/state/session/score",
				Rule:     AllowedIgnore,
				Reason:   "SQLite exposes a documented backend-private value",
			}}
			diffs, err := Compare(test.name, baseline, actual, rules)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if len(diffs) != 1 || diffs[0].Allowed != test.allowed {
				t.Fatalf("Compare() diffs = %+v, want allowed=%t", diffs, test.allowed)
			}
		})
	}
}

func TestCompareAllowedDiffRules(t *testing.T) {
	tests := []struct {
		name     string
		baseline any
		actual   any
		rule     AllowedDiff
		allowed  bool
	}{
		{
			name:     "same type",
			baseline: "baseline",
			actual:   "actual",
			rule: AllowedDiff{
				Rule: AllowedSameType,
			},
			allowed: true,
		},
		{
			name:     "within delta",
			baseline: 10.0,
			actual:   10.25,
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
			allowed: true,
		},
		{
			name:     "outside delta",
			baseline: 10.0,
			actual:   11.0,
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
		},
		{
			name:     "large integers outside zero delta",
			baseline: int64(9007199254740992),
			actual:   int64(9007199254740993),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0,
			},
		},
		{
			name:     "large integers within unit delta",
			baseline: int64(9007199254740992),
			actual:   int64(9007199254740993),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 1,
			},
			allowed: true,
		},
		{
			name:     "scientific notation within exact delta",
			baseline: json.Number("1e3"),
			actual:   json.Number("1000.5"),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 0.5,
			},
			allowed: true,
		},
		{
			name:     "excessive exponent is not expanded",
			baseline: json.Number("1e1000000"),
			actual:   json.Number("2e1000000"),
			rule: AllowedDiff{
				Rule:  AllowedWithinDelta,
				Delta: 1,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := minimalSnapshot("baseline", `{}`)
			actual := minimalSnapshot("actual", `{}`)
			baseline.Case = test.name
			actual.Case = test.name
			baseline.Session["private"] = test.baseline
			actual.Session["private"] = test.actual
			test.rule.BackendA = "baseline"
			test.rule.BackendB = "actual"
			test.rule.Path = "/session/private"
			test.rule.Reason = "backend-specific fixture value"
			diffs, err := Compare(test.name, baseline, actual, []AllowedDiff{test.rule})
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if len(diffs) != 1 || diffs[0].Allowed != test.allowed {
				t.Fatalf("Compare() diffs = %+v, want allowed=%t", diffs, test.allowed)
			}
		})
	}
}

func TestExactNumberParsing(t *testing.T) {
	valid := []struct {
		name  string
		value any
		want  string
	}{
		{name: "JSON number", value: json.Number("-1.25"), want: "-5/4"},
		{name: "float64", value: float64(0.5), want: "1/2"},
		{name: "float32", value: float32(0.25), want: "1/4"},
		{name: "int", value: int(-1), want: "-1"},
		{name: "int64", value: int64(-2), want: "-2"},
		{name: "uint", value: uint(3), want: "3"},
		{name: "uint64", value: uint64(4), want: "4"},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			got, ok := exactNumber(test.value)
			if !ok || got.RatString() != test.want {
				t.Fatalf("exactNumber(%v) = %v, %t, want %s, true", test.value, got, ok, test.want)
			}
		})
	}

	invalid := []any{
		json.Number(strings.Repeat("9", maxExactNumberCharacters+1)),
		json.Number(""),
		json.Number("1e1025"),
		json.Number("1e-1025"),
		json.Number("1e2e3"),
		json.Number("1e"),
		json.Number(".5"),
		json.Number("1."),
		json.Number("1/2"),
		math.NaN(),
		math.Inf(1),
		"1",
	}
	for _, value := range invalid {
		if _, ok := exactNumber(value); ok {
			t.Fatalf("exactNumber(%v) unexpectedly accepted an unbounded or invalid value", value)
		}
	}
}

func TestValidateObservedCausalPlan(t *testing.T) {
	valid := &causalOrderPlan{predecessors: map[string][]string{
		"first":  nil,
		"second": {"first"},
	}}
	if err := validateObservedCausalPlan(map[string]int{"first": 0, "second": 1}, valid); err != nil {
		t.Fatalf("validateObservedCausalPlan() error = %v", err)
	}

	tests := []struct {
		name      string
		positions map[string]int
		plan      *causalOrderPlan
	}{
		{
			name:      "event count mismatch",
			positions: map[string]int{"first": 0},
			plan:      valid,
		},
		{
			name:      "planned event missing",
			positions: map[string]int{"other": 0},
			plan: &causalOrderPlan{predecessors: map[string][]string{
				"first": nil,
			}},
		},
		{
			name:      "planned predecessor missing",
			positions: map[string]int{"first": 0, "second": 1},
			plan: &causalOrderPlan{predecessors: map[string][]string{
				"first":  {"missing"},
				"second": nil,
			}},
		},
		{
			name:      "event precedes predecessor",
			positions: map[string]int{"first": 0, "second": 1},
			plan: &causalOrderPlan{predecessors: map[string][]string{
				"first":  {"second"},
				"second": nil,
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateObservedCausalPlan(test.positions, test.plan); err == nil {
				t.Fatal("validateObservedCausalPlan() unexpectedly accepted an invalid observation")
			}
		})
	}
}

func TestAllowedDiffValidation(t *testing.T) {
	tests := []AllowedDiff{
		{BackendA: "a", BackendB: "b", Path: "relative", Rule: AllowedIgnore, Reason: "bad path"},
		{BackendA: "a", BackendB: "b", Path: "/state/~2key", Rule: AllowedIgnore, Reason: "bad escape"},
		{BackendA: "a", BackendB: "b", Path: "/state/key~", Rule: AllowedIgnore, Reason: "truncated escape"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: "unknown", Reason: "bad rule"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedIgnore},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: -1, Reason: "bad delta"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: math.NaN(), Reason: "bad delta"},
		{BackendA: "a", BackendB: "b", Path: "/x", Rule: AllowedWithinDelta, Delta: math.Inf(1), Reason: "bad delta"},
	}
	for index, rule := range tests {
		if err := validateAllowedDiffs([]AllowedDiff{rule}); err == nil {
			t.Fatalf("rule %d unexpectedly validated: %+v", index, rule)
		}
	}
	valid := AllowedDiff{
		BackendA: "a",
		BackendB: "b",
		Path:     "/summaries/a~1b/~0key",
		Rule:     AllowedIgnore,
		Reason:   "valid escapes",
	}
	if err := validateAllowedDiffs([]AllowedDiff{valid}); err != nil {
		t.Fatalf("valid JSON Pointer escapes rejected: %v", err)
	}
}

func TestTrackPayloadValuesRemainSemantic(t *testing.T) {
	left := map[string]any{
		"status":               "ok",
		"duration_ms":          float64(10),
		"expected_duration_ms": float64(20),
		"deadline_at":          "2026-07-01T00:00:00Z",
		"nested": map[string]any{
			"latency": float64(2),
		},
	}
	right := map[string]any{
		"status":               "ok",
		"duration_ms":          float64(500),
		"expected_duration_ms": float64(600),
		"deadline_at":          "2026-07-02T00:00:00Z",
		"nested": map[string]any{
			"latency": float64(9),
		},
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	if string(leftJSON) == string(rightJSON) {
		t.Fatalf("distinct track payloads compare equal: %s", leftJSON)
	}
}

func TestZeroTimestampIsNotMarkedPresent(t *testing.T) {
	value := map[string]any{
		"created_at": time.Time{}.Format(time.RFC3339Nano),
		"updated_at": caseEpoch.Format(time.RFC3339Nano),
	}
	normalizeTimestamps(value, "created_at", "updated_at")
	if value["created_at"] != nil {
		t.Fatalf("zero created_at = %v, want nil", value["created_at"])
	}
	if value["updated_at"] != presentMarker {
		t.Fatalf("updated_at = %v, want %q", value["updated_at"], presentMarker)
	}
}

func TestEventExtensionTimestampsRemainSemantic(t *testing.T) {
	evt := event.Event{
		ID:        "physical-event",
		Timestamp: caseEpoch.Add(time.Second),
		Response:  &model.Response{Timestamp: caseEpoch.Add(2 * time.Second)},
		Extensions: map[string]json.RawMessage{
			"custom.example/v1": json.RawMessage(`{"timestamp":"2026-07-01T00:00:00Z","created_at":"semantic"}`),
		},
	}
	if err := event.SetExtension(&evt, logicalEventIDExtension, "logical-event"); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	events, _, _, err := normalizeEvents([]event.Event{evt}, EventOrderGlobal, nil, caseEpoch)
	if err != nil {
		t.Fatalf("normalizeEvents() error = %v", err)
	}
	if events[0]["timestamp"] != time.Second.Nanoseconds() {
		t.Fatalf("event timestamp offset = %v, want one second", events[0]["timestamp"])
	}
	response, ok := events[0]["response"].(map[string]any)
	if !ok || response["timestamp"] != (2*time.Second).Nanoseconds() {
		t.Fatalf("response timestamp offset = %v, want two seconds", response["timestamp"])
	}
	extensions, ok := events[0]["extensions"].(map[string]any)
	if !ok {
		t.Fatalf("event extensions = %T, want object", events[0]["extensions"])
	}
	payload, ok := extensions["custom.example/v1"].(map[string]any)
	if !ok {
		t.Fatalf("custom extension = %T, want object", extensions["custom.example/v1"])
	}
	if payload["timestamp"] != "2026-07-01T00:00:00Z" || payload["created_at"] != "semantic" {
		t.Fatalf("semantic extension timestamps were normalized: %v", payload)
	}
}

func TestToolCallArgumentObjectOrderIsCanonical(t *testing.T) {
	normalize := func(arguments string, field string) CanonicalMap {
		message := model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "tool",
					Arguments: []byte(arguments),
				},
			}},
		}
		choice := model.Choice{Message: message}
		if field == "delta" {
			choice = model.Choice{Delta: message}
		}
		step := responseEvent("tool-call", 1, "assistant", model.Response{
			Choices: []model.Choice{choice},
		})
		if err := event.SetExtension(step.Event.Event, logicalEventIDExtension, "tool-call"); err != nil {
			t.Fatalf("SetExtension() error = %v", err)
		}
		events, _, _, err := normalizeEvents(
			[]event.Event{*step.Event.Event},
			EventOrderGlobal,
			nil,
			caseEpoch,
		)
		if err != nil {
			t.Fatalf("normalizeEvents() error = %v", err)
		}
		return events[0]
	}

	for _, field := range []string{"message", "delta"} {
		t.Run(field, func(t *testing.T) {
			left := normalize(`{"a":1,"b":2}`, field)
			right := normalize(`{"b":2,"a":1}`, field)
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("equivalent tool arguments differ:\nleft=%v\nright=%v", left, right)
			}
			arrayLeft := normalize(`[1,2]`, field)
			arrayRight := normalize(`[2,1]`, field)
			if reflect.DeepEqual(arrayLeft, arrayRight) {
				t.Fatal("tool argument array order was ignored")
			}
		})
	}
}

func TestPartialToolCallArgumentsRemainOpaque(t *testing.T) {
	step := responseEvent("partial-tool-call", 1, "assistant", model.Response{
		IsPartial: true,
		Choices: []model.Choice{{
			Delta: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      "tool",
						Arguments: []byte(`{"value":`),
					},
				}},
			},
		}},
	})
	replayCase := Case{
		Name:     "partial-tool-call",
		Requires: []Capability{CapabilitySession},
		Steps:    []Step{step},
	}
	if err := validateCase(replayCase); err != nil {
		t.Fatalf("validateCase() rejected partial tool arguments: %v", err)
	}
	if err := event.SetExtension(step.Event.Event, logicalEventIDExtension, "partial-tool-call"); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	events, _, _, err := normalizeEvents(
		[]event.Event{*step.Event.Event},
		EventOrderGlobal,
		nil,
		caseEpoch,
	)
	if err != nil {
		t.Fatalf("normalizeEvents() rejected partial tool arguments: %v", err)
	}
	if got := normalizedToolArguments(t, events[0], "delta"); got != `{"value":` {
		t.Fatalf("partial tool arguments = %q, want original fragment", got)
	}
}

func normalizedToolArguments(t *testing.T, evt CanonicalMap, field string) string {
	t.Helper()
	choices, ok := evt["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("event choices = %#v, want one choice", evt["choices"])
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		t.Fatalf("choice = %T, want object", choices[0])
	}
	message, ok := choice[field].(map[string]any)
	if !ok {
		t.Fatalf("choice %s = %T, want object", field, choice[field])
	}
	calls, ok := message["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool calls = %#v, want one call", message["tool_calls"])
	}
	call, ok := calls[0].(map[string]any)
	if !ok {
		t.Fatalf("tool call = %T, want object", calls[0])
	}
	function, ok := call["function"].(map[string]any)
	if !ok {
		t.Fatalf("tool function = %T, want object", call["function"])
	}
	arguments, ok := function["arguments"].(string)
	if !ok {
		t.Fatalf("tool arguments = %T, want string", function["arguments"])
	}
	return arguments
}

func TestTrackTimestampOffsetRemainsSemantic(t *testing.T) {
	sess := &session.Session{Tracks: map[session.Track]*session.TrackEvents{
		"tools": {
			Track: "tools",
			Events: []session.TrackEvent{{
				Track:     "tools",
				Payload:   json.RawMessage(`null`),
				Timestamp: caseEpoch.Add(3 * time.Second),
			}},
		},
	}}
	tracks, err := normalizeTracks(sess, caseEpoch)
	if err != nil {
		t.Fatalf("normalizeTracks() error = %v", err)
	}
	if got := tracks["tools"][0]["timestamp"]; got != (3 * time.Second).Nanoseconds() {
		t.Fatalf("track timestamp offset = %v, want three seconds", got)
	}
}

func TestAnchoredSummaryCutoffRemainsSemantic(t *testing.T) {
	evt := event.Event{
		ID:        "physical-event",
		Timestamp: caseEpoch.Add(2 * time.Second),
		Response:  &model.Response{},
	}
	if err := event.SetExtension(&evt, logicalEventIDExtension, "logical-event"); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	boundary := &session.SummaryBoundary{
		Version:     session.SummaryBoundaryVersion,
		CutoffAt:    evt.Timestamp,
		LastEventID: evt.ID,
	}
	sess := &session.Session{
		CreatedAt: caseEpoch,
		Events:    []event.Event{evt},
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary:  "summary",
				Boundary: boundary,
			},
		},
	}
	summaries, err := normalizeSummaries(
		sess,
		sess.GetEvents(),
		map[string]string{evt.ID: "logical-event"},
	)
	if err != nil {
		t.Fatalf("normalizeSummaries() error = %v", err)
	}
	normalized := summaries[session.SummaryFilterKeyAllContents]["boundary"].(CanonicalMap)
	if normalized["last_event_id"] != "logical-event" ||
		normalized["cutoff_at"] != (2*time.Second).Nanoseconds() {
		t.Fatalf("normalized boundary = %v", normalized)
	}
	boundary.CutoffAt = boundary.CutoffAt.Add(time.Second)
	if _, err := normalizeSummaries(
		sess,
		sess.GetEvents(),
		map[string]string{evt.ID: "logical-event"},
	); err == nil {
		t.Fatal("normalizeSummaries() accepted a cutoff that disagrees with its event anchor")
	}
}

func TestTimestampOnlySummaryCutoffRemainsSemantic(t *testing.T) {
	normalize := func(backend string, cutoff time.Time) Snapshot {
		sess := &session.Session{
			CreatedAt: caseEpoch,
			Summaries: map[string]*session.Summary{
				customSummaryFilterKey: {
					Summary:  "summary",
					Boundary: session.NewSummaryBoundary(customSummaryFilterKey, cutoff),
				},
			},
		}
		summaries, err := normalizeSummaries(sess, nil, nil)
		if err != nil {
			t.Fatalf("normalizeSummaries() error = %v", err)
		}
		return Snapshot{Backend: backend, Case: "timestamp-cutoff", Summaries: summaries}
	}
	baseline := normalize("baseline", caseEpoch.Add(time.Second))
	actual := normalize("actual", caseEpoch.Add(2*time.Second))
	diffs, err := Compare("timestamp-cutoff", baseline, actual, nil)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	blocking, _ := countDiffs(diffs)
	if blocking != 1 || len(diffs) != 1 || diffs[0].Path != "/summaries/agent~1custom/boundary/cutoff_at" {
		t.Fatalf("Compare() blocking diffs = %d, want 1: %+v", blocking, diffs)
	}
}

func TestTimestampOnlySummaryCutoffRetainsEqualTimestampEvent(t *testing.T) {
	evt := event.Event{ID: "physical-event", Timestamp: caseEpoch.Add(2 * time.Second), Response: &model.Response{}}
	if err := event.SetExtension(&evt, logicalEventIDExtension, "logical-event"); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	retained := retainedEventIDs(
		[]event.Event{evt},
		&session.Summary{Boundary: session.NewSummaryBoundary("", evt.Timestamp)},
		"",
		map[string]string{evt.ID: "logical-event"},
	)
	if !reflect.DeepEqual(retained, []string{"logical-event"}) {
		t.Fatalf("retained event IDs = %v, want equal-timestamp event", retained)
	}
}

func TestRunnerRejectsReservedWildcardBackendName(t *testing.T) {
	backend := InMemoryBackend()
	backend.Name = "*"
	if _, err := (Runner{}).Run(context.Background(), []Case{PublicCases()[0]}, []Backend{backend}); err == nil {
		t.Fatal("Runner accepted reserved wildcard backend name")
	}
}

func TestCausalEventNormalizationIgnoresGlobalInterleaving(t *testing.T) {
	root := causalEvent(t, "root", "", "root")
	branchA1 := causalEvent(t, "a-1", "branch/a", "a-1")
	branchA2 := causalEvent(t, "a-2", "branch/a", "a-2")
	branchB1 := causalEvent(t, "b-1", "branch/b", "b-1")
	branchB2 := causalEvent(t, "b-2", "branch/b", "b-2")

	left, leftOrder, _, err := normalizeEvents([]event.Event{
		root, branchA1, branchB1, branchA2, branchB2,
	}, EventOrderCausal, nil, caseEpoch)
	if err != nil {
		t.Fatalf("normalizeEvents(left) error = %v", err)
	}
	right, rightOrder, _, err := normalizeEvents([]event.Event{
		root, branchB1, branchA1, branchB2, branchA2,
	}, EventOrderCausal, nil, caseEpoch)
	if err != nil {
		t.Fatalf("normalizeEvents(right) error = %v", err)
	}
	if !reflect.DeepEqual(left, right) || !reflect.DeepEqual(leftOrder, rightOrder) {
		t.Fatalf("causally equivalent interleavings differ:\nleft=%+v\nright=%+v", left, right)
	}
}

func TestMemoryEventTimeRemainsSemantic(t *testing.T) {
	instant := time.Date(2026, time.July, 1, 8, 30, 0, 0, time.UTC)
	sameInstant := instant.In(time.FixedZone("UTC+8", 8*60*60))
	entry := func(eventTime *time.Time) *memory.Entry {
		return &memory.Entry{
			ID:      "backend-id",
			AppName: "replaytest",
			UserID:  "user-1",
			Memory: &memory.Memory{
				Memory:    "A dated event.",
				EventTime: eventTime,
			},
		}
	}
	left, err := normalizeMemories([]*memory.Entry{entry(&instant)})
	if err != nil {
		t.Fatalf("normalizeMemories(left) error = %v", err)
	}
	right, err := normalizeMemories([]*memory.Entry{entry(&sameInstant)})
	if err != nil {
		t.Fatalf("normalizeMemories(right) error = %v", err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("equivalent instants differ: %+v != %+v", left, right)
	}
	drifted := instant.Add(time.Second)
	other, err := normalizeMemories([]*memory.Entry{entry(&drifted)})
	if err != nil {
		t.Fatalf("normalizeMemories(drifted) error = %v", err)
	}
	if reflect.DeepEqual(left, other) {
		t.Fatal("different memory event times were normalized away")
	}
}

func TestNormalizeMemorySearchesPreservesRankAndScore(t *testing.T) {
	entries := []*memory.Entry{
		{ID: "physical-a", Memory: &memory.Memory{Memory: "first"}},
		{ID: "physical-b", Memory: &memory.Memory{Memory: "second"}},
	}
	_, ids, err := normalizeMemoryCatalog(entries)
	if err != nil {
		t.Fatalf("normalizeMemoryCatalog() error = %v", err)
	}
	searches, err := normalizeMemorySearches(map[string][]*memory.Entry{
		"query": {
			{ID: "physical-b", Memory: &memory.Memory{Memory: "second"}, Score: 0.8},
			{ID: "physical-a", Memory: &memory.Memory{Memory: "first"}, Score: 0.4},
		},
	}, ids)
	if err != nil {
		t.Fatalf("normalizeMemorySearches() error = %v", err)
	}
	results := searches["query"]
	if len(results) != 2 ||
		results[0]["id"] != ids["physical-b"].logicalID ||
		results[1]["id"] != ids["physical-a"].logicalID ||
		results[0]["score"] != 0.8 ||
		results[1]["score"] != 0.4 {
		t.Fatalf("normalized search results = %#v", results)
	}
	if _, err := normalizeMemorySearches(map[string][]*memory.Entry{
		"query": {{ID: "unknown", Memory: &memory.Memory{Memory: "unknown"}}},
	}, ids); err == nil {
		t.Fatal("normalizeMemorySearches() accepted an unknown memory id")
	}
	for _, test := range []struct {
		name   string
		mutate func(*memory.Entry)
	}{
		{name: "content", mutate: func(entry *memory.Entry) { entry.Memory.Memory = "changed" }},
		{name: "app", mutate: func(entry *memory.Entry) { entry.AppName = "other" }},
		{name: "user", mutate: func(entry *memory.Entry) { entry.UserID = "other" }},
		{name: "metadata", mutate: func(entry *memory.Entry) { entry.Memory.Topics = []string{"changed"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := &memory.Entry{
				ID:      "physical-a",
				Memory:  &memory.Memory{Memory: "first"},
				AppName: entries[0].AppName,
				UserID:  entries[0].UserID,
			}
			test.mutate(result)
			if _, err := normalizeMemorySearches(map[string][]*memory.Entry{
				"query": {result},
			}, ids); err == nil || !strings.Contains(err.Error(), "does not match its search-time reference") {
				t.Fatalf("normalizeMemorySearches() error = %v, want search-time mismatch", err)
			}
		})
	}
}

func TestNormalizeMemoryCatalogRejectsSemanticDuplicates(t *testing.T) {
	entries := []*memory.Entry{
		{
			ID:      "physical-a",
			AppName: "replaytest",
			UserID:  "user-1",
			Memory: &memory.Memory{
				Memory: "same fact",
				Topics: []string{"replay"},
				Kind:   memory.KindFact,
			},
		},
		{
			ID:      "physical-b",
			AppName: "replaytest",
			UserID:  "user-1",
			Memory: &memory.Memory{
				Memory: "same fact",
				Topics: []string{"replay"},
				Kind:   memory.KindFact,
			},
		},
	}
	if _, _, err := normalizeMemoryCatalog(entries); err == nil ||
		!strings.Contains(err.Error(), "duplicate normalized memory entry") {
		t.Fatalf("normalizeMemoryCatalog() error = %v, want semantic duplicate error", err)
	}

	firstEvent := time.Date(2026, time.July, 1, 8, 30, 0, 0, time.UTC)
	secondEvent := firstEvent.Add(time.Hour)
	for index, eventTime := range []*time.Time{&firstEvent, &secondEvent} {
		entries[index].Memory.Kind = memory.KindEpisode
		entries[index].Memory.EventTime = eventTime
	}
	got, _, err := normalizeMemoryCatalog(entries)
	if err != nil {
		t.Fatalf("normalizeMemoryCatalog(distinct episodes) error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("normalizeMemoryCatalog(distinct episodes) returned %d entries, want 2", len(got))
	}
}

func TestMemorySearchInputQueryIsAuthoritative(t *testing.T) {
	replayCase := memorySearchCase()
	search := replayCase.Steps[len(replayCase.Steps)-1].MemorySearch
	search.Options.Query = "does not match"
	backend := InMemoryBackend()
	snapshot, err := Replay(context.Background(), replayCase, backend)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	results := snapshot.MemorySearches["rank-replay-reports"]
	if len(results) != 2 {
		t.Fatalf("memory search returned %d results, want 2", len(results))
	}
}

func TestNormalizeMemoriesRejectsMalformedEntry(t *testing.T) {
	tests := []struct {
		name    string
		entries []*memory.Entry
	}{
		{name: "nil entry", entries: []*memory.Entry{nil}},
		{name: "nil content", entries: []*memory.Entry{{ID: "memory"}}},
		{name: "missing id", entries: []*memory.Entry{{Memory: &memory.Memory{Memory: "value"}}}},
		{
			name: "duplicate id",
			entries: []*memory.Entry{
				{ID: "memory", Memory: &memory.Memory{Memory: "left"}},
				{ID: "memory", Memory: &memory.Memory{Memory: "right"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeMemories(test.entries); err == nil {
				t.Fatal("normalizeMemories() accepted a malformed memory entry")
			}
		})
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	summaryFilterKey := "agent/weather"
	report := Report{
		GeneratedAt:    caseEpoch,
		ComparisonMode: ComparisonReference,
		Reference:      "inmemory",
		Backends:       []string{"inmemory", "sqlite"},
		TotalCases:     1,
		FailedCases:    1,
		BlockingDiffs:  1,
		Cases: []CaseResult{{
			Name:   "summary_filter_key",
			Status: StatusFailed,
			Diffs: []Diff{{
				Case:             "summary_filter_key",
				BackendA:         "inmemory",
				BackendB:         "sqlite",
				SessionID:        "summary_filter_key",
				SummaryFilterKey: &summaryFilterKey,
				Path:             "/summaries/agent~1weather/text",
				Baseline:         "expected",
				Actual:           "drifted",
			}},
		}},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"summary_filter_key":"agent/weather"`) {
		t.Fatalf("report JSON lacks summary locator: %s", raw)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded Validate() error = %v", err)
	}
}

func TestFullSessionSummaryLocatorRoundTrip(t *testing.T) {
	fullSessionFilterKey := session.SummaryFilterKeyAllContents
	diff := Diff{
		Case:             "summary_update",
		BackendA:         "inmemory",
		BackendB:         "sqlite",
		SessionID:        "summary_update",
		SummaryFilterKey: &fullSessionFilterKey,
		Path:             "/summaries//text",
		Baseline:         "current",
		Actual:           "stale",
	}
	raw, err := json.Marshal(diff)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(raw), `"summary_filter_key":""`) {
		t.Fatalf("full-session summary locator is missing: %s", raw)
	}
	var decoded Diff
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.SummaryFilterKey == nil || *decoded.SummaryFilterKey != "" {
		t.Fatalf("decoded full-session summary locator = %#v", decoded.SummaryFilterKey)
	}
}

func TestWriteReportAndSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("sample report is invalid JSON: %v", err)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("sample report Validate() error = %v", err)
	}
	var output bytes.Buffer
	if err := WriteReport(&output, report); err != nil {
		t.Fatalf("WriteReport() error = %v", err)
	}
	if !json.Valid(output.Bytes()) || !bytes.Contains(output.Bytes(), []byte("fault_injection_demo")) {
		t.Fatalf("WriteReport() output is invalid: %s", output.Bytes())
	}
	if err := WriteReport(nil, report); err == nil {
		t.Fatal("WriteReport(nil) unexpectedly succeeded")
	}
}

func TestReportValidationRejectsIncorrectCounters(t *testing.T) {
	report := Report{
		GeneratedAt:    caseEpoch,
		ComparisonMode: ComparisonReference,
		Reference:      "baseline",
		Backends:       []string{"baseline", "actual"},
		TotalCases:     1,
		PassedCases:    1,
		Cases: []CaseResult{{
			Name:   "clean",
			Status: StatusPassed,
		}},
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	report.PassedCases = 0
	if err := report.Validate(); err == nil {
		t.Fatal("report with incorrect status counters unexpectedly validated")
	}
}

func causalEvent(t *testing.T, logicalID, filterKey, content string) event.Event {
	t.Helper()
	step := messageStep("causal-"+logicalID, logicalID, 1, "assistant", "assistant", content, filterKey)
	evt := step.Event.Event.Clone()
	evt.ID = "physical-" + logicalID
	if err := event.SetExtension(evt, logicalEventIDExtension, logicalID); err != nil {
		t.Fatalf("SetExtension() error = %v", err)
	}
	return *evt
}

type eventAuthorDriftService struct {
	session.Service
}

type nilTrackHistoryService struct {
	session.Service
}

type unexpectedStateReadService struct {
	session.Service
	err error
}

type summaryLeakService struct {
	session.Service
}

type ignoredSummaryUpdateService struct {
	session.Service
	calls int
}

func (s *ignoredSummaryUpdateService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	s.calls++
	if s.calls == 2 {
		return nil
	}
	return s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

type reversedMemorySearchService struct {
	memory.Service
}

type memoryDriftService struct {
	memory.Service
	catalog bool
	search  bool
	content bool
	invalid bool
}

type droppedNonPersistedStateDeltaService struct {
	session.Service
}

func (s *droppedNonPersistedStateDeltaService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	if evt != nil && !replayEventIsPersistable(evt) && len(evt.StateDelta) > 0 {
		evt = evt.Clone()
		evt.StateDelta = nil
	}
	return s.Service.AppendEvent(ctx, sess, evt, options...)
}

func (s *reversedMemorySearchService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	results, err := s.Service.SearchMemories(ctx, userKey, query, opts...)
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(results)-1; left < right; left, right = left+1, right-1 {
		results[left], results[right] = results[right], results[left]
	}
	return results, nil
}

func (s *memoryDriftService) ReadMemories(
	ctx context.Context,
	key memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	entries, err := s.Service.ReadMemories(ctx, key, limit)
	if err != nil || !s.catalog {
		return entries, err
	}
	return memoriesWithWrongOwner(entries), nil
}

func (s *memoryDriftService) SearchMemories(
	ctx context.Context,
	key memory.UserKey,
	query string,
	options ...memory.SearchOption,
) ([]*memory.Entry, error) {
	entries, err := s.Service.SearchMemories(ctx, key, query, options...)
	if err != nil || (!s.search && !s.content && !s.invalid) {
		return entries, err
	}
	entries = cloneMemoryEntries(entries)
	if len(entries) > 0 && entries[0] != nil {
		if s.search {
			entries[0].UserID += "-wrong"
		}
		if s.content && entries[0].Memory != nil {
			entries[0].Memory.Memory += "-wrong"
		}
		if s.invalid && entries[0].Memory != nil {
			entries[0].Memory.Memory = string([]byte{0xff})
		}
	}
	return entries, nil
}

func memoriesWithWrongOwner(entries []*memory.Entry) []*memory.Entry {
	output := cloneMemoryEntries(entries)
	if len(output) > 0 && output[0] != nil {
		output[0].UserID += "-wrong"
	}
	return output
}

func (s *summaryLeakService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.GetSession(ctx, key, options...)
	if err != nil || sess == nil || !strings.HasSuffix(key.SessionID, summaryIsolationSessionSuffix) {
		return sess, err
	}
	sess = sess.Clone()
	sess.Summaries = map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "leaked summary"},
	}
	return sess, nil
}

func (s *unexpectedStateReadService) ListAppStates(context.Context, string) (session.StateMap, error) {
	return nil, s.err
}

func (s *unexpectedStateReadService) ListUserStates(context.Context, session.UserKey) (session.StateMap, error) {
	return nil, s.err
}

func (s *eventAuthorDriftService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	drifted := evt.Clone()
	drifted.Author += "-drifted"
	return s.Service.AppendEvent(ctx, sess, drifted, options...)
}

func (s *nilTrackHistoryService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.CreateSession(ctx, key, state, options...)
	if err != nil || sess == nil {
		return sess, err
	}
	sess.TracksMu.Lock()
	sess.Tracks = map[session.Track]*session.TrackEvents{"broken": nil}
	sess.TracksMu.Unlock()
	return sess, nil
}

func eventAuthorDriftBackend(name string) Backend {
	backend := InMemoryBackend()
	backend.Name = name
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &eventAuthorDriftService{Service: services.Session}
		return services, nil
	}
	return backend
}

func droppedNonPersistedStateDeltaBackend(name string) Backend {
	backend := InMemoryBackend()
	backend.Name = name
	open := backend.Open
	backend.Open = func(ctx context.Context, caseName string) (*Services, error) {
		services, err := open(ctx, caseName)
		if err != nil {
			return nil, err
		}
		services.Session = &droppedNonPersistedStateDeltaService{Service: services.Session}
		return services, nil
	}
	return backend
}

func openFailureBackend(name string) Backend {
	return Backend{
		Name:         name,
		Capabilities: FullCapabilities(),
		Open: func(context.Context, string) (*Services, error) {
			return nil, errors.New("injected open failure")
		},
	}
}

func missingCapabilityBackend(name string, capability Capability) Backend {
	backend := InMemoryBackend()
	backend.Name = name
	backend.Capabilities[capability] = false
	return backend
}

func countEvidence(diffs []Diff, backend, path string) int {
	count := 0
	for _, diff := range diffs {
		if diff.BackendA == backend && diff.BackendB == backend && diff.Path == path {
			count++
		}
	}
	return count
}

func minimalSnapshot(backend, score string) Snapshot {
	return Snapshot{
		Backend: backend,
		Case:    "allowed",
		Session: CanonicalMap{"id": "session-1", "app_name": "app", "user_id": "user"},
		State: map[string]CanonicalMap{
			"app": {}, "user": {}, "session": {"score": score},
		},
		Summaries: map[string]CanonicalMap{},
		Tracks:    map[string][]CanonicalMap{},
	}
}
