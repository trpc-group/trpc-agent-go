//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestEventFilteringDetachesDiscardedPrefix(t *testing.T) {
	base := time.Unix(100, 0)
	tests := []struct {
		name   string
		filter func(*Session)
		start  int
	}{
		{
			name:   "first user",
			filter: func(s *Session) { s.ApplyEventFiltering() },
			start:  1,
		},
		{
			name:   "count",
			filter: func(s *Session) { s.ApplyEventFiltering(WithEventNum(2)) },
			start:  3,
		},
		{
			name: "time",
			filter: func(s *Session) {
				s.ApplyEventFiltering(WithEventTime(base.Add(3 * time.Second)))
			},
			start: 3,
		},
		{
			name: "time and count and first user",
			filter: func(s *Session) {
				s.ApplyEventFiltering(WithEventTime(base.Add(time.Second)), WithEventNum(3))
			},
			start: 3,
		},
		{
			name:   "restored user anchor",
			filter: func(s *Session) { s.ApplyEventFiltering(WithEventNum(1)) },
			start:  3,
		},
		{
			name:   "ensure first user",
			filter: (*Session).EnsureEventStartWithUser,
			start:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []event.Event{
				*createTestEvent(model.RoleAssistant, "discarded", base, nil),
				*createTestEvent(model.RoleUser, "first user", base.Add(time.Second), nil),
				*createTestEvent(model.RoleAssistant, "first answer", base.Add(2*time.Second), nil),
				*createTestEvent(model.RoleUser, "last user", base.Add(3*time.Second), nil),
				*createTestEvent(model.RoleAssistant, "last answer", base.Add(4*time.Second), nil),
			}
			original := slices.Clone(events)
			sess := &Session{Events: events}
			tt.filter(sess)

			require.Equal(t, original[tt.start:], sess.Events)
			// Detach instead of clearing the prefix: other readers may still
			// own the original slice, including the discarded events.
			assert.Equal(t, original, events)
			require.NotSame(t, &events[tt.start], &sess.Events[0],
				"the retained history must not keep the discarded prefix reachable")
			for i := range sess.Events {
				assert.Same(t, events[tt.start+i].Response, sess.Events[i].Response,
					"retained payloads should not be deep-copied")
			}

			// Repeated filtering of the already-trimmed history needs no copy.
			retained := &sess.Events[0]
			sess.ApplyEventFiltering()
			assert.Same(t, retained, &sess.Events[0])
			sess.EnsureEventStartWithUser()
			assert.Same(t, retained, &sess.Events[0])
		})
	}
}
