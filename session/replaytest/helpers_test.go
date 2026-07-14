//go:build cgo

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// makeBackends creates InMemory and SQLite Backend instances for a given session key.
func makeBackends(t *testing.T, key session.Key) []Backend {
	t.Helper()
	inMemFactory := inMemoryFactory{}
	sqliteFact := sqliteFactory{}

	inMemBackend := inMemFactory.Create(context.Background(), t)
	sqliteBackend := sqliteFact.Create(context.Background(), t)

	// Override session keys.
	inMemBackend.SessKey = func() session.Key { return key }
	sqliteBackend.SessKey = func() session.Key { return key }

	return []Backend{*inMemBackend, *sqliteBackend}
}

// requireNoUnexpectedDiff asserts that a CaseResult has no unexpected diffs.
func requireNoUnexpectedDiff(t *testing.T, result CaseResult) {
	t.Helper()
	require.False(t, HasUnexpectedDiff(result),
		"unexpected diffs in %s: %+v", result.Name, result.Diffs)
}

// injectDrift mutates a snapshot by modifying a value at the given path.
func injectDrift(snap *Snapshot, section string, mutate func(any) any) {
	switch section {
	case "events":
		if len(snap.Events) > 0 {
			for k, v := range snap.Events[0] {
				snap.Events[0][k] = mutate(v)
				return
			}
		}
	case "state":
		for k, v := range snap.State {
			snap.State[k] = mutate(v)
			return
		}
	case "memories":
		if len(snap.Memories) > 0 {
			snap.Memories[0].Content = fmt.Sprintf("%s-drifted", snap.Memories[0].Content)
		}
	case "summaries":
		for k, s := range snap.Summaries {
			s.Text = fmt.Sprintf("%s-drifted", s.Text)
			snap.Summaries[k] = s
			return
		}
	case "tracks":
		for k, events := range snap.Tracks {
			if len(events) > 0 {
				events[0].Track = fmt.Sprintf("%s-drifted", events[0].Track)
				snap.Tracks[k] = events
				return
			}
		}
	}
}

// newEventWithStateDeltaNull creates an event where StateDelta has nil values.
func newEventWithStateDeltaNull(content string, keys ...string) *event.Event {
	delta := make(map[string][]byte, len(keys))
	for _, k := range keys {
		delta[k] = nil
	}
	return newAssistantEventWithStateDelta(content, delta)
}

// newTrackEventWithVolatile creates a track event with volatile payload keys.
func newTrackEventWithVolatile(track string, payload map[string]any) *session.TrackEvent {
	b, _ := json.Marshal(payload)
	return &session.TrackEvent{
		Track:     session.Track(track),
		Payload:   json.RawMessage(b),
		Timestamp: time.Now(),
	}
}
