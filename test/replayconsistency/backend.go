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
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alicebob/miniredis/v2"
	// Registers the sqlite3 driver used by the SQLite backends.
	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"

	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Capabilities declares the optional features a backend implements.
//
// A feature a backend does not implement is recorded as unsupported in the
// report rather than compared and reported as a difference. Without this
// distinction a backend that simply lacks tracks would look identical to a
// backend that loses them.
type Capabilities struct {
	// Tracks reports whether the session service implements
	// session.TrackService.
	Tracks bool
	// Summary reports whether the session service persists summaries.
	Summary bool
	// Memory reports whether a memory service is paired with this backend.
	Memory bool
}

// Services is the service pair a backend contributes to one replay run,
// together with the func that releases whatever the backend allocated.
type Services struct {
	Session session.Service
	Memory  memory.Service
	Close   func()
}

// Backend builds the services one replay target runs against.
type Backend struct {
	// Name identifies the backend in reports and divergence records.
	Name string
	// Integration marks a backend that needs an external service. Integration
	// backends are skipped unless their environment variable is set.
	Integration bool
	// Open builds a fresh, empty service pair. The summarizer belongs to the
	// harness so that summary text stays deterministic; the backend only wires
	// it into the session service.
	Open func(sum summary.SessionSummarizer) (*Services, error)
}

// LightweightBackends returns the backends that need no external service.
//
// These are the backends CI runs. In-memory is the baseline every other
// backend is compared against, because it is the implementation applications
// develop against before switching to persistence.
func LightweightBackends() []Backend {
	return []Backend{
		{Name: "inmemory", Open: openInMemory},
		{Name: "sqlite", Open: openSQLite},
		{Name: "redis", Open: openRedis},
	}
}

// EnvRedisURL enables the integration Redis backend, for example
// redis://127.0.0.1:6379. It is read at run time, and the backend is skipped
// when the variable is unset.
const EnvRedisURL = "TRPC_REPLAY_REDIS_URL"

// IntegrationBackends returns the backends enabled by environment variables.
//
// The lightweight mode runs Redis through an in-process server, which is
// enough to compare behavior across backends but not enough to settle
// questions about Lua semantics or server-side ordering. Pointing this at a
// real server replays the same cases against it, which is how a divergence
// recorded as possibly emulation specific gets confirmed or dismissed.
//
// Adding a further backend is one entry here plus its module in test/go.mod.
// The cases and the comparator need no change, because they depend only on
// session.Service and memory.Service.
func IntegrationBackends() []Backend {
	var out []Backend
	if url := os.Getenv(EnvRedisURL); url != "" {
		out = append(out, Backend{
			Name:        "redis-server",
			Integration: true,
			Open: func(sum summary.SessionSummarizer) (*Services, error) {
				return openRedisURL(sum, url)
			},
		})
	}
	return out
}

func openInMemory(sum summary.SessionSummarizer) (*Services, error) {
	sessSvc := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(sum),
		sessioninmemory.WithAsyncSummaryNum(0),
	)
	memSvc := memoryinmemory.NewMemoryService()
	return &Services{
		Session: sessSvc,
		Memory:  memSvc,
		Close: func() {
			_ = sessSvc.Close()
			_ = memSvc.Close()
		},
	}, nil
}

func openSQLite(sum summary.SessionSummarizer) (*Services, error) {
	dir, err := os.MkdirTemp("", "replayconsistency-sqlite-")
	if err != nil {
		return nil, fmt.Errorf("create sqlite temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	sessDB, err := openSQLiteDB(filepath.Join(dir, "session.db"))
	if err != nil {
		cleanup()
		return nil, err
	}
	memDB, err := openSQLiteDB(filepath.Join(dir, "memory.db"))
	if err != nil {
		_ = sessDB.Close()
		cleanup()
		return nil, err
	}

	sessSvc, err := sessionsqlite.NewService(
		sessDB,
		sessionsqlite.WithSummarizer(sum),
		sessionsqlite.WithAsyncSummaryNum(0),
	)
	if err != nil {
		_ = sessDB.Close()
		_ = memDB.Close()
		cleanup()
		return nil, fmt.Errorf("create sqlite session service: %w", err)
	}
	memSvc, err := memorysqlite.NewService(memDB)
	if err != nil {
		_ = sessSvc.Close()
		_ = memDB.Close()
		cleanup()
		return nil, fmt.Errorf("create sqlite memory service: %w", err)
	}
	return &Services{
		Session: sessSvc,
		Memory:  memSvc,
		Close: func() {
			_ = sessSvc.Close()
			_ = memSvc.Close()
			cleanup()
		},
	}, nil
}

// openSQLiteDB opens a single-connection SQLite database.
//
// The pool is capped at one connection because SQLite serializes writers, and
// a larger pool turns concurrent appends into intermittent "database is
// locked" errors that would be reported as backend divergence.
func openSQLiteDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	return db, nil
}

func openRedis(sum summary.SessionSummarizer) (*Services, error) {
	mr, err := miniredis.Run()
	if err != nil {
		return nil, fmt.Errorf("start miniredis: %w", err)
	}
	svcs, err := openRedisURL(sum, "redis://"+mr.Addr())
	if err != nil {
		mr.Close()
		return nil, err
	}
	inner := svcs.Close
	svcs.Close = func() {
		inner()
		mr.Close()
	}
	return svcs, nil
}

// openRedisURL builds the Redis service pair against an already-running
// server, whether that is the in-process one or a real deployment.
//
// Each run uses a fresh key prefix so that replaying against a shared server
// cannot see another run's data, which would otherwise look like a backend
// inventing records.
func openRedisURL(sum summary.SessionSummarizer, url string) (*Services, error) {
	prefix := fmt.Sprintf("replayconsistency:%d:", nextRunID())

	sessSvc, err := sessionredis.NewService(
		sessionredis.WithKeyPrefix(prefix),
		sessionredis.WithRedisClientURL(url),
		sessionredis.WithSummarizer(sum),
		sessionredis.WithAsyncSummaryNum(0),
	)
	if err != nil {
		return nil, fmt.Errorf("create redis session service: %w", err)
	}
	memSvc, err := memoryredis.NewService(
		memoryredis.WithRedisClientURL(url),
		memoryredis.WithKeyPrefix(prefix),
	)
	if err != nil {
		_ = sessSvc.Close()
		return nil, fmt.Errorf("create redis memory service: %w", err)
	}
	return &Services{
		Session: sessSvc,
		Memory:  memSvc,
		Close: func() {
			_ = sessSvc.Close()
			_ = memSvc.Close()
		},
	}, nil
}

// runCounter distinguishes the key namespaces of concurrent runs sharing one
// Redis server.
var runCounter atomic.Uint64

func nextRunID() uint64 { return runCounter.Add(1) }

// target is the live service pair a scenario is replayed against.
type target struct {
	name       string
	session    session.Service
	memory     memory.Service
	summarizer *scriptedSummarizer
	caps       Capabilities
	// base anchors every scripted timestamp. It is supplied by the run rather
	// than computed per backend so that all backends receive identical
	// absolute timestamps, not merely identical offsets.
	base time.Time
}

// getSession reloads the session from the backend.
//
// The value is never cached between ops: an in-process copy would let a
// backend that fails to persist an event still look correct, which is exactly
// the bug class this harness exists to find.
func (t *target) getSession(ctx context.Context, ref SessionRef) (*session.Session, error) {
	sess, err := t.session.GetSession(ctx, ref.Key())
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", ref, err)
	}
	if sess == nil {
		return nil, fmt.Errorf("get session %s: not found", ref)
	}
	return sess, nil
}

// memoryReadLimit bounds memory read-back.
//
// A positive limit is passed explicitly rather than relying on zero meaning
// "unlimited", because that convention is exactly the kind of detail backends
// disagree about, and resolution must not depend on it.
const memoryReadLimit = 1000

// resolveMemoryID finds the identifier of the memory whose content matches.
//
// Memory identifiers are content-derived hashes computed inside the memory
// package, so a script cannot name one ahead of time.
func (t *target) resolveMemoryID(ctx context.Context, ref SessionRef, content string) (string, error) {
	entries, err := t.memory.ReadMemories(ctx, ref.MemoryUserKey(), memoryReadLimit)
	if err != nil {
		return "", fmt.Errorf("read memories for %s: %w", ref, err)
	}
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		if entry.Memory.Memory == content {
			return entry.ID, nil
		}
	}
	return "", fmt.Errorf("no memory with content %q for %s", content, ref)
}

// scriptedSummarizer returns the text the current CreateSummary op declares.
//
// It satisfies summary.SessionSummarizer without a model, so summaries are
// generated with no API key and identical text reaches every backend. The
// stored UpdatedAt is derived by the framework from event boundaries rather
// than the wall clock, so summaries stay comparable across backends.
type scriptedSummarizer struct {
	mu   sync.Mutex
	spec SummarySpec
	set  bool
}

// setSpec records the summary to return while one CreateSummary op is in
// flight, and returns the func that retires it.
//
// The spec is scoped to the op rather than latched for the rest of the run.
// A summarizer that keeps answering yes after its op has finished would report
// that summarization is due for every later event, which is not what the script
// asked for and would only show up once a case used a non-forced summary.
func (s *scriptedSummarizer) setSpec(spec SummarySpec) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spec = spec
	s.set = true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.spec = SummarySpec{}
		s.set = false
	}
}

// ShouldSummarize reports whether the scenario asked for a summary.
func (s *scriptedSummarizer) ShouldSummarize(sess *session.Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

// Summarize returns the scripted summary text.
func (s *scriptedSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set {
		return "", nil
	}
	return s.spec.Text, nil
}

// SetPrompt implements summary.SessionSummarizer and is intentionally inert:
// the text is scripted, so there is no prompt to apply.
func (s *scriptedSummarizer) SetPrompt(string) {}

// SetModel implements summary.SessionSummarizer and is intentionally inert:
// the summarizer never calls a model.
func (s *scriptedSummarizer) SetModel(model.Model) {}

// Metadata implements summary.SessionSummarizer.
func (s *scriptedSummarizer) Metadata() map[string]any {
	return map[string]any{"summarizer": "scripted"}
}
