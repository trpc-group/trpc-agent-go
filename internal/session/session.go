//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package session provides internal usage for session service.
package session

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// EnsureEventStartWithUser filters events to ensure they start with RoleUser.
// It removes events from the beginning until it finds the first event from RoleUser.
func EnsureEventStartWithUser(sess *session.Session) {
	if sess == nil || len(sess.Events) == 0 {
		log.Info("session is nil or has no events")
		return
	}
	// Find the first event that starts with RoleUser
	startIndex := -1
	for i, event := range sess.Events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			if event.Response.Choices[0].Message.Role == model.RoleUser {
				startIndex = i
				break
			}
		}
		// If event has no response or choices, continue to next event
	}

	// If no user event found, clear all events
	if startIndex == -1 {
		sess.Events = []event.Event{}
		return
	}

	// Keep events starting from the first user event
	if startIndex > 0 {
		sess.Events = sess.Events[startIndex:]
	}
}

// UpdateUserSession updates the user session with the given event and options.
func UpdateUserSession(sess *session.Session, event *event.Event, opts ...session.Option) {
	if sess == nil || event == nil {
		log.Info("session or event is nil")
		return
	}
	if event.Response != nil && !event.IsPartial && event.IsValidContent() {
		sess.Events = append(sess.Events, *event)

		// Apply filtering options
		ApplyEventFiltering(sess, opts...)
		// Ensure events start with RoleUser after filtering
		EnsureEventStartWithUser(sess)
	}

	sess.UpdatedAt = time.Now()
	if sess.State == nil {
		sess.State = make(session.StateMap)
	}
	ApplyEventStateDelta(sess, event)
}

// ApplyEventFiltering applies event number and time filtering to session events
func ApplyEventFiltering(sess *session.Session, opts ...session.Option) {
	if sess == nil {
		log.Info("session is nil")
		return
	}
	opt := applyOptions(opts...)
	// Apply event number limit
	if opt.EventNum > 0 && len(sess.Events) > opt.EventNum {
		sess.Events = sess.Events[len(sess.Events)-opt.EventNum:]
	}

	// Apply event time filter - keep events after the specified time
	if !opt.EventTime.IsZero() {
		startIndex := -1
		for i, e := range sess.Events {
			if e.Timestamp.After(opt.EventTime) || e.Timestamp.Equal(opt.EventTime) {
				startIndex = i
				break
			}
		}
		if startIndex >= 0 {
			sess.Events = sess.Events[startIndex:]
		} else {
			// No events after the specified time, clear all events
			sess.Events = nil
		}
	}
}

// ApplyEventStateDelta merges the state delta of the event into the session state.
func ApplyEventStateDelta(sess *session.Session, e *event.Event) {
	if sess == nil || e == nil {
		log.Info("session or event is nil")
		return
	}
	if sess.State == nil {
		sess.State = make(session.StateMap)
	}
	for key, value := range e.StateDelta {
		sess.State[key] = value
	}
}

// ApplyEventStateDeltaMap merges the state delta of the event into the session state.
func ApplyEventStateDeltaMap(state session.StateMap, e *event.Event) {
	if state == nil || e == nil {
		log.Info("state or event is nil")
		return
	}

	for key, value := range e.StateDelta {
		state[key] = value
	}
}

func applyOptions(opts ...session.Option) *session.Options {
	opt := &session.Options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}
