//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNormalizer(t *testing.T) {
	base := time.Date(2026, 7, 1, 1, 2, 3, 4, time.FixedZone("CST", 8*3600))
	evt := event.NewResponseEvent("inv", "user", &model.Response{
		Choices: []model.Choice{{Message: model.NewUserMessage("hello")}},
	}, event.WithTag("case.one"))
	evt.ID = "generated"
	evt.Timestamp = base
	evt.Extensions = map[string]json.RawMessage{
		"z": json.RawMessage(`{"b":2,"a":1}`),
	}
	sess := session.NewSession("app", "user", "sess", session.WithSessionEvents([]event.Event{*evt}))
	sess.State["_private"] = []byte("drop")
	sess.State["visible"] = []byte("keep")
	sess.Summaries["branch"] = &session.Summary{
		Summary:   "sum",
		Topics:    []string{"z", "a"},
		UpdatedAt: base,
	}
	sess.Tracks = map[session.Track]*session.TrackEvents{
		"trace": {
			Track: "trace",
			Events: []session.TrackEvent{{
				Track:     "trace",
				Payload:   json.RawMessage(`{"z":2,"a":1}`),
				Timestamp: base,
			}},
		},
	}
	snap := &SessionSnapshot{
		BackendName: "a",
		Session:     sess,
		Memories: []*memory.Entry{{
			ID:        "m1",
			CreatedAt: base,
			UpdatedAt: base,
			Memory: &memory.Memory{
				Memory:       "pref",
				Topics:       []string{"z", "a"},
				Participants: []string{"bob", "alice"},
			},
		}},
	}

	norm, err := NewNormalizer().Normalize(snap)
	require.NoError(t, err)
	require.Equal(t, "generated", snap.Session.Events[0].ID, "normalizer must not mutate input")
	require.Equal(t, "case.one", norm.Session.Events[0].ID)
	require.NotContains(t, norm.Session.State, "_private")
	require.Equal(t, []string{"a", "z"}, norm.Memories[0].Memory.Topics)
	require.Equal(t, []string{"alice", "bob"}, norm.Memories[0].Memory.Participants)
	require.Equal(t, base.UTC(), norm.Session.Events[0].Timestamp)
	require.JSONEq(t, `{"a":1,"b":2}`, string(norm.Session.Events[0].Extensions["z"]))
	require.JSONEq(t, `{"a":1,"z":2}`, string(norm.Session.Tracks["trace"].Events[0].Payload))
}

func TestNormalizeTime(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	tm := time.Date(2026, 7, 1, 12, 0, 0, 0, loc)
	require.Equal(t, tm.UTC(), normalizeTime(tm))
}
