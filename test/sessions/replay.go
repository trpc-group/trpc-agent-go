//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	flowprocessor "trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// ActionType identifies one operation in a replay fixture.
type ActionType string

const (
	ActionMetadata       ActionType = "metadata"
	ActionAllowDiff      ActionType = "allow_diff"
	ActionCreateSession  ActionType = "create_session"
	ActionAppendEvent    ActionType = "append_event"
	ActionAppendTrack    ActionType = "append_track"
	ActionUpdateState    ActionType = "update_state"
	ActionAddMemory      ActionType = "add_memory"
	ActionUpdateMemory   ActionType = "update_memory"
	ActionDeleteMemory   ActionType = "delete_memory"
	ActionCreateSummary  ActionType = "create_summary"
	ActionEnqueueSummary ActionType = "enqueue_summary"
	ActionAssertSession  ActionType = "assert_session"
	ActionCheckpoint     ActionType = "checkpoint"
	replaySchemaVersion             = 1
)

// ReplayCase is a backend-independent sequence of business operations.
type ReplayCase struct {
	Version     int
	ID          string
	Description string
	AllowedDiff []AllowedDiffRule
	Actions     []ReplayAction
}

// ReplayAction is one JSONL record.
type ReplayAction struct {
	Action      ActionType                 `json:"action"`
	Version     int                        `json:"version,omitempty"`
	ID          string                     `json:"id,omitempty"`
	Description string                     `json:"description,omitempty"`
	SessionID   string                     `json:"session_id,omitempty"`
	State       map[string]json.RawMessage `json:"state,omitempty"`
	Event       *EventInput                `json:"event,omitempty"`
	Track       *TrackInput                `json:"track,omitempty"`
	Memory      *MemoryInput               `json:"memory,omitempty"`
	Summary     *SummaryInput              `json:"summary,omitempty"`
	Expected    *SessionExpectation        `json:"expected,omitempty"`
	Failure     *FailureInput              `json:"failure,omitempty"`
	AllowedDiff *AllowedDiffRule           `json:"allowed_diff,omitempty"`
	Checkpoint  string                     `json:"checkpoint,omitempty"`
}

// TrackInput describes one observable tool or subtask trajectory event.
type TrackInput struct {
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp string          `json:"timestamp"`
}

// EventInput describes a stable event without exposing backend details.
type EventInput struct {
	ID           string                     `json:"id"`
	InvocationID string                     `json:"invocation_id,omitempty"`
	Author       string                     `json:"author,omitempty"`
	Role         model.Role                 `json:"role"`
	Content      string                     `json:"content,omitempty"`
	Timestamp    string                     `json:"timestamp"`
	FilterKey    string                     `json:"filter_key,omitempty"`
	ToolCalls    []ToolCallInput            `json:"tool_calls,omitempty"`
	ToolID       string                     `json:"tool_id,omitempty"`
	ToolName     string                     `json:"tool_name,omitempty"`
	StateDelta   map[string]json.RawMessage `json:"state_delta,omitempty"`
}

// ToolCallInput is the fixture representation of a model tool call.
type ToolCallInput struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// MemoryInput describes a memory operation. Ref is stable within one case.
type MemoryInput struct {
	Ref          string      `json:"ref"`
	Content      string      `json:"content,omitempty"`
	Topics       []string    `json:"topics,omitempty"`
	Kind         memory.Kind `json:"kind,omitempty"`
	EventTime    string      `json:"event_time,omitempty"`
	Participants []string    `json:"participants,omitempty"`
	Location     string      `json:"location,omitempty"`
}

// SummaryInput selects the summary scope.
type SummaryInput struct {
	FilterKey string `json:"filter_key,omitempty"`
	Force     bool   `json:"force,omitempty"`
	Await     bool   `json:"await,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// SessionExpectation describes backend-local invariants checked by a fixture.
// These assertions complement cross-backend comparison: they also catch a bug
// that happens in exactly the same way on every backend.
type SessionExpectation struct {
	EventIDs       []string                           `json:"event_ids,omitempty"`
	UniqueEventIDs bool                               `json:"unique_event_ids,omitempty"`
	State          map[string]json.RawMessage         `json:"state,omitempty"`
	SummaryCount   *int                               `json:"summary_count,omitempty"`
	Summary        *SummaryExpectation                `json:"summary,omitempty"`
	Context        *ContextExpectation                `json:"context,omitempty"`
	Tracks         map[string][]TrackEventExpectation `json:"tracks,omitempty"`
}

// TrackEventExpectation checks the ordered payload and optional timestamp of
// one event in a named track.
type TrackEventExpectation struct {
	Payload   json.RawMessage `json:"payload"`
	Timestamp string          `json:"timestamp,omitempty"`
}

// SummaryExpectation checks summary identity, content, boundary, and the
// unsummarized event tail needed to rebuild a model context.
type SummaryExpectation struct {
	FilterKey             string   `json:"filter_key,omitempty"`
	Version               int      `json:"version,omitempty"`
	Revision              int      `json:"revision,omitempty"`
	UpdatedAtNonZero      bool     `json:"updated_at_non_zero,omitempty"`
	UpdatedAtEqualsCutoff bool     `json:"updated_at_equals_cutoff,omitempty"`
	LastEventID           string   `json:"last_event_id,omitempty"`
	Contains              []string `json:"contains,omitempty"`
	Excludes              []string `json:"excludes,omitempty"`
	TailEventIDs          []string `json:"tail_event_ids,omitempty"`
}

// ContextExpectation validates the actual model request projection produced
// by ContentRequestProcessor from the restored session.
type ContextExpectation struct {
	FilterKey string                      `json:"filter_key,omitempty"`
	Messages  []ContextMessageExpectation `json:"messages"`
}

// ContextMessageExpectation checks one projected model message.
type ContextMessageExpectation struct {
	Role     model.Role `json:"role"`
	Content  string     `json:"content,omitempty"`
	Contains []string   `json:"contains,omitempty"`
}

// FailureInput injects a failure around the action that contains it.
type FailureInput struct {
	FailBefore bool `json:"fail_before,omitempty"`
	FailAfter  bool `json:"fail_after,omitempty"`
	Duplicate  bool `json:"duplicate,omitempty"`
	Retry      bool `json:"retry,omitempty"`
}

// AllowedDiffRule documents one intentional backend difference.
type AllowedDiffRule struct {
	Path    string `json:"path"`
	Backend string `json:"backend,omitempty"`
	Reason  string `json:"reason"`
}

// LoadReplayCases loads all JSONL fixtures in lexical filename order.
func LoadReplayCases(dir string) ([]ReplayCase, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("glob replay cases: %w", err)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no replay cases found in %s", dir)
	}
	cases := make([]ReplayCase, 0, len(paths))
	for _, path := range paths {
		tc, err := LoadReplayCase(path)
		if err != nil {
			return nil, err
		}
		cases = append(cases, tc)
	}
	return cases, nil
}

// LoadReplayCase parses a single JSONL fixture.
func LoadReplayCase(path string) (ReplayCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReplayCase{}, fmt.Errorf("open replay case %s: %w", path, err)
	}
	defer f.Close()
	tc := ReplayCase{
		Version: replaySchemaVersion,
		ID:      strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var action ReplayAction
		if err := json.Unmarshal([]byte(raw), &action); err != nil {
			return ReplayCase{}, fmt.Errorf("%s:%d: decode action: %w", path, line, err)
		}
		switch action.Action {
		case ActionMetadata:
			if action.Version != 0 {
				tc.Version = action.Version
			}
			if action.ID != "" {
				tc.ID = action.ID
			}
			tc.Description = action.Description
		case ActionAllowDiff:
			if action.AllowedDiff == nil {
				return ReplayCase{}, fmt.Errorf("%s:%d: allowed_diff is required", path, line)
			}
			tc.AllowedDiff = append(tc.AllowedDiff, *action.AllowedDiff)
		default:
			if err := action.Validate(); err != nil {
				return ReplayCase{}, fmt.Errorf("%s:%d: %w", path, line, err)
			}
			tc.Actions = append(tc.Actions, action)
		}
	}
	if err := scanner.Err(); err != nil {
		return ReplayCase{}, fmt.Errorf("scan replay case %s: %w", path, err)
	}
	if err := tc.Validate(); err != nil {
		return ReplayCase{}, fmt.Errorf("%s: %w", path, err)
	}
	return tc, nil
}

// Validate validates a complete case.
func (c ReplayCase) Validate() error {
	if c.Version != replaySchemaVersion {
		return fmt.Errorf("unsupported replay schema version %d", c.Version)
	}
	if c.ID == "" || len(c.Actions) == 0 {
		return errors.New("case id and at least one action are required")
	}
	for _, rule := range c.AllowedDiff {
		if rule.Path == "" || rule.Reason == "" {
			return errors.New("allowed diff requires path and reason")
		}
	}
	return nil
}

// Validate validates one executable action.
func (a ReplayAction) Validate() error {
	switch a.Action {
	case ActionCreateSession, ActionUpdateState, ActionAppendEvent, ActionAppendTrack,
		ActionCreateSummary, ActionEnqueueSummary, ActionAssertSession:
		if a.SessionID == "" {
			return fmt.Errorf("%s requires session_id", a.Action)
		}
	}
	switch a.Action {
	case ActionCreateSession, ActionCreateSummary, ActionEnqueueSummary:
		return nil
	case ActionAppendEvent:
		if a.Event == nil || a.Event.ID == "" || a.Event.Timestamp == "" {
			return errors.New("append_event requires event.id and event.timestamp")
		}
		if !a.Event.Role.IsValid() {
			return fmt.Errorf("invalid event role %q", a.Event.Role)
		}
	case ActionAppendTrack:
		if a.Track == nil || strings.TrimSpace(a.Track.Name) == "" ||
			a.Track.Timestamp == "" || !json.Valid(a.Track.Payload) {
			return errors.New("append_track requires track.name, valid payload, and timestamp")
		}
	case ActionUpdateState:
		if len(a.State) == 0 {
			return errors.New("update_state requires state")
		}
	case ActionAddMemory, ActionUpdateMemory, ActionDeleteMemory:
		if a.Memory == nil || a.Memory.Ref == "" {
			return fmt.Errorf("%s requires memory.ref", a.Action)
		}
	case ActionCheckpoint:
		if a.Checkpoint == "" {
			return errors.New("checkpoint requires a name")
		}
	case ActionAssertSession:
		if a.Expected == nil {
			return errors.New("assert_session requires expected")
		}
		if a.Expected.Context != nil {
			for i, message := range a.Expected.Context.Messages {
				if !message.Role.IsValid() {
					return fmt.Errorf(
						"assert_session context message %d has invalid role %q",
						i, message.Role,
					)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported replay action %q", a.Action)
	}
	return nil
}

// Backend exposes the two storage services that form a replay target.
type Backend interface {
	Name() string
	SessionService() session.Service
	MemoryService() memory.Service
	Close() error
}

// BackendFactory creates an isolated backend for one replay case.
type BackendFactory interface {
	Name() string
	Create(context.Context, BackendConfig) (Backend, error)
}

// BackendConfig contains per-case backend configuration.
type BackendConfig struct {
	CaseID, AppName, UserID, TempDir, RedisURL, KeyPrefix string
}

type serviceBackend struct {
	name           string
	sessionService session.Service
	memoryService  memory.Service
	cleanup        func() error
}

func (b *serviceBackend) Name() string                    { return b.name }
func (b *serviceBackend) SessionService() session.Service { return b.sessionService }
func (b *serviceBackend) MemoryService() memory.Service   { return b.memoryService }
func (b *serviceBackend) Close() error {
	if b.cleanup == nil {
		return nil
	}
	return b.cleanup()
}

// InMemoryBackendFactory creates the reference backend.
type InMemoryBackendFactory struct{}

func (InMemoryBackendFactory) Name() string { return "inmemory" }
func (InMemoryBackendFactory) Create(context.Context, BackendConfig) (Backend, error) {
	ss := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(deterministicSummarizer{}),
		sessioninmemory.WithSummaryFilterAllowlist("agent/tool"),
	)
	ms := memoryinmemory.NewMemoryService()
	return &serviceBackend{
		name: "inmemory", sessionService: ss, memoryService: ms,
		cleanup: func() error { return errors.Join(ss.Close(), ms.Close()) },
	}, nil
}

// SQLiteBackendFactory creates a real persistent backend in temporary files.
type SQLiteBackendFactory struct{}

func (SQLiteBackendFactory) Name() string { return "sqlite" }
func (SQLiteBackendFactory) Create(_ context.Context, cfg BackendConfig) (Backend, error) {
	if cfg.TempDir == "" {
		return nil, errors.New("sqlite backend requires temp dir")
	}
	sdb, err := sql.Open("sqlite3", filepath.Join(cfg.TempDir, cfg.CaseID+"-session.db"))
	if err != nil {
		return nil, fmt.Errorf("open session sqlite: %w", err)
	}
	ss, err := sessionsqlite.NewService(sdb,
		sessionsqlite.WithEnableAsyncPersist(false),
		sessionsqlite.WithSummarizer(deterministicSummarizer{}),
		sessionsqlite.WithSummaryFilterAllowlist("agent/tool"))
	if err != nil {
		sdb.Close()
		return nil, fmt.Errorf("create session sqlite service: %w", err)
	}
	mdb, err := sql.Open("sqlite3", filepath.Join(cfg.TempDir, cfg.CaseID+"-memory.db"))
	if err != nil {
		ss.Close()
		return nil, fmt.Errorf("open memory sqlite: %w", err)
	}
	ms, err := memorysqlite.NewService(mdb)
	if err != nil {
		mdb.Close()
		ss.Close()
		return nil, fmt.Errorf("create memory sqlite service: %w", err)
	}
	return &serviceBackend{
		name: "sqlite", sessionService: ss, memoryService: ms,
		cleanup: func() error { return errors.Join(ss.Close(), ms.Close()) },
	}, nil
}

// RedisBackendFactory creates miniredis by default and accepts a real Redis URL.
type RedisBackendFactory struct{}

func (RedisBackendFactory) Name() string { return "redis" }
func (RedisBackendFactory) Create(_ context.Context, cfg BackendConfig) (Backend, error) {
	redisURL := cfg.RedisURL
	var mini *miniredis.Miniredis
	var err error
	if redisURL == "" {
		mini, err = miniredis.Run()
		if err != nil {
			return nil, fmt.Errorf("start miniredis: %w", err)
		}
		redisURL = "redis://" + mini.Addr()
	}
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "replay:" + cfg.CaseID
	}
	ss, err := sessionredis.NewService(
		sessionredis.WithRedisClientURL(redisURL),
		sessionredis.WithKeyPrefix(prefix),
		sessionredis.WithCompatMode(sessionredis.CompatModeNone),
		sessionredis.WithEnableAsyncPersist(false),
		sessionredis.WithSummarizer(deterministicSummarizer{}),
		sessionredis.WithSummaryFilterAllowlist("agent/tool"))
	if err != nil {
		if mini != nil {
			mini.Close()
		}
		return nil, fmt.Errorf("create redis session service: %w", err)
	}
	ms, err := memoryredis.NewService(
		memoryredis.WithRedisClientURL(redisURL),
		memoryredis.WithKeyPrefix(prefix))
	if err != nil {
		ss.Close()
		if mini != nil {
			mini.Close()
		}
		return nil, fmt.Errorf("create redis memory service: %w", err)
	}
	return &serviceBackend{
		name: "redis", sessionService: ss, memoryService: ms,
		cleanup: func() error {
			err := errors.Join(ss.Close(), ms.Close())
			if mini != nil {
				mini.Close()
			}
			return err
		},
	}, nil
}

// DefaultBackendFactories returns the lightweight backend matrix.
func DefaultBackendFactories() []BackendFactory {
	return []BackendFactory{
		InMemoryBackendFactory{}, SQLiteBackendFactory{}, RedisBackendFactory{},
	}
}

// ReplayResult captures one case executed on one backend.
type ReplayResult struct {
	CaseID        string         `json:"case_id"`
	Backend       string         `json:"backend"`
	ActionResults []ActionResult `json:"actions"`
	Checkpoints   []Snapshot     `json:"checkpoints,omitempty"`
	FinalSnapshot Snapshot       `json:"final_snapshot"`
	DurationMS    int64          `json:"duration_ms"`
	Error         string         `json:"error,omitempty"`
}

// ActionResult records one action execution.
type ActionResult struct {
	Index      int        `json:"index"`
	Action     ActionType `json:"action"`
	Success    bool       `json:"success"`
	DurationMS int64      `json:"duration_ms"`
	Error      string     `json:"error,omitempty"`
}

type replayState struct {
	appName     string
	userID      string
	sessions    map[string]*session.Session
	memoryIDs   map[string]string
	checkpoints []Snapshot
}

// Replay executes a case using public session and memory APIs.
func Replay(ctx context.Context, backend Backend, tc ReplayCase, appName, userID string) (*ReplayResult, error) {
	started := time.Now()
	state := &replayState{
		appName: appName, userID: userID,
		sessions:  make(map[string]*session.Session),
		memoryIDs: make(map[string]string),
	}
	result := &ReplayResult{CaseID: tc.ID, Backend: backend.Name()}
	for i, action := range tc.Actions {
		actionStarted := time.Now()
		err := executeReplayAction(ctx, backend, state, action)
		ar := ActionResult{
			Index: i, Action: action.Action, Success: err == nil,
			DurationMS: time.Since(actionStarted).Milliseconds(),
		}
		if err != nil {
			ar.Error, result.Error = err.Error(), err.Error()
			result.ActionResults = append(result.ActionResults, ar)
			result.DurationMS = time.Since(started).Milliseconds()
			return result, fmt.Errorf("case %s backend %s action %d (%s): %w",
				tc.ID, backend.Name(), i, action.Action, err)
		}
		result.ActionResults = append(result.ActionResults, ar)
	}
	final, err := CollectSnapshot(ctx, backend, state, "final")
	if err != nil {
		return result, fmt.Errorf("collect final snapshot: %w", err)
	}
	final.CaseID = tc.ID
	for i := range state.checkpoints {
		state.checkpoints[i].CaseID = tc.ID
	}
	result.Checkpoints = state.checkpoints
	result.FinalSnapshot = final
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func executeReplayAction(ctx context.Context, backend Backend, state *replayState, action ReplayAction) error {
	op := func() error {
		switch action.Action {
		case ActionCreateSession:
			key := session.Key{AppName: state.appName, UserID: state.userID, SessionID: action.SessionID}
			sess, err := backend.SessionService().CreateSession(ctx, key, rawState(action.State))
			if err == nil {
				state.sessions[action.SessionID] = sess
			}
			return err
		case ActionAppendEvent:
			sess, err := getReplaySession(ctx, backend, state, action.SessionID)
			if err != nil {
				return err
			}
			evt, err := buildEvent(action.Event)
			if err != nil {
				return err
			}
			if err := backend.SessionService().AppendEvent(ctx, sess, evt); err != nil {
				return err
			}
			state.sessions[action.SessionID] = sess
			return nil
		case ActionAppendTrack:
			sess, err := getReplaySession(ctx, backend, state, action.SessionID)
			if err != nil {
				return err
			}
			trackService, ok := backend.SessionService().(session.TrackService)
			if !ok {
				return fmt.Errorf("backend %s does not support tracks", backend.Name())
			}
			trackEvent, err := buildTrackEvent(action.Track)
			if err != nil {
				return err
			}
			if err := trackService.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
				return err
			}
			state.sessions[action.SessionID] = sess
			return nil
		case ActionUpdateState:
			key := session.Key{AppName: state.appName, UserID: state.userID, SessionID: action.SessionID}
			return backend.SessionService().UpdateSessionState(ctx, key, rawState(action.State))
		case ActionAddMemory:
			userKey := memory.UserKey{AppName: state.appName, UserID: state.userID}
			opts, err := addMemoryOptions(action.Memory)
			if err != nil {
				return err
			}
			if err := backend.MemoryService().AddMemory(ctx, userKey,
				action.Memory.Content, action.Memory.Topics, opts...); err != nil {
				return err
			}
			entries, err := backend.MemoryService().ReadMemories(ctx, userKey, 0)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.Memory != nil && entry.Memory.Memory == action.Memory.Content {
					state.memoryIDs[action.Memory.Ref] = entry.ID
					return nil
				}
			}
			return fmt.Errorf("added memory %q was not found", action.Memory.Ref)
		case ActionUpdateMemory:
			memoryID, ok := state.memoryIDs[action.Memory.Ref]
			if !ok {
				return fmt.Errorf("unknown memory ref %q", action.Memory.Ref)
			}
			metadata, err := memoryMetadata(action.Memory)
			if err != nil {
				return err
			}
			updateResult := &memory.UpdateResult{}
			opts := []memory.UpdateOption{memory.WithUpdateResult(updateResult)}
			if metadata != nil {
				opts = append(opts, memory.WithUpdateMetadata(metadata))
			}
			key := memory.Key{AppName: state.appName, UserID: state.userID, MemoryID: memoryID}
			if err := backend.MemoryService().UpdateMemory(ctx, key,
				action.Memory.Content, action.Memory.Topics, opts...); err != nil {
				return err
			}
			if updateResult.MemoryID != "" {
				state.memoryIDs[action.Memory.Ref] = updateResult.MemoryID
			}
			return nil
		case ActionDeleteMemory:
			memoryID, ok := state.memoryIDs[action.Memory.Ref]
			if !ok {
				return fmt.Errorf("unknown memory ref %q", action.Memory.Ref)
			}
			key := memory.Key{AppName: state.appName, UserID: state.userID, MemoryID: memoryID}
			if err := backend.MemoryService().DeleteMemory(ctx, key); err != nil {
				return err
			}
			delete(state.memoryIDs, action.Memory.Ref)
			return nil
		case ActionCreateSummary:
			sess, err := getReplaySession(ctx, backend, state, action.SessionID)
			if err != nil {
				return err
			}
			filterKey := ""
			if action.Summary != nil {
				filterKey = action.Summary.FilterKey
			}
			return backend.SessionService().CreateSessionSummary(ctx, sess, filterKey, true)
		case ActionEnqueueSummary:
			sess, err := getReplaySession(ctx, backend, state, action.SessionID)
			if err != nil {
				return err
			}
			var input SummaryInput
			if action.Summary != nil {
				input = *action.Summary
			}
			if err := backend.SessionService().EnqueueSummaryJob(
				ctx, sess, input.FilterKey, input.Force,
			); err != nil {
				return err
			}
			if input.Await {
				return awaitReplaySummary(
					ctx, backend, state, action.SessionID,
					input.FilterKey, input.TimeoutMS,
				)
			}
			return nil
		case ActionAssertSession:
			sess, err := getReplaySession(ctx, backend, state, action.SessionID)
			if err != nil {
				return err
			}
			return assertReplaySession(ctx, backend, sess, action.Expected)
		case ActionCheckpoint:
			snapshot, err := CollectSnapshot(ctx, backend, state, action.Checkpoint)
			if err == nil {
				state.checkpoints = append(state.checkpoints, snapshot)
			}
			return err
		default:
			return fmt.Errorf("unsupported action %q", action.Action)
		}
	}
	var confirm func() (bool, error)
	switch action.Action {
	case ActionAppendEvent:
		confirm = func() (bool, error) {
			sess, err := getReplaySession(
				ctx, backend, state, action.SessionID,
			)
			if err != nil {
				return false, err
			}
			state.sessions[action.SessionID] = sess
			count := 0
			for _, evt := range sess.Events {
				if evt.ID == action.Event.ID {
					count++
				}
			}
			return count == 1, nil
		}
	case ActionCreateSummary:
		confirm = func() (bool, error) {
			sess, err := getReplaySession(
				ctx, backend, state, action.SessionID,
			)
			if err != nil {
				return false, err
			}
			filterKey := ""
			if action.Summary != nil {
				filterKey = action.Summary.FilterKey
			}
			sess.SummariesMu.RLock()
			summary := sess.Summaries[filterKey]
			sess.SummariesMu.RUnlock()
			return summary != nil, nil
		}
	}
	return executeWithFailure(op, action.Failure, confirm)
}

func executeWithFailure(
	operation func() error,
	failure *FailureInput,
	confirm func() (bool, error),
) error {
	if failure == nil {
		return operation()
	}
	if failure.FailBefore && !failure.Retry {
		return errors.New("injected failure before write")
	}
	if failure.Duplicate {
		if err := operation(); err != nil {
			return err
		}
		return operation()
	}
	if err := operation(); err != nil {
		return err
	}
	if failure.FailAfter {
		if !failure.Retry {
			return errors.New("injected failure after write")
		}
		if confirm != nil {
			persisted, err := confirm()
			if err != nil {
				return fmt.Errorf("confirm ambiguous write: %w", err)
			}
			if persisted {
				return nil
			}
		}
		return operation()
	}
	return nil
}

func awaitReplaySummary(
	ctx context.Context,
	backend Backend,
	state *replayState,
	sessionID, filterKey string,
	timeoutMS int,
) error {
	timeout := 2 * time.Second
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		sess, err := getReplaySession(ctx, backend, state, sessionID)
		if err != nil {
			return err
		}
		sess.SummariesMu.RLock()
		summary := sess.Summaries[filterKey]
		sess.SummariesMu.RUnlock()
		if summary != nil {
			state.sessions[sessionID] = sess
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf(
				"wait for summary session=%q filter=%q: timeout after %s",
				sessionID, filterKey, timeout,
			)
		case <-ticker.C:
		}
	}
}

func assertReplaySession(
	ctx context.Context,
	backend Backend,
	sess *session.Session,
	expected *SessionExpectation,
) error {
	if expected == nil {
		return errors.New("session expectation is nil")
	}
	sess.EventMu.RLock()
	events := append([]event.Event(nil), sess.Events...)
	sess.EventMu.RUnlock()
	eventIDs := make([]string, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, evt := range events {
		eventIDs = append(eventIDs, evt.ID)
		if expected.UniqueEventIDs {
			if _, ok := seen[evt.ID]; ok {
				return fmt.Errorf("duplicate event id %q", evt.ID)
			}
			seen[evt.ID] = struct{}{}
		}
	}
	if expected.EventIDs != nil && !equalStrings(eventIDs, expected.EventIDs) {
		return fmt.Errorf("event ids: got %v, want %v", eventIDs, expected.EventIDs)
	}
	actualState := sess.SnapshotState()
	for key, want := range expected.State {
		got, ok := actualState[key]
		if !ok {
			return fmt.Errorf("state %q is missing", key)
		}
		if string(canonicalJSON(got)) != string(canonicalJSON(want)) {
			return fmt.Errorf("state %q: got %s, want %s", key, got, want)
		}
	}
	for trackName, wantEvents := range expected.Tracks {
		got, err := sess.GetTrackEvents(session.Track(trackName))
		if err != nil {
			return fmt.Errorf("track %q: %w", trackName, err)
		}
		if len(got.Events) != len(wantEvents) {
			return fmt.Errorf("track %q event count: got %d, want %d", trackName, len(got.Events), len(wantEvents))
		}
		for i, want := range wantEvents {
			if i > 0 && got.Events[i].Timestamp.Before(got.Events[i-1].Timestamp) {
				return fmt.Errorf("track %q timestamps are out of order at event %d", trackName, i)
			}
			if string(canonicalJSON(got.Events[i].Payload)) != string(canonicalJSON(want.Payload)) {
				return fmt.Errorf("track %q event %d payload: got %s, want %s", trackName, i, got.Events[i].Payload, want.Payload)
			}
			if want.Timestamp != "" {
				wantTime, err := time.Parse(time.RFC3339Nano, want.Timestamp)
				if err != nil {
					return fmt.Errorf("track %q event %d timestamp: %w", trackName, i, err)
				}
				if !got.Events[i].Timestamp.Equal(wantTime) {
					return fmt.Errorf("track %q event %d timestamp: got %s, want %s", trackName, i, got.Events[i].Timestamp, wantTime)
				}
			}
		}
	}
	sess.SummariesMu.RLock()
	if expected.SummaryCount != nil && len(sess.Summaries) != *expected.SummaryCount {
		sess.SummariesMu.RUnlock()
		return fmt.Errorf(
			"summary count: got %d, want %d",
			len(sess.Summaries), *expected.SummaryCount,
		)
	}
	if expected.Summary == nil {
		sess.SummariesMu.RUnlock()
		return assertReplayContext(ctx, backend, sess, expected.Context)
	}
	wantSummary := expected.Summary
	summary := sess.Summaries[wantSummary.FilterKey].Clone()
	sess.SummariesMu.RUnlock()
	if summary == nil {
		return fmt.Errorf("summary filter %q is missing", wantSummary.FilterKey)
	}
	boundary := summary.CutoffBoundary()
	if wantSummary.Version > 0 &&
		(boundary == nil || boundary.Version != wantSummary.Version) {
		return fmt.Errorf("summary version: got %v, want %d", boundary, wantSummary.Version)
	}
	if wantSummary.Revision > 0 && summary.Revision != wantSummary.Revision {
		return fmt.Errorf(
			"summary revision: got %d, want %d",
			summary.Revision, wantSummary.Revision,
		)
	}
	if wantSummary.UpdatedAtNonZero && summary.UpdatedAt.IsZero() {
		return errors.New("summary updated_at is zero")
	}
	if wantSummary.UpdatedAtEqualsCutoff &&
		(boundary == nil || !summary.UpdatedAt.Equal(boundary.CutoffTime())) {
		return fmt.Errorf(
			"summary updated_at %s does not equal cutoff %v",
			summary.UpdatedAt, boundary,
		)
	}
	if wantSummary.LastEventID != "" &&
		(boundary == nil || boundary.LastEventID != wantSummary.LastEventID) {
		return fmt.Errorf(
			"summary last event id: got %v, want %q",
			boundary, wantSummary.LastEventID,
		)
	}
	for _, fragment := range wantSummary.Contains {
		if !strings.Contains(summary.Summary, fragment) {
			return fmt.Errorf("summary does not contain %q", fragment)
		}
	}
	for _, fragment := range wantSummary.Excludes {
		if strings.Contains(summary.Summary, fragment) {
			return fmt.Errorf("summary unexpectedly contains %q", fragment)
		}
	}
	if wantSummary.TailEventIDs != nil {
		tail, err := eventIDsAfterSummaryBoundary(events, boundary)
		if err != nil {
			return err
		}
		if !equalStrings(tail, wantSummary.TailEventIDs) {
			return fmt.Errorf("summary tail event ids: got %v, want %v", tail, wantSummary.TailEventIDs)
		}
	}
	return assertReplayContext(ctx, backend, sess, expected.Context)
}

func assertReplayContext(
	ctx context.Context,
	backend Backend,
	sess *session.Session,
	expected *ContextExpectation,
) error {
	if expected == nil {
		return nil
	}
	invocation := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationSessionService(backend.SessionService()),
		agent.WithInvocationEventFilterKey(expected.FilterKey),
	)
	invocation.AgentName = model.RoleAssistant.String()
	request := &model.Request{}
	processor := flowprocessor.NewContentRequestProcessor(
		flowprocessor.WithAddSessionSummary(true),
	)
	processor.ProcessRequest(ctx, invocation, request, nil)
	if len(request.Messages) != len(expected.Messages) {
		return fmt.Errorf(
			"projected context message count: got %d, want %d",
			len(request.Messages), len(expected.Messages),
		)
	}
	for i, want := range expected.Messages {
		got := request.Messages[i]
		if got.Role != want.Role {
			return fmt.Errorf(
				"projected context message %d role: got %q, want %q",
				i, got.Role, want.Role,
			)
		}
		if want.Content != "" && got.Content != want.Content {
			return fmt.Errorf(
				"projected context message %d content: got %q, want %q",
				i, got.Content, want.Content,
			)
		}
		for _, fragment := range want.Contains {
			if !strings.Contains(got.Content, fragment) {
				return fmt.Errorf(
					"projected context message %d does not contain %q",
					i, fragment,
				)
			}
		}
	}
	return nil
}

func eventIDsAfterSummaryBoundary(
	events []event.Event,
	boundary *session.SummaryBoundary,
) ([]string, error) {
	if boundary == nil {
		return nil, errors.New("summary boundary is missing")
	}
	if boundary.LastEventID != "" {
		for i := range events {
			if events[i].ID == boundary.LastEventID {
				ids := make([]string, 0, len(events)-i-1)
				for _, evt := range events[i+1:] {
					ids = append(ids, evt.ID)
				}
				return ids, nil
			}
		}
		return nil, fmt.Errorf(
			"summary boundary event %q is missing from session",
			boundary.LastEventID,
		)
	}
	ids := []string{}
	for _, evt := range events {
		if evt.Timestamp.After(boundary.CutoffTime()) {
			ids = append(ids, evt.ID)
		}
	}
	return ids, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func getReplaySession(ctx context.Context, backend Backend, state *replayState, sessionID string) (*session.Session, error) {
	key := session.Key{AppName: state.appName, UserID: state.userID, SessionID: sessionID}
	sess, err := backend.SessionService().GetSession(ctx, key)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	return sess, nil
}

func rawState(input map[string]json.RawMessage) session.StateMap {
	state := make(session.StateMap, len(input))
	for key, value := range input {
		state[key] = append([]byte(nil), value...)
	}
	return state
}

func buildEvent(input *EventInput) (*event.Event, error) {
	if input == nil {
		return nil, errors.New("event input is nil")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, input.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("parse event timestamp: %w", err)
	}
	message := model.Message{
		Role: input.Role, Content: input.Content,
		ToolID: input.ToolID, ToolName: input.ToolName,
	}
	for _, call := range input.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, model.ToolCall{
			Type: "function", ID: call.ID,
			Function: model.FunctionDefinitionParam{
				Name: call.Name, Arguments: append([]byte(nil), call.Arguments...),
			},
		})
	}
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion, Done: true,
		Choices: []model.Choice{{Message: message}},
	}
	author := input.Author
	if author == "" {
		author = input.Role.String()
	}
	invocationID := input.InvocationID
	if invocationID == "" {
		invocationID = "invocation-" + input.ID
	}
	evt := event.NewResponseEvent(invocationID, author, response)
	evt.ID, evt.Timestamp, evt.FilterKey = input.ID, timestamp.UTC(), input.FilterKey
	evt.StateDelta = rawState(input.StateDelta)
	return evt, nil
}

func buildTrackEvent(input *TrackInput) (*session.TrackEvent, error) {
	if input == nil {
		return nil, errors.New("track input is nil")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, input.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("parse track timestamp: %w", err)
	}
	return &session.TrackEvent{
		Track:     session.Track(input.Name),
		Payload:   append(json.RawMessage(nil), input.Payload...),
		Timestamp: timestamp.UTC(),
	}, nil
}

func memoryMetadata(input *MemoryInput) (*memory.Metadata, error) {
	if input == nil || (input.Kind == "" && input.EventTime == "" &&
		len(input.Participants) == 0 && input.Location == "") {
		return nil, nil
	}
	metadata := &memory.Metadata{
		Kind: input.Kind, Participants: append([]string(nil), input.Participants...),
		Location: input.Location,
	}
	if input.EventTime != "" {
		t, err := time.Parse(time.RFC3339Nano, input.EventTime)
		if err != nil {
			return nil, fmt.Errorf("parse memory event time: %w", err)
		}
		t = t.UTC()
		metadata.EventTime = &t
	}
	return metadata, nil
}

func addMemoryOptions(input *MemoryInput) ([]memory.AddOption, error) {
	metadata, err := memoryMetadata(input)
	if err != nil || metadata == nil {
		return nil, err
	}
	return []memory.AddOption{memory.WithMetadata(metadata)}, nil
}

// Snapshot is the storage-independent state captured at one checkpoint.
type Snapshot struct {
	CaseID     string            `json:"case_id"`
	Backend    string            `json:"backend"`
	Checkpoint string            `json:"checkpoint"`
	Sessions   []SessionSnapshot `json:"sessions"`
	Memories   []MemorySnapshot  `json:"memories"`
}

// SessionSnapshot contains the persisted session business data.
type SessionSnapshot struct {
	ID        string                     `json:"id"`
	AppName   string                     `json:"app_name"`
	UserID    string                     `json:"user_id"`
	State     map[string]json.RawMessage `json:"state"`
	Events    []EventSnapshot            `json:"events"`
	Summaries map[string]SummarySnapshot `json:"summaries"`
	Tracks    []TrackSnapshot            `json:"tracks"`
}

// TrackSnapshot contains one named observation stream in storage order.
type TrackSnapshot struct {
	Name   string               `json:"name"`
	Events []TrackEventSnapshot `json:"events"`
}

// TrackEventSnapshot contains comparable Track fields.
type TrackEventSnapshot struct {
	Index     int             `json:"index"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

// EventSnapshot contains comparable event fields.
type EventSnapshot struct {
	ID             string                     `json:"id"`
	Index          int                        `json:"index"`
	InvocationID   string                     `json:"invocation_id"`
	Author         string                     `json:"author"`
	Role           string                     `json:"role"`
	Content        string                     `json:"content"`
	ToolCalls      []ToolCallSnapshot         `json:"tool_calls"`
	ToolResponseID string                     `json:"tool_response_id,omitempty"`
	ToolName       string                     `json:"tool_name,omitempty"`
	StateDelta     map[string]json.RawMessage `json:"state_delta"`
	Timestamp      time.Time                  `json:"timestamp"`
	FilterKey      string                     `json:"filter_key,omitempty"`
}

// ToolCallSnapshot contains comparable tool-call fields.
type ToolCallSnapshot struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// SummarySnapshot contains summary semantics and structural boundary.
type SummarySnapshot struct {
	SessionID   string    `json:"session_id"`
	FilterKey   string    `json:"filter_key"`
	Content     string    `json:"content"`
	Topics      []string  `json:"topics"`
	Revision    int       `json:"revision"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int       `json:"version"`
	CutoffAt    time.Time `json:"cutoff_at"`
	LastEventID string    `json:"last_event_id,omitempty"`
}

// MemorySnapshot contains user-scoped memory business data.
type MemorySnapshot struct {
	ID           string      `json:"id"`
	Content      string      `json:"content"`
	Topics       []string    `json:"topics"`
	Kind         memory.Kind `json:"kind,omitempty"`
	EventTime    *time.Time  `json:"event_time,omitempty"`
	Participants []string    `json:"participants"`
	Location     string      `json:"location,omitempty"`
}

// CollectSnapshot reads back all state using public service APIs.
func CollectSnapshot(ctx context.Context, backend Backend, state *replayState, checkpoint string) (Snapshot, error) {
	snapshot := Snapshot{
		Backend: backend.Name(), Checkpoint: checkpoint,
		Sessions: make([]SessionSnapshot, 0, len(state.sessions)),
		Memories: []MemorySnapshot{},
	}
	sessionIDs := make([]string, 0, len(state.sessions))
	for sessionID := range state.sessions {
		sessionIDs = append(sessionIDs, sessionID)
	}
	sort.Strings(sessionIDs)
	for _, sessionID := range sessionIDs {
		sess, err := getReplaySession(ctx, backend, state, sessionID)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read session %s: %w", sessionID, err)
		}
		snapshot.Sessions = append(snapshot.Sessions, snapshotSession(sess))
	}
	entries, err := backend.MemoryService().ReadMemories(ctx,
		memory.UserKey{AppName: state.appName, UserID: state.userID}, 0)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memories: %w", err)
	}
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		item := MemorySnapshot{
			ID: entry.ID, Content: entry.Memory.Memory,
			Topics:       append([]string(nil), entry.Memory.Topics...),
			Kind:         entry.Memory.Kind,
			Participants: append([]string(nil), entry.Memory.Participants...),
			Location:     entry.Memory.Location,
		}
		if entry.Memory.EventTime != nil {
			t := entry.Memory.EventTime.UTC()
			item.EventTime = &t
		}
		snapshot.Memories = append(snapshot.Memories, item)
	}
	sort.Slice(snapshot.Memories, func(i, j int) bool {
		if snapshot.Memories[i].Content == snapshot.Memories[j].Content {
			return snapshot.Memories[i].ID < snapshot.Memories[j].ID
		}
		return snapshot.Memories[i].Content < snapshot.Memories[j].Content
	})
	return snapshot, nil
}

func snapshotSession(sess *session.Session) SessionSnapshot {
	result := SessionSnapshot{
		ID: sess.ID, AppName: sess.AppName, UserID: sess.UserID,
		State: make(map[string]json.RawMessage), Events: []EventSnapshot{},
		Summaries: make(map[string]SummarySnapshot), Tracks: []TrackSnapshot{},
	}
	for key, value := range sess.SnapshotState() {
		result.State[key] = append(json.RawMessage(nil), value...)
	}
	sess.EventMu.RLock()
	for i, evt := range sess.Events {
		item := EventSnapshot{
			ID: evt.ID, Index: i, InvocationID: evt.InvocationID, Author: evt.Author,
			Timestamp: evt.Timestamp.UTC(), FilterKey: evt.FilterKey,
			ToolCalls: []ToolCallSnapshot{}, StateDelta: make(map[string]json.RawMessage),
		}
		for key, value := range evt.StateDelta {
			item.StateDelta[key] = append(json.RawMessage(nil), value...)
		}
		if evt.Response != nil && len(evt.Choices) > 0 {
			message := evt.Choices[0].Message
			item.Role, item.Content = message.Role.String(), message.Content
			item.ToolResponseID, item.ToolName = message.ToolID, message.ToolName
			for _, call := range message.ToolCalls {
				item.ToolCalls = append(item.ToolCalls, ToolCallSnapshot{
					ID: call.ID, Name: call.Function.Name,
					Arguments: append(json.RawMessage(nil), call.Function.Arguments...),
				})
			}
		}
		result.Events = append(result.Events, item)
	}
	sess.EventMu.RUnlock()
	sess.SummariesMu.RLock()
	for filterKey, summary := range sess.Summaries {
		if summary == nil {
			continue
		}
		item := SummarySnapshot{
			SessionID: sess.ID, FilterKey: filterKey, Content: summary.Summary,
			Topics:   append([]string(nil), summary.Topics...),
			Revision: summary.Revision, UpdatedAt: summary.UpdatedAt.UTC(),
		}
		if boundary := summary.CutoffBoundary(); boundary != nil {
			item.Version, item.CutoffAt = boundary.Version, boundary.CutoffTime()
			item.LastEventID = boundary.LastEventID
		}
		result.Summaries[filterKey] = item
	}
	sess.SummariesMu.RUnlock()
	trackNames := make([]string, 0, len(sess.Tracks))
	sess.TracksMu.RLock()
	for track := range sess.Tracks {
		trackNames = append(trackNames, string(track))
	}
	sort.Strings(trackNames)
	for _, trackName := range trackNames {
		history := sess.Tracks[session.Track(trackName)]
		if history == nil {
			continue
		}
		track := TrackSnapshot{Name: trackName, Events: []TrackEventSnapshot{}}
		for i, evt := range history.Events {
			track.Events = append(track.Events, TrackEventSnapshot{
				Index: i, Payload: append(json.RawMessage(nil), evt.Payload...),
				Timestamp: evt.Timestamp.UTC(),
			})
		}
		result.Tracks = append(result.Tracks, track)
	}
	sess.TracksMu.RUnlock()
	return result
}

// deterministicSummarizer removes model nondeterminism from storage tests.
type deterministicSummarizer struct{}

const replaySummaryEventThreshold = 8

func (deterministicSummarizer) ShouldSummarize(sess *session.Session) bool {
	return sess != nil && len(sess.Events) >= replaySummaryEventThreshold
}
func (deterministicSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	type item struct {
		Author, Role, Content string
	}
	items := make([]item, 0, len(sess.Events))
	for _, evt := range sess.Events {
		entry := item{Author: evt.Author}
		if evt.Response != nil && len(evt.Choices) > 0 {
			entry.Role = evt.Choices[0].Message.Role.String()
			entry.Content = evt.Choices[0].Message.Content
		}
		items = append(items, entry)
	}
	raw, err := json.Marshal(items)
	return string(raw), err
}
func (deterministicSummarizer) SetPrompt(string)     {}
func (deterministicSummarizer) SetModel(model.Model) {}
func (deterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"kind": "deterministic"}
}

// Mutation describes a deliberate corruption used to verify detection.
type Mutation struct {
	Name, Path string
}

const (
	MutationSummaryMissing      = "summary_missing"
	MutationSummaryOverwrite    = "summary_overwrite"
	MutationSummaryWrongSession = "summary_wrong_session"
	MutationEventDuplicate      = "event_duplicate"
	MutationStateDirty          = "state_dirty"
	MutationTrackPayload        = "track_payload"
)

// ApplyMutation changes one meaningful field.
func ApplyMutation(snapshot *Snapshot) (Mutation, error) {
	if snapshot == nil {
		return Mutation{}, errors.New("snapshot is nil")
	}
	for i := range snapshot.Sessions {
		if len(snapshot.Sessions[i].Summaries) > 0 {
			return ApplySummaryMutation(snapshot, MutationSummaryOverwrite)
		}
	}
	for i := range snapshot.Sessions {
		if len(snapshot.Sessions[i].Events) > 0 {
			snapshot.Sessions[i].Events[0].Content += " [mutated]"
			return Mutation{"event_content",
				fmt.Sprintf("sessions[%d].events[0].content", i)}, nil
		}
	}
	if len(snapshot.Memories) > 0 {
		snapshot.Memories[0].Content += " [mutated]"
		return Mutation{"memory_content", "memories[0].content"}, nil
	}
	return Mutation{}, errors.New("snapshot has no mutable business field")
}

// ApplyTrackMutation verifies that Track payload differences are observable.
func ApplyTrackMutation(snapshot *Snapshot) (Mutation, error) {
	if snapshot == nil {
		return Mutation{}, errors.New("snapshot is nil")
	}
	for i := range snapshot.Sessions {
		for j := range snapshot.Sessions[i].Tracks {
			if len(snapshot.Sessions[i].Tracks[j].Events) == 0 {
				continue
			}
			snapshot.Sessions[i].Tracks[j].Events[0].Payload = json.RawMessage(`{"mutated":true}`)
			return Mutation{MutationTrackPayload,
				fmt.Sprintf("sessions[%d].tracks[%d].events[0].payload", i, j)}, nil
		}
	}
	return Mutation{}, errors.New("snapshot has no track event to mutate")
}

// ApplyEventDuplicateMutation simulates a backend returning the same event
// twice after an ambiguous write retry.
func ApplyEventDuplicateMutation(snapshot *Snapshot) (Mutation, error) {
	if snapshot == nil {
		return Mutation{}, errors.New("snapshot is nil")
	}
	for i := range snapshot.Sessions {
		if len(snapshot.Sessions[i].Events) == 0 {
			continue
		}
		duplicate := snapshot.Sessions[i].Events[0]
		duplicate.Index = len(snapshot.Sessions[i].Events)
		snapshot.Sessions[i].Events = append(
			snapshot.Sessions[i].Events,
			duplicate,
		)
		return Mutation{
			MutationEventDuplicate,
			fmt.Sprintf("sessions[%d].events", i),
		}, nil
	}
	return Mutation{}, errors.New("snapshot has no event to duplicate")
}

// ApplyStateDirtyMutation simulates a state/event partial-write mismatch.
func ApplyStateDirtyMutation(snapshot *Snapshot) (Mutation, error) {
	if snapshot == nil {
		return Mutation{}, errors.New("snapshot is nil")
	}
	for i := range snapshot.Sessions {
		if snapshot.Sessions[i].State == nil {
			snapshot.Sessions[i].State = make(map[string]json.RawMessage)
		}
		snapshot.Sessions[i].State["recovery_status"] =
			json.RawMessage(`"dirty"`)
		return Mutation{
			MutationStateDirty,
			fmt.Sprintf("sessions[%d].state.recovery_status", i),
		}, nil
	}
	return Mutation{}, errors.New("snapshot has no session state to corrupt")
}

// ApplySummaryMutation injects one of the three summary-specific failures
// required by the replay consistency acceptance criteria.
func ApplySummaryMutation(snapshot *Snapshot, kind string) (Mutation, error) {
	if snapshot == nil {
		return Mutation{}, errors.New("snapshot is nil")
	}
	sourceIndex := -1
	var filterKey string
	for i := range snapshot.Sessions {
		keys := make([]string, 0, len(snapshot.Sessions[i].Summaries))
		for key := range snapshot.Sessions[i].Summaries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			sourceIndex, filterKey = i, keys[0]
			break
		}
	}
	if sourceIndex < 0 {
		return Mutation{}, errors.New("snapshot has no summary")
	}
	summary := snapshot.Sessions[sourceIndex].Summaries[filterKey]
	switch kind {
	case MutationSummaryMissing:
		delete(snapshot.Sessions[sourceIndex].Summaries, filterKey)
		return Mutation{kind, fmt.Sprintf(
			"sessions[%d].summaries.%s", sourceIndex, filterKey)}, nil
	case MutationSummaryOverwrite:
		summary.Content += " [stale overwrite]"
		snapshot.Sessions[sourceIndex].Summaries[filterKey] = summary
		return Mutation{kind, fmt.Sprintf(
			"sessions[%d].summaries.%s.content", sourceIndex, filterKey)}, nil
	case MutationSummaryWrongSession:
		if len(snapshot.Sessions) < 2 {
			return Mutation{}, errors.New("wrong-session mutation requires two sessions")
		}
		targetIndex := (sourceIndex + 1) % len(snapshot.Sessions)
		delete(snapshot.Sessions[sourceIndex].Summaries, filterKey)
		summary.SessionID = snapshot.Sessions[targetIndex].ID
		snapshot.Sessions[targetIndex].Summaries[filterKey] = summary
		return Mutation{kind, fmt.Sprintf(
			"sessions[%d].summaries.%s.session_id", targetIndex, filterKey)}, nil
	default:
		return Mutation{}, fmt.Errorf("unsupported summary mutation %q", kind)
	}
}

func cloneSnapshot(input Snapshot) (Snapshot, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Snapshot{}, err
	}
	var output Snapshot
	err = json.Unmarshal(raw, &output)
	return output, err
}
