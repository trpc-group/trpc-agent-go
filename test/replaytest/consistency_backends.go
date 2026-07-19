//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	sessionsummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Ensure DeterministicSummarizer implements the expected interface.
var _ sessionsummary.SessionSummarizer = (*DeterministicSummarizer)(nil)

// ReplayBackend bundles session and memory services for one backend.
type ReplayBackend struct {
	Name           string
	SessionService session.Service
	TrackService   session.TrackService
	MemoryService  memory.Service
	Summarizer     *DeterministicSummarizer
}

// DeterministicSummarizer implements session.SessionSummarizer without
// calling any LLM API, keeping the framework dependency-free.
type DeterministicSummarizer struct {
	text string
}

func (s *DeterministicSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (s *DeterministicSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	if s.text == "" {
		return "replay summary", nil
	}
	return s.text, nil
}

func (s *DeterministicSummarizer) SetPrompt(string)     {}
func (s *DeterministicSummarizer) SetModel(model.Model) {}
func (s *DeterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"deterministic": true}
}

// SetText sets the text that the next Summarize call will return.
func (s *DeterministicSummarizer) SetText(text string) { s.text = text }

// NewReplayBackends returns the lightweight backend pair (InMemory + SQLite).
func NewReplayBackends(t testing.TB) []*ReplayBackend {
	inMemSum := &DeterministicSummarizer{}
	inMemSess := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(inMemSum),
	)
	inMemMem := meminmemory.NewMemoryService(
		meminmemory.WithMinSearchScore(0),
		meminmemory.WithMaxResults(0),
	)

	sqliteSum := &DeterministicSummarizer{}
	sqliteSess, err := sesssqlite.NewService(
		openTempSQLiteDB(t, "replay-session"),
		sesssqlite.WithSummarizer(sqliteSum),
	)
	if err != nil {
		t.Fatalf("create sqlite session service: %v", err)
	}
	sqliteMem, err := memsqlite.NewService(
		openTempSQLiteDB(t, "replay-memory"),
		memsqlite.WithMinSearchScore(0),
		memsqlite.WithMaxResults(0),
	)
	if err != nil {
		t.Fatalf("create sqlite memory service: %v", err)
	}

	backends := []*ReplayBackend{
		{Name: "in_memory", SessionService: inMemSess, TrackService: inMemSess,
			MemoryService: inMemMem, Summarizer: inMemSum},
		{Name: "sqlite", SessionService: sqliteSess, TrackService: sqliteSess,
			MemoryService: sqliteMem, Summarizer: sqliteSum},
	}
	t.Cleanup(func() { closeBackends(t, backends) })
	return backends
}

func openTempSQLiteDB(t testing.TB, name string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open sqlite db %s: %v", name, err)
	}
	return db
}

func closeBackends(t testing.TB, backends []*ReplayBackend) {
	t.Helper()
	for _, b := range backends {
		if b.MemoryService != nil {
			if err := b.MemoryService.Close(); err != nil {
				t.Logf("close memory service %s: %v", b.Name, err)
			}
		}
	}
	for _, b := range backends {
		if b.SessionService != nil {
			if err := b.SessionService.Close(); err != nil {
				t.Logf("close session service %s: %v", b.Name, err)
			}
		}
	}
}
