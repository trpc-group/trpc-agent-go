//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tooljournal records conservative tool dispatch facts in session state.
package tooljournal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Prefix identifies versioned framework-owned session state, not model messages.
const Prefix = "__tool_execution_v1/"
const recorderKey = "__tool_execution_recorder__"

// Call is one logical call, including the number of admitted raw attempts.
// Dispatch includes callbacks, which can themselves perform external work.
type Call struct {
	Tool     model.ToolCall `json:"tool"`
	Status   string         `json:"status"`
	Attempts int            `json:"attempts,omitempty"`
	Choices  []model.Choice `json:"choices,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// Record is a versioned snapshot; its source event disambiguates reused call IDs.
type Record struct {
	Response      *model.Response `json:"response,omitempty"`
	Version       int             `json:"version"`
	ID            string          `json:"id"`
	SourceEventID string          `json:"source_event_id"`
	RequestID     string          `json:"request_id"`
	InvocationID  string          `json:"invocation_id"`
	Calls         []Call          `json:"calls"`
}

// Recorder owns writes for one run. Its append function outlives output closure.
type Recorder struct {
	mu      sync.Mutex
	changed func()
	flows   atomic.Int64
	active  atomic.Int64
	append  func(context.Context, *event.Event) error
}

// Attach enables tracking on an invocation and its clones.
func Attach(inv *agent.Invocation, appendEvent func(context.Context, *event.Event) error) {
	inv.SetState(recorderKey, &Recorder{append: appendEvent})
}

// Round owns serialized updates for one model tool-call response.
type Round struct {
	mu        sync.Mutex
	recorder  *Recorder
	record    Record
	author    string
	filterKey string
}
type roundKey struct{}
type sourceKey struct{}

// WithSource identifies the exact model event even before Runner consumes it.
func WithSource(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sourceKey{}, id)
}

// Inherit keeps tracking when a callback replaces its execution context.
func Inherit(ctx, original context.Context) context.Context {
	if r := fromContext(original); r != nil {
		return context.WithValue(ctx, roundKey{}, r)
	}
	return ctx
}

// Begin persists all planned calls before any dispatch. Missing evidence is
// never interpreted as proof that a call did not execute.
func Begin(ctx context.Context, inv *agent.Invocation, rsp *model.Response) (context.Context, error) {
	recorder, ok := agent.GetStateValue[*Recorder](inv, recorderKey)
	if !ok {
		return ctx, nil
	}
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	if inv.Session == nil {
		return ctx, errors.New("tool tracking requires a session")
	}
	calls := providerdata.ToolCallsForExecution(rsp.Choices[0].Message)
	encoded, err := json.Marshal(calls)
	if err != nil {
		return ctx, err
	}
	var source event.Event
	inv.Session.EventMu.RLock()
	for i := len(inv.Session.Events) - 1; i >= 0; i-- {
		e := inv.Session.Events[i]
		if !e.IsToolCallResponse() || e.IsPartial {
			continue
		}
		b, _ := json.Marshal(providerdata.ToolCallsForExecution(e.Choices[0].Message))
		if string(b) == string(encoded) && e.RequestID == inv.RunOptions.RequestID {
			source = e
			break
		}
	}
	inv.Session.EventMu.RUnlock()
	if id, ok := ctx.Value(sourceKey{}).(string); ok && id != "" {
		source = *event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp.Clone())
		source.ID = id
		source.RequestID = inv.RunOptions.RequestID
		source.Branch = inv.Branch
		source.FilterKey = inv.GetEventFilterKey()
	}
	if source.ID == "" {
		return ctx, errors.New("tool tracking requires a persisted tool-call event")
	}
	// Persist the source before work can cancel the output loop. The shared
	// coordinator deduplicates this with normal event consumption by event ID.
	sourceCtx, sourceCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	sourceErr := recorder.append(sourceCtx, &source)
	sourceCancel()
	if sourceErr != nil {
		return ctx, fmt.Errorf("persist tool-call source: %w", sourceErr)
	}

	round := &Round{recorder: recorder, author: source.Author, filterKey: source.FilterKey,
		record: Record{Version: 1, ID: uuid.NewString(), SourceEventID: source.ID, Response: rsp.Clone(),
			RequestID: inv.RunOptions.RequestID, InvocationID: inv.InvocationID}}
	for _, c := range calls {
		round.record.Calls = append(round.record.Calls, Call{Tool: c, Status: "not_started"})
	}
	if err := round.save(ctx); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, roundKey{}, round), nil
}

func fromContext(ctx context.Context) *Round { r, _ := ctx.Value(roundKey{}).(*Round); return r }

// Dispatch records conservative admission before any callback or tool work.
func Dispatch(ctx context.Context, id string) error {
	err := update(ctx, id, func(c *Call) { c.Status = "dispatched" })
	if err == nil {
		if r := fromContext(ctx); r != nil {
			r.recorder.active.Add(1)
		}
	}
	return err
}

// Attempt records each raw call admission, including retries, before calling it.
func Attempt(ctx context.Context, id string) error {
	return update(ctx, id, func(c *Call) { c.Attempts++ })
}

func update(ctx context.Context, id string, apply func(*Call)) error {
	r := fromContext(ctx)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.record.Calls {
		if r.record.Calls[i].Tool.ID == id {
			apply(&r.record.Calls[i])
			return r.save(ctx)
		}
	}
	return errors.New("tool execution record does not contain call")
}

// Finish preserves rendered outcomes even when the execution context is cancelled.
// Cancellation errors and rendering failures remain unknown, never safe to replay.
func Finish(ctx context.Context, id string, choices []model.Choice, callErr error) error {
	if r := fromContext(ctx); r != nil {
		defer func() { r.recorder.active.Add(-1); r.recorder.notify() }()
	}
	return update(ctx, id, func(c *Call) {
		c.Status = "completed"
		c.Choices = choices
		if callErr != nil {
			c.Error = callErr.Error()
			c.Status = "failed"
		}
		if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) || (len(choices) == 0 && callErr == nil) {
			c.Status = "unknown"
		}
	})
}

func (r *Round) save(ctx context.Context) error {
	b, err := json.Marshal(r.record)
	if err != nil {
		return err
	}
	e := event.New(r.record.InvocationID, r.author, event.WithObject(model.ObjectTypeStateUpdate))
	e.RequestID = r.record.RequestID
	e.FilterKey = r.filterKey
	e.StateDelta = map[string][]byte{Prefix + r.record.ID: b}
	// Use the existing state-update protocol. No fake assistant/tool content is
	// necessary, and stores that omit contentless events still persist the state.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if err := r.recorder.append(persistCtx, e); err != nil {
		return errors.Join(fmt.Errorf("persist tool execution: %w", err), agent.NewStopError("tool execution persistence failed"))
	}
	return nil
}

// Records reads independent snapshots. Unknown versions and malformed records
// are errors so callers cannot accidentally fall back to unsafe legacy replay.
func Records(sess *session.Session) ([]Record, error) {
	if sess == nil {
		return nil, nil
	}
	var records []Record
	for key, value := range sess.SnapshotState() {
		if len(key) < len(Prefix) || key[:len(Prefix)] != Prefix {
			continue
		}
		var record Record
		if err := json.Unmarshal(value, &record); err != nil {
			return nil, fmt.Errorf("read tool execution: %w", err)
		}
		if record.Version != 1 {
			return nil, fmt.Errorf("unsupported tool execution version %d", record.Version)
		}
		records = append(records, record)
	}
	return records, nil
}

// Active reports logical tool executions admitted but not yet finalized.
func Active(inv *agent.Invocation) int {
	r, _ := agent.GetStateValue[*Recorder](inv, recorderKey)
	if r == nil {
		return 0
	}
	return int(r.active.Load())
}

// StartFlow tracks the real flow lifetime, including work hidden by an agent
// wrapper that closes its public event channel early on cancellation.
func StartFlow(inv *agent.Invocation) func() {
	r, _ := agent.GetStateValue[*Recorder](inv, recorderKey)
	if r == nil {
		return func() {}
	}
	r.flows.Add(1)
	return func() { r.flows.Add(-1); r.notify() }
}

// Flows reports standard LLMAgent flows that have not returned.
func Flows(inv *agent.Invocation) int {
	r, _ := agent.GetStateValue[*Recorder](inv, recorderKey)
	if r == nil {
		return 0
	}
	return int(r.flows.Load())
}

// OnChange installs the run's observer and immediately reconciles its snapshot.
func OnChange(inv *agent.Invocation, callback func()) {
	r, _ := agent.GetStateValue[*Recorder](inv, recorderKey)
	if r == nil {
		return
	}
	r.mu.Lock()
	r.changed = callback
	r.mu.Unlock()
	callback()
}

func (r *Recorder) notify() {
	r.mu.Lock()
	callback := r.changed
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
}
