//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package session provides internal session functionality.
package session

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// groupEventsByFilterKey groups events by filter key with backward
// compatibility. If the event version is not current, fall back to Branch.
// The empty key is a valid group key.
func groupEventsByFilterKey(evs []event.Event) map[string][]event.Event {
	m := make(map[string][]event.Event)
	for _, e := range evs {
		key := e.FilterKey
		if e.Version != event.CurrentVersion {
			key = e.Branch
		}
		m[key] = append(m[key], e)
	}
	return m
}

// computeDeltaSince returns events that occurred strictly after the given
// time. When since is zero, all events are returned.
func computeDeltaSince(evs []event.Event, since time.Time) []event.Event {
	if since.IsZero() {
		return evs
	}
	out := make([]event.Event, 0, len(evs))
	for _, e := range evs {
		if e.Timestamp.After(since) {
			out = append(out, e)
		}
	}
	return out
}

// prependPrevSummary returns a new slice that prepends the previous summary as
// a synthetic system event when prevSummary is non-empty, followed by delta.
func prependPrevSummary(prevSummary string, delta []event.Event, now time.Time) []event.Event {
	if prevSummary == "" {
		return delta
	}
	out := make([]event.Event, 0, len(delta)+1)
	out = append(out, event.Event{
		Author:    "system",
		Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Content: prevSummary}}}},
		Timestamp: now,
	})
	out = append(out, delta...)
	return out
}

// buildBranchSession builds a temporary session containing branch events.
func buildBranchSession(base *session.Session, branch string, evs []event.Event) *session.Session {
	return &session.Session{
		ID:        base.ID + ":" + branch,
		AppName:   base.AppName,
		UserID:    base.UserID,
		State:     nil,
		Events:    evs,
		UpdatedAt: time.Now(),
		CreatedAt: base.CreatedAt,
	}
}

// SummarizeAndPersist performs per-branch delta summarization using the given
// summarizer and writes results via the provided write callback.
// - When filterKey is non-empty, summarizes the filtered branch only.
// - When filterKey is empty, summarizes all branches grouped by filter key.
// The getPrev callback returns previous summary text and its UpdatedAt time.
func SummarizeAndPersist(
	ctx context.Context,
	m summary.SessionSummarizer,
	base *session.Session,
	filterKey string,
	force bool,
	getPrev func(key string) (string, time.Time),
	write func(key string, text string) error,
) error {
	if m == nil || base == nil {
		return nil
	}

	process := func(key string, evs []event.Event) error {
		if len(evs) == 0 {
			return nil
		}
		prevText, prevAt := getPrev(key)
		delta := computeDeltaSince(evs, prevAt)
		if !force && len(delta) == 0 {
			return nil
		}
		input := prependPrevSummary(prevText, delta, time.Now())
		tmp := buildBranchSession(base, key, input)
		if !force && !m.ShouldSummarize(tmp) {
			return nil
		}
		text, err := m.Summarize(ctx, tmp)
		if err != nil || text == "" {
			return nil
		}
		return write(key, text)
	}

	if filterKey != "" {
		matched := make([]event.Event, 0, len(base.Events))
		for _, e := range base.Events {
			if e.Filter(filterKey) {
				matched = append(matched, e)
			}
		}
		return process(filterKey, matched)
	}

	for key, evs := range groupEventsByFilterKey(base.Events) {
		if err := process(key, evs); err != nil {
			log.Warnf("summarize and persist failed for filter %s: %v", key, err)
		}
	}
	return nil
}
