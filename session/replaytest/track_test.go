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
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrackCaseE2E(t *testing.T) {
	report := runReplayCaseReport(t, CaseTrackEvents)
	require.Equal(t, 1, report.PassedCases)

	snapshot := runReplayCaseSnapshot(t, CaseTrackEvents)
	require.NotNil(t, snapshot.Session)
	require.NotEmpty(t, snapshot.Session.Tracks)

	trackEvents, ok := snapshot.Session.Tracks[session.Track("tool")]
	require.True(t, ok)
	require.Equal(t, session.Track("tool"), trackEvents.Track)
	require.Len(t, trackEvents.Events, 2)
	require.JSONEq(t, `{"duration_ms":5}`, string(trackEvents.Events[0].Payload))
	require.JSONEq(t, `{"status":"ok"}`, string(trackEvents.Events[1].Payload))
}

func TestTrackFaultDetection(t *testing.T) {
	base := trackSnapshot("a", session.Track("tool"), []session.TrackEvent{
		*trackEvent("tool", `{"duration_ms":5}`),
		*trackEvent("tool", `{"status":"ok"}`),
	})

	tests := []struct {
		name string
		mut  func(*SessionSnapshot)
	}{
		{
			name: "payload_tampered",
			mut: func(s *SessionSnapshot) {
				s.Session.Tracks["tool"].Events[0].Payload = json.RawMessage(`{"duration_ms":7}`)
			},
		},
		{
			name: "track_name_wrong",
			mut: func(s *SessionSnapshot) {
				trackEvents := s.Session.Tracks["tool"]
				delete(s.Session.Tracks, "tool")
				trackEvents.Track = "wrong"
				for i := range trackEvents.Events {
					trackEvents.Events[i].Track = "wrong"
				}
				s.Session.Tracks["wrong"] = trackEvents
			},
		},
		{
			name: "events_lost",
			mut: func(s *SessionSnapshot) {
				s.Session.Tracks["tool"].Events = s.Session.Tracks["tool"].Events[:1]
			},
		},
		{
			name: "timestamp_wrong",
			mut: func(s *SessionSnapshot) {
				s.Session.Tracks["tool"].Events[0].Timestamp = fixedTime.Add(time.Second).UTC()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneSnapshot(base)
			changed.BackendName = "b"
			tc.mut(changed)

			result := NewComparator().Compare(base, changed, nil, InMemoryProfile(), InMemoryProfile())
			require.Equal(t, StatusFailed, result.Status)
			require.NotEmpty(t, result.Diffs)
		})
	}
}

func trackSnapshot(backend string, track session.Track, events []session.TrackEvent) *SessionSnapshot {
	sess := session.NewSession("app", "user", "sess")
	sess.CreatedAt = time.Time{}
	sess.UpdatedAt = time.Time{}
	sess.Tracks = map[session.Track]*session.TrackEvents{
		track: {
			Track:  track,
			Events: events,
		},
	}
	norm, err := NewNormalizer().Normalize(&SessionSnapshot{BackendName: backend, Session: sess})
	if err != nil {
		panic(err)
	}
	return norm
}
