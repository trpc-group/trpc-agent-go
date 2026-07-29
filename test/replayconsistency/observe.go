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
	"context"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Observation is the canonical projection of everything a backend persisted
// for one replay case.
//
// The comparator only sees this projection, so it defines the contract under
// test. Anything a backend stores outside it is invisible on purpose;
// anything inside it is compared.
type Observation struct {
	Backend  string        `json:"backend" diffskip:"backend identity; the field naming the backend differs by definition"`
	Sessions []SessionView `json:"sessions"`
	Memories *MemoryView   `json:"memories,omitempty"`
}

// SessionView projects one session.
type SessionView struct {
	// Ref is the app/user/session triple the view was read back under.
	Ref string `json:"ref" diffkey:"true"`
	// Exists reports whether the backend returned the session at all. A
	// missing session is a first-class observation rather than an error, so
	// that a backend which loses a session diverges instead of aborting.
	Exists    bool          `json:"exists"`
	State     []StateEntry  `json:"state,omitempty"`
	Events    []EventView   `json:"events,omitempty"`
	Summaries []SummaryView `json:"summaries,omitempty"`
	Tracks    []TrackView   `json:"tracks,omitempty"`
}

// EventView projects one persisted event.
type EventView struct {
	Index        int              `json:"index"`
	ID           string           `json:"id"`
	InvocationID string           `json:"invocationId,omitempty"`
	Author       string           `json:"author,omitempty"`
	Offset       string           `json:"offset,omitempty"`
	Role         string           `json:"role,omitempty"`
	Content      string           `json:"content,omitempty"`
	ToolCalls    []ToolCallView   `json:"toolCalls,omitempty"`
	ToolID       string           `json:"toolId,omitempty"`
	ToolName     string           `json:"toolName,omitempty"`
	Branch       string           `json:"branch,omitempty"`
	Tag          string           `json:"tag,omitempty"`
	FilterKey    string           `json:"filterKey,omitempty"`
	StateDelta   []StateEntry     `json:"stateDelta,omitempty"`
	Extensions   []ExtensionEntry `json:"extensions,omitempty"`
}

// ToolCallView projects one tool call carried by an event.
type ToolCallView struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
	Name string `json:"name"`
	// Arguments is canonical JSON so that member order does not matter.
	Arguments string `json:"arguments,omitempty"`
}

// SummaryView projects one stored summary.
type SummaryView struct {
	// FilterKey selects the conversation branch. The empty string is the
	// whole-session summary.
	FilterKey string   `json:"filterKey" diffkey:"true"`
	Text      string   `json:"text"`
	Topics    []string `json:"topics,omitempty"`
	// UpdatedOffset is derived by the framework from the summarized event
	// boundary rather than the wall clock, so it is compared exactly.
	UpdatedOffset string `json:"updatedOffset,omitempty"`
	// OwnerRef records which session the backend actually surfaced the summary
	// under. A summary that appears under the wrong session is a distinct
	// fault class, and it is only visible if ownership is projected.
	OwnerRef        string `json:"ownerRef"`
	BoundaryVersion int    `json:"boundaryVersion,omitempty"`
	// BoundaryFilterKey is stored inside the boundary and can drift away from
	// the map key the summary is filed under. Projecting both makes that drift
	// observable.
	BoundaryFilterKey    string `json:"boundaryFilterKey,omitempty"`
	BoundaryCutoffOffset string `json:"boundaryCutoffOffset,omitempty"`
	BoundaryLastEventID  string `json:"boundaryLastEventId,omitempty"`
}

// TrackView projects one observability track.
type TrackView struct {
	Track  string           `json:"track" diffkey:"true"`
	Events []TrackEventView `json:"events,omitempty"`
}

// TrackEventView projects one track entry.
type TrackEventView struct {
	Index  int    `json:"index"`
	Offset string `json:"offset,omitempty"`
	// Payload is canonical JSON.
	Payload string `json:"payload,omitempty"`
}

// MemoryView projects the memories stored for one user.
type MemoryView struct {
	Ref string `json:"ref"`
	// Entries are ordered by identifier so that comparison covers membership
	// and content without depending on read-back order.
	Entries []MemoryEntryView `json:"entries,omitempty"`
	// ReadOrder is the identifier sequence the backend actually returned. It is
	// recorded for debugging but deliberately not compared.
	//
	// ReadMemories orders by an update timestamp the service assigns from the
	// wall clock, so writes landing in the same tick tie, and the tie breaks
	// differently between backends and between runs of the same backend.
	// Comparing it produces a result that changes run to run, which is noise
	// rather than signal: it would make the artifact churn and teach readers to
	// ignore the differences it reports. Membership, identifiers, content and
	// topics are still compared exactly through Entries.
	ReadOrder []string `json:"readOrder,omitempty" diffskip:"ReadMemories ties on a wall-clock timestamp, so the order is not reproducible even for one backend"`
}

// MemoryEntryView projects one memory.
type MemoryEntryView struct {
	// ID is the content-derived identifier. Backends compute it with the same
	// shared helper, so they must agree on it, and a rotation on update must
	// happen everywhere or nowhere.
	ID      string   `json:"id" diffkey:"true"`
	Content string   `json:"content"`
	Topics  []string `json:"topics,omitempty"`
}

// observe reads back everything the scenario touched and projects it.
func observe(ctx context.Context, tgt *target, sc Scenario) (*Observation, error) {
	obs := &Observation{Backend: tgt.name}
	for _, ref := range sc.Sessions {
		view, err := observeSession(ctx, tgt, ref)
		if err != nil {
			return nil, err
		}
		obs.Sessions = append(obs.Sessions, view)
	}
	if sc.MemoryUser != nil && tgt.memory != nil {
		view, err := observeMemory(ctx, tgt, *sc.MemoryUser)
		if err != nil {
			return nil, err
		}
		obs.Memories = view
	}
	return obs, nil
}

func observeSession(ctx context.Context, tgt *target, ref SessionRef) (SessionView, error) {
	view := SessionView{Ref: ref.String()}
	sess, err := tgt.session.GetSession(ctx, ref.Key())
	if err != nil {
		return view, fmt.Errorf("observe session %s: %w", ref, err)
	}
	if sess == nil {
		return view, nil
	}
	view.Exists = true
	view.State = stateEntries(sess.State)
	view.Events = eventViews(sess.Events, tgt.base)
	view.Summaries = summaryViews(ref, sess.Summaries, tgt.base)
	if tgt.caps.Tracks {
		view.Tracks = trackViews(sess.Tracks, tgt.base)
	}
	return view, nil
}

func eventViews(events []event.Event, base time.Time) []EventView {
	if len(events) == 0 {
		return nil
	}
	out := make([]EventView, 0, len(events))
	for i := range events {
		e := events[i]
		view := EventView{
			Index:        i,
			ID:           e.ID,
			InvocationID: e.InvocationID,
			Author:       e.Author,
			Offset:       offsetFrom(e.Timestamp, base),
			Branch:       e.Branch,
			Tag:          e.Tag,
			FilterKey:    e.FilterKey,
			StateDelta:   stateEntries(e.StateDelta),
			Extensions:   extensionEntries(e.Extensions),
		}
		if msg, ok := eventMessage(&e); ok {
			view.Role = string(msg.Role)
			view.Content = msg.Content
			view.ToolID = msg.ToolID
			view.ToolName = msg.ToolName
			view.ToolCalls = toolCallViews(msg.ToolCalls)
		}
		out = append(out, view)
	}
	return out
}

// eventMessage returns the message an event carries, if any.
func eventMessage(e *event.Event) (model.Message, bool) {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return model.Message{}, false
	}
	return e.Response.Choices[0].Message, true
}

func toolCallViews(calls []model.ToolCall) []ToolCallView {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCallView, 0, len(calls))
	for _, c := range calls {
		out = append(out, ToolCallView{
			ID:        c.ID,
			Type:      c.Type,
			Name:      c.Function.Name,
			Arguments: canonicalJSON(c.Function.Arguments),
		})
	}
	return out
}

func summaryViews(ref SessionRef, summaries map[string]*session.Summary, base time.Time) []SummaryView {
	if len(summaries) == 0 {
		return nil
	}
	out := make([]SummaryView, 0, len(summaries))
	for filterKey, sum := range summaries {
		if sum == nil {
			continue
		}
		view := SummaryView{
			FilterKey:     filterKey,
			Text:          sum.Summary,
			Topics:        sortedCopy(sum.Topics),
			UpdatedOffset: offsetFrom(sum.UpdatedAt, base),
			OwnerRef:      ref.String(),
		}
		if b := sum.Boundary; b != nil {
			view.BoundaryVersion = b.Version
			view.BoundaryFilterKey = b.FilterKey
			view.BoundaryCutoffOffset = offsetFrom(b.CutoffAt, base)
			view.BoundaryLastEventID = b.LastEventID
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FilterKey < out[j].FilterKey })
	return out
}

func trackViews(tracks map[session.Track]*session.TrackEvents, base time.Time) []TrackView {
	if len(tracks) == 0 {
		return nil
	}
	out := make([]TrackView, 0, len(tracks))
	for name, te := range tracks {
		view := TrackView{Track: string(name)}
		if te != nil {
			for i, ev := range te.Events {
				view.Events = append(view.Events, TrackEventView{
					Index:   i,
					Offset:  offsetFrom(ev.Timestamp, base),
					Payload: canonicalJSON(ev.Payload),
				})
			}
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Track < out[j].Track })
	return out
}

func observeMemory(ctx context.Context, tgt *target, ref SessionRef) (*MemoryView, error) {
	entries, err := tgt.memory.ReadMemories(ctx, ref.MemoryUserKey(), memoryReadLimit)
	if err != nil {
		return nil, fmt.Errorf("observe memories for %s: %w", ref, err)
	}
	view := &MemoryView{Ref: ref.AppName + "/" + ref.UserID}
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		view.Entries = append(view.Entries, MemoryEntryView{
			ID:      entry.ID,
			Content: entry.Memory.Memory,
			Topics:  sortedCopy(entry.Memory.Topics),
		})
		view.ReadOrder = append(view.ReadOrder, entry.ID)
	}
	sort.Slice(view.Entries, func(i, j int) bool { return view.Entries[i].ID < view.Entries[j].ID })
	return view, nil
}
