//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replayconsistency

import (
	"context"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	localTTLProbeDuration        = 150 * time.Millisecond
	localTTLProbeTimeout         = 5 * time.Second
	expectedPaginationProbeCount = 2
)

func TestLightweightPaginationCapabilityProbes(t *testing.T) {
	backends := []replaytest.Backend{
		newInMemoryBackend(),
		newSQLiteBackend(t.TempDir()),
	}
	for _, backend := range backends {
		t.Run(backend.Name, func(t *testing.T) {
			report, err := runPaginationProbes(context.Background(), backend)
			if err != nil {
				t.Fatalf("runPaginationProbes() error = %v", err)
			}
			assertPaginationProbeReport(t, backend.Name, report)
		})
	}
}

func TestLightweightTTLExpiryProbes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), localTTLProbeTimeout)
	defer cancel()
	config := BackendConfig{SessionTTL: localTTLProbeDuration}
	backends := []replaytest.Backend{
		newInMemoryBackendWithConfig(config),
		newSQLiteBackendWithConfig(t.TempDir(), config),
	}
	for _, backend := range backends {
		t.Run(backend.Name, func(t *testing.T) {
			result, err := runTTLExpiryProbe(ctx, backend)
			if err != nil {
				t.Fatalf("runTTLExpiryProbe() error = %v", err)
			}
			if result.Status != replaytest.ResultPass {
				t.Fatalf("TTL probe = %#v", result)
			}
		})
	}
}

func TestSessionPaginationProbeRejectsInvalidUnpagedOracle(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func([]*session.Session) []*session.Session
	}{
		{name: "missing", corrupt: func(sessions []*session.Session) []*session.Session {
			return append([]*session.Session(nil), sessions[:len(sessions)-1]...)
		}},
		{name: "duplicate", corrupt: func(sessions []*session.Session) []*session.Session {
			cloned := append([]*session.Session(nil), sessions...)
			cloned[len(cloned)-1] = cloned[0]
			return cloned
		}},
		{name: "extra", corrupt: func(sessions []*session.Session) []*session.Session {
			return append(append([]*session.Session(nil), sessions...), &session.Session{ID: "extra"})
		}},
		{name: "wrong order", corrupt: func(sessions []*session.Session) []*session.Session {
			cloned := append([]*session.Session(nil), sessions...)
			cloned[0], cloned[len(cloned)-1] = cloned[len(cloned)-1], cloned[0]
			return cloned
		}},
		{name: "wrong app", corrupt: func(sessions []*session.Session) []*session.Session {
			cloned := append([]*session.Session(nil), sessions...)
			cloned[0] = cloned[0].Clone()
			cloned[0].AppName = "other-app"
			return cloned
		}},
		{name: "wrong user", corrupt: func(sessions []*session.Session) []*session.Session {
			cloned := append([]*session.Session(nil), sessions...)
			cloned[0] = cloned[0].Clone()
			cloned[0].UserID = "other-user"
			return cloned
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &corruptingListSessionService{
				Service: sessioninmemory.NewSessionService(), corrupt: test.corrupt,
			}
			fixture := newReplayFixture(replayFixtureConfig{
				name: "corrupt", sessionService: service,
				memoryService: memoryinmemory.NewMemoryService(), summarizer: &replaySummarizer{},
			})
			t.Cleanup(func() {
				if err := fixture.Close(); err != nil {
					t.Errorf("close fixture: %v", err)
				}
			})
			result := runSessionPagingProbe(context.Background(), fixture)
			if result.Status != replaytest.ResultFail || result.Explanation == "" {
				t.Fatalf("session paging probe = %#v", result)
			}
		})
	}
}

func TestSessionPaginationProbeRejectsNilPagedSession(t *testing.T) {
	service := &corruptingListSessionService{
		Service: sessioninmemory.NewSessionService(),
		corruptPage: func([]*session.Session) []*session.Session {
			return []*session.Session{nil}
		},
	}
	fixture := newReplayFixture(replayFixtureConfig{
		name: "nil-page", sessionService: service,
		memoryService: memoryinmemory.NewMemoryService(), summarizer: &replaySummarizer{},
	})
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	result := runSessionPagingProbe(context.Background(), fixture)
	if result.Status != replaytest.ResultFail || !strings.Contains(result.Explanation, "is nil") {
		t.Fatalf("session paging probe = %#v", result)
	}
}

func TestSessionPaginationProbeRejectsWrongPagedScope(t *testing.T) {
	service := &corruptingListSessionService{
		Service: sessioninmemory.NewSessionService(),
		corruptPage: func(sessions []*session.Session) []*session.Session {
			cloned := append([]*session.Session(nil), sessions...)
			cloned[0] = cloned[0].Clone()
			cloned[0].UserID = "other-user"
			return cloned
		},
	}
	fixture := newReplayFixture(replayFixtureConfig{
		name: "wrong-page-scope", sessionService: service,
		memoryService: memoryinmemory.NewMemoryService(), summarizer: &replaySummarizer{},
	})
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	result := runSessionPagingProbe(context.Background(), fixture)
	if result.Status != replaytest.ResultFail ||
		!strings.Contains(result.Explanation, "other-user") {
		t.Fatalf("session paging probe = %#v", result)
	}
}

func TestValidateEventPagesRejectsInvalidResults(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	newPages := func(ids []string) (*session.Session, *session.Session, *session.Session) {
		events := make([]event.Event, len(ids))
		for i, id := range ids {
			events[i].ID = id
		}
		newSession := func(page []event.Event) *session.Session {
			return &session.Session{
				AppName: key.AppName, UserID: key.UserID, ID: key.SessionID,
				Events: append([]event.Event(nil), page...),
			}
		}
		return newSession(events), newSession(events[2:]), newSession(events[:2])
	}
	wantIDs := []string{"page-event-0", "page-event-1", "page-event-2", "page-event-3"}
	full, recent, older := newPages(wantIDs)
	if err := validateEventPages(full, recent, older, key); err != nil {
		t.Fatalf("validateEventPages() error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*session.Session, *session.Session, *session.Session)
		want   string
	}{
		{
			name: "wrong full app",
			mutate: func(full, _, _ *session.Session) {
				full.AppName = "other-app"
			},
			want: "other-app",
		},
		{
			name: "wrong recent user",
			mutate: func(_, recent, _ *session.Session) {
				recent.UserID = "other-user"
			},
			want: "other-user",
		},
		{
			name: "wrong older session",
			mutate: func(_, _, older *session.Session) {
				older.ID = "other-session"
			},
			want: "other-session",
		},
		{
			name:   "consistently corrupted event ids",
			mutate: func(full, recent, older *session.Session) {},
			want:   "full event ids",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids := wantIDs
			if test.name == "consistently corrupted event ids" {
				ids = []string{"bad-0", "bad-1", "bad-2", "bad-3"}
			}
			full, recent, older := newPages(ids)
			test.mutate(full, recent, older)
			err := validateEventPages(full, recent, older, key)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateEventPages() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTTLProbeRejectsDroppedCreate(t *testing.T) {
	backend := replaytest.Backend{Name: "dropping", New: func(
		context.Context,
		string,
	) (replaytest.Fixture, error) {
		service := &droppingCreateSessionService{Service: sessioninmemory.NewSessionService()}
		return newReplayFixture(replayFixtureConfig{
			name: "dropping", sessionService: service,
			memoryService: memoryinmemory.NewMemoryService(), summarizer: &replaySummarizer{},
		}), nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := runTTLExpiryProbe(ctx, backend)
	if err != nil {
		t.Fatalf("runTTLExpiryProbe() error = %v", err)
	}
	if result.Status != replaytest.ResultFail || !strings.Contains(result.Explanation, "not found") {
		t.Fatalf("TTL probe = %#v", result)
	}
}

type corruptingListSessionService struct {
	session.Service
	corrupt     func([]*session.Session) []*session.Session
	corruptPage func([]*session.Session) []*session.Session
}

func (service *corruptingListSessionService) ListSessions(
	ctx context.Context,
	key session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	sessions, err := service.Service.ListSessions(ctx, key, options...)
	if err != nil {
		return sessions, err
	}
	if len(options) > 0 {
		if service.corruptPage != nil {
			return service.corruptPage(sessions), nil
		}
		return sessions, nil
	}
	if service.corrupt != nil {
		return service.corrupt(sessions), nil
	}
	return sessions, nil
}

type droppingCreateSessionService struct {
	session.Service
}

func (service *droppingCreateSessionService) CreateSession(
	_ context.Context,
	key session.Key,
	_ session.StateMap,
	_ ...session.Option,
) (*session.Session, error) {
	return &session.Session{
		AppName: key.AppName, UserID: key.UserID, ID: key.SessionID,
	}, nil
}

func assertPaginationProbeReport(
	t *testing.T,
	backend string,
	report replaytest.Report,
) {
	t.Helper()
	if report.HasUnexpectedDifferences() || report.HasInconclusiveResults() {
		t.Fatalf("pagination report has failure: %#v", report)
	}
	if len(report.Probes) != expectedPaginationProbeCount {
		t.Fatalf(
			"probe count = %d, want %d", len(report.Probes), expectedPaginationProbeCount,
		)
	}
	for _, probe := range report.Probes {
		if probe.Backend != backend {
			t.Fatalf("probe backend = %q, want %q", probe.Backend, backend)
		}
		if probe.Capability == replaytest.CapabilitySessionPaging &&
			probe.Status != replaytest.ResultPass {
			t.Fatalf("session paging probe = %#v", probe)
		}
		if probe.Capability == replaytest.CapabilityEventPaging &&
			(probe.Status != replaytest.ResultUnsupported || !probe.AllowedDiff) {
			t.Fatalf("event paging probe = %#v", probe)
		}
	}
}
