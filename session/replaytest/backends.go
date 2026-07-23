//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

// Backend Integration Guide
//
// Adding a new backend:
//
// 1. Define a BackendFactory with a unique Name, Enabled flag (controlled by
//    environment variable), and a New function that creates session.Service
//    and memory.Service instances.
//
// 2. Register the factory in init() via RegisterBackend().
//
// 3. Environment variable naming convention:
//      REPLAYTEST_<NAME>_ENABLED=true/false
//    For backends requiring connection strings:
//      REPLAYTEST_<NAME>_URL  or  REPLAYTEST_<NAME>_DSN
//
// 4. All external backends (Redis, Postgres, MySQL, ClickHouse) are disabled
//    by default and require the user to set the corresponding env vars.
//
// 5. Backends that don't support a feature (e.g., ClickHouse has no memory
//    service) should return nil for the unsupported service type.
//
// Currently registered backends and their env vars:
//
//   InMemory   REPLAYTEST_INMEMORY_ENABLED  (default: true)
//   SQLite     REPLAYTEST_SQLITE_ENABLED     (default: true)
//   Redis      REPLAYTEST_REDIS_ENABLED      (default: false)
//              REPLAYTEST_REDIS_URL          e.g. redis://localhost:6379/0
//   Postgres   REPLAYTEST_POSTGRES_ENABLED   (default: false)
//              REPLAYTEST_POSTGRES_DSN       e.g. postgres://user:pass@localhost:5432/db
//   MySQL      REPLAYTEST_MYSQL_ENABLED      (default: false)
//              REPLAYTEST_MYSQL_DSN          e.g. user:pass@tcp(localhost:3306)/db
//   ClickHouse REPLAYTEST_CLICKHOUSE_ENABLED (default: false)
//              REPLAYTEST_CLICKHOUSE_DSN     e.g. clickhouse://user:pass@localhost:9000/db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	mmysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	mpostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	mredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	msqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	smysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	spostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	ssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"

	inmemorymemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	inmemorysession "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

	_ "github.com/mattn/go-sqlite3"
)

// BackendCapability describes a feature a backend may or may not support.
type BackendCapability string

const (
	// CapEventPaging indicates the backend supports event pagination (offset/limit).
	CapEventPaging BackendCapability = "event_paging"
	// CapTrack indicates the backend supports track events.
	CapTrack BackendCapability = "track"
	// CapSummaryFilterKey indicates the backend supports summary filter-key branching.
	CapSummaryFilterKey BackendCapability = "summary_filter_key"
	// CapMemorySearch indicates the backend supports memory search.
	CapMemorySearch BackendCapability = "memory_search"
	// CapTTL indicates the backend supports TTL-based expiration.
	CapTTL BackendCapability = "ttl"
)

// BackendCapabilities returns the set of capabilities for a given backend name.
// This is used by the Comparator to determine which differences are "allowed".
func BackendCapabilities(name string) map[BackendCapability]bool {
	caps := map[BackendCapability]bool{
		CapEventPaging:      false,
		CapTrack:            false,
		CapSummaryFilterKey: false,
		CapMemorySearch:     false,
		CapTTL:              false,
	}
	switch name {
	case "InMemory":
		caps[CapTrack] = true
		caps[CapSummaryFilterKey] = true
		caps[CapMemorySearch] = true
	case "SQLite":
		caps[CapTrack] = true
		caps[CapSummaryFilterKey] = true
		caps[CapMemorySearch] = true
	case "Redis":
		caps[CapTrack] = true
		caps[CapSummaryFilterKey] = true
		caps[CapMemorySearch] = true
		caps[CapTTL] = true
	case "Postgres":
		caps[CapEventPaging] = true
		caps[CapTrack] = true
		caps[CapSummaryFilterKey] = true
		caps[CapMemorySearch] = true
	case "MySQL":
		caps[CapEventPaging] = true
		caps[CapTrack] = true
		caps[CapSummaryFilterKey] = true
		caps[CapMemorySearch] = true
	case "ClickHouse":
		// ClickHouse only supports session, no memory search.
	}
	return caps
}

type BackendFactory struct {
	// Name is the human-readable backend name.
	Name string `json:"name"`
	// Enabled indicates whether the backend is enabled.
	Enabled bool `json:"enabled"`
	// New creates a new backend instance returning session and memory services.
	New func() (session.Service, memory.Service, error)
}

var (
	mu            sync.RWMutex
	backends      []BackendFactory
	envVarPrefix  = "REPLAYTEST_"
)

// RegisterBackend registers a backend factory.
func RegisterBackend(factory BackendFactory) {
	mu.Lock()
	defer mu.Unlock()
	backends = append(backends, factory)
}

// GetBackends returns the list of registered backends.
func GetBackends() []BackendFactory {
	mu.RLock()
	defer mu.RUnlock()
	result := make([]BackendFactory, len(backends))
	copy(result, backends)
	return result
}

// envEnabled checks if a backend is enabled via environment variable.
// Defaults to defaultValue if the env var is not set.
func envEnabled(name string, defaultValue bool) bool {
	envKey := envVarPrefix + name + "_ENABLED"
	val, ok := os.LookupEnv(envKey)
	if !ok {
		return defaultValue
	}
	return val == "true" || val == "1" || val == "yes"
}

// envVarName returns the environment variable name for a backend.
func envVarName(name string) string {
	return envVarPrefix + name + "_ENABLED"
}

// RunBackends runs a function on all enabled backends in parallel.
func RunBackends(ctx context.Context, fn func(ctx context.Context, name string, sessSvc session.Service, memSvc memory.Service) error) []error {
	backends := GetBackends()
	var wg sync.WaitGroup
	errCh := make(chan error, len(backends))

	for _, b := range backends {
		if !b.Enabled {
			continue
		}
		wg.Add(1)
		go func(b BackendFactory) {
			defer wg.Done()
			sessSvc, memSvc, err := b.New()
			if err != nil {
				errCh <- fmt.Errorf("%s: create services: %w", b.Name, err)
				return
			}
			defer sessSvc.Close()
			defer memSvc.Close()

			if err := fn(ctx, b.Name, sessSvc, memSvc); err != nil {
				errCh <- fmt.Errorf("%s: %w", b.Name, err)
			}
		}(b)
	}
	wg.Wait()
	close(errCh)

	var errors []error
	for err := range errCh {
		errors = append(errors, err)
	}
	return errors
}

// init registers the built-in backends.
func init() {
	// InMemory backend — always enabled by default.
	RegisterBackend(BackendFactory{
		Name:    "InMemory",
		Enabled: envEnabled("INMEMORY", true),
		New: func() (session.Service, memory.Service, error) {
			sessSvc := inmemorysession.NewSessionService()
			memSvc := inmemorymemory.NewMemoryService()
			return sessSvc, memSvc, nil
		},
	})

	// SQLite backend — always enabled by default.
	  RegisterBackend(BackendFactory{
	   Name:    "SQLite",
	   Enabled: envEnabled("SQLITE", true),
	   New: func() (session.Service, memory.Service, error) {
	    db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	    if err != nil {
	     return nil, nil, fmt.Errorf("open sqlite: %w", err)
	    }
	    sessSvc, err := ssqlite.NewService(db)
	    if err != nil {
	     db.Close()
	     return nil, nil, fmt.Errorf("create sqlite session service: %w", err)
	    }
	    memSvc, err := msqlite.NewService(db)
	    if err != nil {
	     sessSvc.Close()
	     db.Close()
	     return nil, nil, fmt.Errorf("create sqlite memory service: %w", err)
	    }
	    return sessSvc, memSvc, nil
	   },
	  })

	  // Redis backend — disabled by default. Enable with REPLAYTEST_REDIS_ENABLED=true
	  // and set REPLAYTEST_REDIS_URL=redis://localhost:6379/0
	  RegisterBackend(BackendFactory{
	   Name:    "Redis",
	   Enabled: envEnabled("REDIS", false),
	   New: func() (session.Service, memory.Service, error) {
	    redisURL := os.Getenv("REPLAYTEST_REDIS_URL")
	    if redisURL == "" {
	     return nil, nil, fmt.Errorf("REPLAYTEST_REDIS_URL not set")
	    }
	    sessSvc, err := sredis.NewService(sredis.WithRedisClientURL(redisURL))
	    if err != nil {
	     return nil, nil, fmt.Errorf("create redis session service: %w", err)
	    }
	    memSvc, err := mredis.NewService(mredis.WithRedisClientURL(redisURL))
	    if err != nil {
	     sessSvc.Close()
	     return nil, nil, fmt.Errorf("create redis memory service: %w", err)
	    }
	    return sessSvc, memSvc, nil
	   },
	  })

	  // Postgres backend — disabled by default. Enable with REPLAYTEST_POSTGRES_ENABLED=true
	  // and set REPLAYTEST_POSTGRES_DSN=postgres://user:password@localhost:5432/dbname?sslmode=disable
	  RegisterBackend(BackendFactory{
	   Name:    "Postgres",
	   Enabled: envEnabled("POSTGRES", false),
	   New: func() (session.Service, memory.Service, error) {
	    dsn := os.Getenv("REPLAYTEST_POSTGRES_DSN")
	    if dsn == "" {
	     return nil, nil, fmt.Errorf("REPLAYTEST_POSTGRES_DSN not set")
	    }
	    sessSvc, err := spostgres.NewService(spostgres.WithPostgresClientDSN(dsn))
	    if err != nil {
	     return nil, nil, fmt.Errorf("create postgres session service: %w", err)
	    }
	    memSvc, err := mpostgres.NewService(mpostgres.WithPostgresClientDSN(dsn))
	    if err != nil {
	     sessSvc.Close()
	     return nil, nil, fmt.Errorf("create postgres memory service: %w", err)
	    }
	    return sessSvc, memSvc, nil
	   },
	  })

	  // MySQL backend — disabled by default. Enable with REPLAYTEST_MYSQL_ENABLED=true
	  // and set REPLAYTEST_MYSQL_DSN=user:password@tcp(localhost:3306)/sessions?parseTime=true&charset=utf8mb4
	  RegisterBackend(BackendFactory{
	   Name:    "MySQL",
	   Enabled: envEnabled("MYSQL", false),
	   New: func() (session.Service, memory.Service, error) {
	    dsn := os.Getenv("REPLAYTEST_MYSQL_DSN")
	    if dsn == "" {
	     return nil, nil, fmt.Errorf("REPLAYTEST_MYSQL_DSN not set")
	    }
	    sessSvc, err := smysql.NewService(smysql.WithMySQLClientDSN(dsn))
	    if err != nil {
	     return nil, nil, fmt.Errorf("create mysql session service: %w", err)
	    }
	    memSvc, err := mmysql.NewService(mmysql.WithMySQLClientDSN(dsn))
	    if err != nil {
	     sessSvc.Close()
	     return nil, nil, fmt.Errorf("create mysql memory service: %w", err)
	    }
	    return sessSvc, memSvc, nil
	   },
	  })

	  // ClickHouse backend — disabled by default. Enable with REPLAYTEST_CLICKHOUSE_ENABLED=true
	    // and set REPLAYTEST_CLICKHOUSE_DSN=clickhouse://user:password@localhost:9000/sessions?dial_timeout=10s
	    // Note: ClickHouse only supports session service, not memory.
	    RegisterBackend(BackendFactory{
	     Name:    "ClickHouse",
	     Enabled: envEnabled("CLICKHOUSE", false),
	     New: func() (session.Service, memory.Service, error) {
	      dsn := os.Getenv("REPLAYTEST_CLICKHOUSE_DSN")
	      if dsn == "" {
	       return nil, nil, fmt.Errorf("REPLAYTEST_CLICKHOUSE_DSN not set")
	      }
	      sessSvc, err := sclickhouse.NewService(sclickhouse.WithClickHouseDSN(dsn))
	      if err != nil {
	       return nil, nil, fmt.Errorf("create clickhouse session service: %w", err)
	      }
	      // ClickHouse has no dedicated memory service; return nil for memory.
	      return sessSvc, nil, nil
	     },
	    })
}

// executeOps executes a sequence of replay operations on a given backend.
func executeOps(ctx context.Context, sessSvc session.Service, memSvc memory.Service, ops []ReplayOp) (*BackendResult, error) {
	result := &BackendResult{
		SummaryTexts: make(map[string]string),
		Tracks:       make(map[session.Track]*session.TrackEvents),
	}

	for _, op := range ops {
		if err := executeOp(ctx, sessSvc, memSvc, op, result); err != nil {
			return nil, fmt.Errorf("op %s: %w", op.Type, err)
		}
	}

	return result, nil
}

// executeOp executes a single replay operation.
func executeOp(ctx context.Context, sessSvc session.Service, memSvc memory.Service, op ReplayOp, result *BackendResult) error {
	switch op.Type {
	case OpCreateSession:
		stateMap, _ := op.Data.(session.StateMap)
		sess, err := sessSvc.CreateSession(ctx, op.Key, stateMap)
		if err != nil {
			return fmt.Errorf("CreateSession: %w", err)
		}
		result.Session = sess

	case OpAppendEvent:
		if result.Session == nil {
			return fmt.Errorf("AppendEvent: no session created yet")
		}
		ed, ok := op.Data.(EventData)
		if !ok {
			return fmt.Errorf("AppendEvent: invalid data type %T", op.Data)
		}
		if err := sessSvc.AppendEvent(ctx, result.Session, ed.Event); err != nil {
			return fmt.Errorf("AppendEvent: %w", err)
		}

	case OpUpdateSessionState:
		if result.Session == nil {
			return fmt.Errorf("UpdateSessionState: no session created yet")
		}
		sd, ok := op.Data.(StateData)
		if !ok {
			return fmt.Errorf("UpdateSessionState: invalid data type %T", op.Data)
		}
		if err := sessSvc.UpdateSessionState(ctx, op.Key, sd.State); err != nil {
			return fmt.Errorf("UpdateSessionState: %w", err)
		}

	case OpDeleteSessionState:
		// Only the first key is deleted for simplicity.
		if stateMap, ok := op.Data.(session.StateMap); ok {
			for key := range stateMap {
				if err := sessSvc.UpdateSessionState(ctx, op.Key, session.StateMap{key: nil}); err != nil {
					return fmt.Errorf("DeleteSessionState: %w", err)
				}
				break
			}
		}

	case OpAddMemory:
		md, ok := op.Data.(MemoryData)
		if !ok {
			return fmt.Errorf("AddMemory: invalid data type %T", op.Data)
		}
		var opts []memory.AddOption
		if md.Metadata != nil {
			opts = append(opts, memory.WithMetadata(md.Metadata))
		}
		if err := memSvc.AddMemory(ctx, md.UserKey, md.Memory, md.Topics, opts...); err != nil {
			return fmt.Errorf("AddMemory: %w", err)
		}

	case OpUpdateMemory:
		md, ok := op.Data.(MemoryData)
		if !ok {
			return fmt.Errorf("UpdateMemory: invalid data type %T", op.Data)
		}
		var opts []memory.UpdateOption
		if md.Metadata != nil {
			opts = append(opts, memory.WithUpdateMetadata(md.Metadata))
		}
		memKey := memory.Key{AppName: md.UserKey.AppName, UserID: md.UserKey.UserID, MemoryID: ""}
		if err := memSvc.UpdateMemory(ctx, memKey, md.Memory, md.Topics, opts...); err != nil {
			return fmt.Errorf("UpdateMemory: %w", err)
		}

	case OpDeleteMemory:
		mk, ok := op.Data.(memory.Key)
		if !ok {
			return fmt.Errorf("DeleteMemory: invalid data type %T", op.Data)
		}
		if err := memSvc.DeleteMemory(ctx, mk); err != nil {
			return fmt.Errorf("DeleteMemory: %w", err)
		}

	case OpClearMemories:
		uk, ok := op.Data.(memory.UserKey)
		if !ok {
			return fmt.Errorf("ClearMemories: invalid data type %T", op.Data)
		}
		if err := memSvc.ClearMemories(ctx, uk); err != nil {
			return fmt.Errorf("ClearMemories: %w", err)
		}

	case OpCreateSessionSummary:
		if result.Session == nil {
			return fmt.Errorf("CreateSessionSummary: no session created yet")
		}
		sd, ok := op.Data.(SummaryData)
		if !ok {
			return fmt.Errorf("CreateSessionSummary: invalid data type %T", op.Data)
		}
		if err := sessSvc.CreateSessionSummary(ctx, result.Session, sd.FilterKey, sd.Force); err != nil {
			return fmt.Errorf("CreateSessionSummary: %w", err)
		}

	case OpGetSession:
		sess, err := sessSvc.GetSession(ctx, op.Key)
		if err != nil {
			return fmt.Errorf("GetSession: %w", err)
		}
		result.Session = sess

	case OpGetSessionSummaryText:
		if result.Session == nil {
			return fmt.Errorf("GetSessionSummaryText: no session created yet")
		}
		text, ok := sessSvc.GetSessionSummaryText(ctx, result.Session)
		if ok {
			result.SummaryTexts[""] = text
		}

	case OpAppendTrackEvent:
		if result.Session == nil {
			return fmt.Errorf("AppendTrackEvent: no session created yet")
		}
		td, ok := op.Data.(TrackEventData)
		if !ok {
			return fmt.Errorf("AppendTrackEvent: invalid data type %T", op.Data)
		}
		// Check if the session service implements TrackService.
		if ts, ok := sessSvc.(session.TrackService); ok {
			if err := ts.AppendTrackEvent(ctx, result.Session, td.Event); err != nil {
				return fmt.Errorf("AppendTrackEvent: %w", err)
			}
		}

	case OpReadMemories:
		uk, ok := op.Data.(memory.UserKey)
		if !ok {
			return fmt.Errorf("ReadMemories: invalid data type %T", op.Data)
		}
		entries, err := memSvc.ReadMemories(ctx, uk, 100)
		if err != nil {
			return fmt.Errorf("ReadMemories: %w", err)
		}
		result.Memories = entries

	case OpSearchMemories:
		type searchData struct {
			UserKey memory.UserKey
			Query   string
		}
		sd, ok := op.Data.(searchData)
		if !ok {
			// Fallback: try UserKey with empty query.
			uk, ok := op.Data.(memory.UserKey)
			if !ok {
				return fmt.Errorf("SearchMemories: invalid data type %T", op.Data)
			}
			sd = searchData{UserKey: uk}
		}
		entries, err := memSvc.SearchMemories(ctx, sd.UserKey, sd.Query)
		if err != nil {
			return fmt.Errorf("SearchMemories: %w", err)
		}
		result.Memories = entries

	default:
		return fmt.Errorf("unknown operation type: %s", op.Type)
	}

	return nil
}

// NewEvent creates a basic event with the given author and content.
func NewEvent(invocationID, author, role, content string) *event.Event {
	return event.New(invocationID, author,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.Role(role),
						Content: content,
					},
				},
			},
		}),
	)
}

// NewToolCallEvent creates a tool call event.
func NewToolCallEvent(invocationID, author, toolCallID, toolName string, arguments json.RawMessage) *event.Event {
	return event.New(invocationID, author,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID:   toolCallID,
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name:      toolName,
									Arguments: arguments,
								},
							},
						},
					},
				},
			},
		}),
	)
}

// NewToolResponseEvent creates a tool response event.
func NewToolResponseEvent(invocationID, author, toolCallID, toolName, content string) *event.Event {
	return event.New(invocationID, author,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleTool,
						Content: content,
						ToolID:  toolCallID,
						ToolName: toolName,
					},
				},
			},
		}),
	)
}