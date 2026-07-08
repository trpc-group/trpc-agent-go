//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Fault kinds injected at the real write boundary.
const (
	FaultDuplicateEvent    = "duplicate_event"
	FaultDropMemory        = "drop_memory"
	FaultTamperState       = "tamper_state"
	FaultWrongTrackPayload = "wrong_track_payload"
	FaultDropSummary       = "drop_summary"
	FaultWrongFilterKey    = "wrong_filterkey"
	FaultOverwriteSummary  = "overwrite_summary"
)

// WrapFaulty returns a copy of b whose session/memory services corrupt data as
// it flows through a single write method, so the bad data is genuinely
// persisted and surfaces on an ordinary read-back. overwrite_summary is not a
// decorator fault: it is produced by constructing a backend with
// NewFaultySummarizer (see the fault-detection test), so WrapFaulty rejects it.
func WrapFaulty(b *Backend, fault string) (*Backend, error) {
	switch fault {
	case FaultDuplicateEvent, FaultTamperState, FaultWrongTrackPayload,
		FaultDropSummary, FaultWrongFilterKey:
		return &Backend{
			Name:              b.Name,
			Session:           &faultySession{Service: b.Session, track: asTrack(b.Session), fault: fault},
			Memory:            b.Memory,
			SupportsEventPage: b.SupportsEventPage,
			SupportsTTL:       b.SupportsTTL,
		}, nil
	case FaultDropMemory:
		return &Backend{
			Name:              b.Name,
			Session:           b.Session,
			Memory:            &faultyMemory{Service: b.Memory, fault: fault},
			SupportsEventPage: b.SupportsEventPage,
			SupportsTTL:       b.SupportsTTL,
		}, nil
	case FaultOverwriteSummary:
		return nil, fmt.Errorf("overwrite_summary is injected via NewFaultySummarizer, not WrapFaulty")
	default:
		return nil, fmt.Errorf("unknown fault %q", fault)
	}
}

func asTrack(s session.Service) session.TrackService {
	t, _ := s.(session.TrackService)
	return t
}

// faultySession embeds session.Service and overrides only the corrupting
// method. It re-implements AppendTrackEvent because TrackService is a separate
// interface that embedding does not promote.
type faultySession struct {
	session.Service
	track session.TrackService
	fault string
}

func (f *faultySession) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, opts ...session.Option) error {
	switch f.fault {
	case FaultDuplicateEvent:
		if err := f.Service.AppendEvent(ctx, sess, e, opts...); err != nil {
			return err
		}
		return f.Service.AppendEvent(ctx, sess, e, opts...)
	case FaultTamperState:
		if len(e.StateDelta) > 0 {
			for k := range e.StateDelta {
				e.StateDelta[k] = []byte("fault: tampered state")
			}
		}
	}
	return f.Service.AppendEvent(ctx, sess, e, opts...)
}

func (f *faultySession) UpdateSessionState(ctx context.Context, key session.Key, state session.StateMap) error {
	if f.fault == FaultTamperState {
		for k := range state {
			state[k] = []byte("fault: tampered state")
		}
	}
	return f.Service.UpdateSessionState(ctx, key, state)
}

func (f *faultySession) CreateSessionSummary(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
	switch f.fault {
	case FaultDropSummary:
		return nil
	case FaultWrongFilterKey:
		return f.Service.CreateSessionSummary(ctx, sess, filterKey+"-wrong", force)
	}
	return f.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

func (f *faultySession) AppendTrackEvent(ctx context.Context, sess *session.Session, te *session.TrackEvent, opts ...session.Option) error {
	if f.track == nil {
		return nil
	}
	if f.fault == FaultWrongTrackPayload {
		te.Payload = []byte(`{"fault":"wrong payload"}`)
	}
	return f.track.AppendTrackEvent(ctx, sess, te, opts...)
}

// faultyMemory embeds memory.Service and drops writes for drop_memory.
type faultyMemory struct {
	memory.Service
	fault string
}

func (f *faultyMemory) AddMemory(ctx context.Context, userKey memory.UserKey, mem string, topics []string, opts ...memory.AddOption) error {
	if f.fault == FaultDropMemory {
		return nil
	}
	return f.Service.AddMemory(ctx, userKey, mem, topics, opts...)
}

// faultySummarizer returns a fixed wrong summary; the real service persists it
// verbatim, so overwrite_summary needs no service-level setter.
type faultySummarizer struct{}

// NewFaultySummarizer builds a summarizer that always overwrites the summary
// text with a fixed wrong value.
func NewFaultySummarizer() summary.SessionSummarizer { return &faultySummarizer{} }

func (f *faultySummarizer) ShouldSummarize(*session.Session) bool { return true }
func (f *faultySummarizer) Summarize(context.Context, *session.Session) (string, error) {
	return "fault: overwritten summary", nil
}
func (f *faultySummarizer) SetPrompt(string)         {}
func (f *faultySummarizer) SetModel(model.Model)     {}
func (f *faultySummarizer) Metadata() map[string]any { return nil }
