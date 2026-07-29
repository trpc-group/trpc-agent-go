//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// baseLead keeps every scripted timestamp comfortably later than the
// wall-clock instant at which a backend writes the session row.
//
// Backends may legitimately discard data that predates the session it belongs
// to: session/sqlite drops a summary whose updated_at precedes the session's
// created_at, which stops a stale summary from attaching itself to a session
// that was deleted and recreated under the same identifier. A script anchored
// in the past trips that guard, and the resulting empty summary looks exactly
// like a backend losing data. Leading the wall clock keeps the harness from
// manufacturing that false report.
const baseLead = time.Minute

// newBaseTime returns the instant a run anchors its timestamps to.
//
// One base is computed per run and shared by every backend, so all backends
// receive identical absolute timestamps rather than merely identical offsets.
// The value is truncated to the comparison precision so that a timestamp
// cannot be rounded into a different bucket by one backend and not another.
func newBaseTime() time.Time {
	return time.Now().UTC().Add(baseLead).Truncate(timestampPrecision)
}

// SessionRef identifies the session an operation targets.
type SessionRef struct {
	AppName   string
	UserID    string
	SessionID string
}

// Key returns the session key used by the session service.
func (r SessionRef) Key() session.Key {
	return session.Key{AppName: r.AppName, UserID: r.UserID, SessionID: r.SessionID}
}

// UserKey returns the user-scoped key used for app and user state.
func (r SessionRef) UserKey() session.UserKey {
	return session.UserKey{AppName: r.AppName, UserID: r.UserID}
}

// MemoryUserKey returns the user-scoped key used by the memory service. The
// session and memory packages declare separate key types for the same pair.
func (r SessionRef) MemoryUserKey() memory.UserKey {
	return memory.UserKey{AppName: r.AppName, UserID: r.UserID}
}

// String renders the reference as it appears in divergence paths.
func (r SessionRef) String() string {
	return r.AppName + "/" + r.UserID + "/" + r.SessionID
}

// ToolCallSpec describes a single tool call carried by an assistant event.
type ToolCallSpec struct {
	// ID is the tool call identifier a later tool response refers to.
	ID string
	// Name is the invoked function name.
	Name string
	// Arguments is the raw JSON argument object. It is compared as canonical
	// JSON so that key order does not matter.
	Arguments string
}

// EventSpec describes an event in fully deterministic terms.
//
// The harness assigns ID and Timestamp from the spec instead of letting
// event.New generate them, because event.New uses a fresh UUID and the wall
// clock. Leaving either to chance would make every backend disagree on every
// event for reasons that have nothing to do with backend behavior.
type EventSpec struct {
	// ID is the stable event identifier. It must be unique within a scenario.
	ID string
	// InvocationID groups events belonging to one agent invocation.
	InvocationID string
	// Author names the event producer, such as "user" or an agent name.
	Author string
	// Offset places the event at BaseTime+Offset.
	Offset time.Duration
	// Role is the message role. An empty role produces an event with no
	// message, which exercises how backends persist metadata-only events.
	Role model.Role
	// Content is the message text.
	Content string
	// ToolCalls are the tool calls requested by an assistant message.
	ToolCalls []ToolCallSpec
	// ToolID and ToolName associate a tool response with its call.
	ToolID   string
	ToolName string
	// Branch, Tag and FilterKey carry the routing metadata that summary
	// filtering and event selection depend on.
	Branch    string
	Tag       string
	FilterKey string
	// StateDelta is merged into session state when the event is appended. A
	// nil value stores a nil value under the key; it does not remove the key.
	StateDelta map[string][]byte
	// Extensions holds raw JSON values keyed by extension name.
	Extensions map[string]string
}

// Timestamp returns the absolute instant the spec describes.
func (s EventSpec) Timestamp(base time.Time) time.Time { return base.Add(s.Offset) }

// Build materializes the spec as an event relative to the run's base instant.
// The returned event is safe to hand to a session service directly.
func (s EventSpec) Build(base time.Time) (*event.Event, error) {
	e := event.New(s.InvocationID, s.Author)
	e.ID = s.ID
	e.Timestamp = s.Timestamp(base)
	e.Branch = s.Branch
	e.Tag = s.Tag
	e.FilterKey = s.FilterKey

	if len(s.StateDelta) > 0 {
		e.StateDelta = make(map[string][]byte, len(s.StateDelta))
		for k, v := range s.StateDelta {
			if v == nil {
				e.StateDelta[k] = nil
				continue
			}
			e.StateDelta[k] = append([]byte(nil), v...)
		}
	}

	if len(s.Extensions) > 0 {
		e.Extensions = make(map[string]json.RawMessage, len(s.Extensions))
		for k, v := range s.Extensions {
			if !json.Valid([]byte(v)) {
				return nil, fmt.Errorf("event %q extension %q is not valid JSON: %s", s.ID, k, v)
			}
			e.Extensions[k] = json.RawMessage(v)
		}
	}

	msg, ok, err := s.message()
	if err != nil {
		return nil, err
	}
	if ok {
		e.Response = &model.Response{
			Choices: []model.Choice{{Message: msg}},
			Done:    true,
		}
	}
	return e, nil
}

// message builds the message the spec describes. The second result reports
// whether the spec carries a message at all.
func (s EventSpec) message() (model.Message, bool, error) {
	switch s.Role {
	case "":
		return model.Message{}, false, nil
	case model.RoleUser:
		return model.NewUserMessage(s.Content), true, nil
	case model.RoleSystem:
		return model.NewSystemMessage(s.Content), true, nil
	case model.RoleTool:
		return model.NewToolMessage(s.ToolID, s.ToolName, s.Content), true, nil
	case model.RoleAssistant:
		msg := model.NewAssistantMessage(s.Content)
		for _, tc := range s.ToolCalls {
			if !json.Valid([]byte(tc.Arguments)) {
				return model.Message{}, false, fmt.Errorf(
					"event %q tool call %q has invalid JSON arguments: %s", s.ID, tc.ID, tc.Arguments)
			}
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      tc.Name,
					Arguments: []byte(tc.Arguments),
				},
			})
		}
		return msg, true, nil
	default:
		return model.Message{}, false, fmt.Errorf("event %q has unknown role %q", s.ID, s.Role)
	}
}

// TrackEventSpec describes one entry appended to an observability track.
type TrackEventSpec struct {
	// Track names the track the entry belongs to.
	Track session.Track
	// Offset places the entry at BaseTime+Offset.
	Offset time.Duration
	// Payload is the raw JSON payload.
	Payload string
}

// Build materializes the spec as a track event relative to the run's base
// instant.
func (s TrackEventSpec) Build(base time.Time) (*session.TrackEvent, error) {
	if !json.Valid([]byte(s.Payload)) {
		return nil, fmt.Errorf("track %q payload is not valid JSON: %s", s.Track, s.Payload)
	}
	return &session.TrackEvent{
		Track:     s.Track,
		Payload:   json.RawMessage(s.Payload),
		Timestamp: base.Add(s.Offset),
	}, nil
}

// SummarySpec describes a summary the deterministic summarizer should produce
// for a given filter key.
type SummarySpec struct {
	// FilterKey selects the conversation branch. The empty string is the
	// whole-session summary.
	FilterKey string
	// Text is the summary body the summarizer returns.
	Text string
	// Force requests summarization even when the service would skip it.
	Force bool
}

// MemorySpec describes a memory write.
type MemorySpec struct {
	// Content is the memory body.
	Content string
	// Topics label the memory. Topics deliberately do not participate in
	// memory ID generation, so changing only topics must not rotate the ID.
	Topics []string
}

// sortedTopics returns a copy of the topics in a stable order so that a
// backend which reorders them is not reported as divergent for that reason
// alone.
func (s MemorySpec) sortedTopics() []string {
	out := append([]string(nil), s.Topics...)
	sort.Strings(out)
	return out
}
