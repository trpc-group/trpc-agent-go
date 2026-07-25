//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- NewService / lifecycle ---

func TestNewService_FailsWithoutAPIKey(t *testing.T) {
	_, err := NewService()
	assert.Error(t, err, "missing api key must fail")
}

func TestNewService_SelfHostedOSSAllowsEmptyAPIKey(t *testing.T) {
	svc, err := NewService(WithSelfHostedOSS())
	require.NoError(t, err)
	defer svc.Close()
	assert.Equal(t, apiModeSelfHostedOSS, svc.opts.apiMode)
	assert.Equal(t, defaultSelfHostedOSSHost, svc.opts.host)
}

func TestNewService_SelfHostedOSSRejectsCloudDefaultHost(t *testing.T) {
	_, err := NewService(WithSelfHostedOSS(), WithHost(defaultHost))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default host")

	_, err = NewService(WithSelfHostedOSS(), WithHost(defaultHost+"/"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default host")
}

func TestNewService_SelfHostedOSSRejectsOrgProject(t *testing.T) {
	_, err := NewService(
		WithSelfHostedOSS(),
		WithHost("http://localhost:8888"),
		WithOrgProject("org", "project"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "org/project")
}

func TestNewService_DefaultsToolsList(t *testing.T) {
	svc, err := NewService(WithAPIKey("k"))
	require.NoError(t, err)
	defer svc.Close()
	assert.Len(t, svc.Tools(), 1)

	// Tools() must return a fresh slice (not share backing array).
	tools1 := svc.Tools()
	tools1[0] = nil
	tools2 := svc.Tools()
	assert.NotNil(t, tools2[0], "Tools() must return an independent slice")
}

func TestService_CloseIsIdempotent(t *testing.T) {
	svc, err := NewService(WithAPIKey("k"))
	require.NoError(t, err)
	assert.NoError(t, svc.Close())
	assert.NoError(t, svc.Close(), "second Close must be a no-op")
}

// --- IngestSession ---

func TestIngestSession_InvalidUserKeyReturnsError(t *testing.T) {
	svc, err := NewService(WithAPIKey("test-key"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	cases := []struct {
		name string
		sess *session.Session
	}{
		{"empty app name", &session.Session{UserID: "u", ID: "s"}},
		{"empty user id", &session.Session{AppName: "a", ID: "s"}},
		{"both empty", &session.Session{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, svc.IngestSession(context.Background(), tc.sess))
		})
	}
}

func TestIngestSession_NilSessionReturnsNil(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	assert.NoError(t, svc.IngestSession(context.Background(), nil))
}

func TestIngestSession_NoMessagesReturnsNil(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	// A session with no events and a valid user key → nothing to ingest.
	sess := &session.Session{AppName: "app", UserID: "user", ID: "s"}
	assert.NoError(t, svc.IngestSession(context.Background(), sess))
}

func TestIngestSession_NoIngestibleTextSkipsOptionsAndAdvancesWatermark(t *testing.T) {
	var requestCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	var resolverCalls atomic.Int32
	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(server.URL),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				resolverCalls.Add(1)
				return time.Time{}, nil
			},
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	eventTime := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	sess := &session.Session{
		AppName: "app",
		UserID:  "user",
		ID:      "session",
		Events: []event.Event{{
			Timestamp: eventTime,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: " \n\t "},
			}}},
		}},
	}
	var optionCalls atomic.Int32
	option := session.IngestOption(func(*session.IngestOptions) {
		optionCalls.Add(1)
	})

	require.NoError(t, svc.IngestSession(context.Background(), sess, option))
	assert.Equal(t, eventTime, readLastExtractAt(sess))
	assert.Zero(t, optionCalls.Load())
	assert.Zero(t, resolverCalls.Load())
	assert.Zero(t, requestCalls.Load())
}

func TestIngestSession_EarlyReturnDoesNotResolveOptions(t *testing.T) {
	var resolverCalls atomic.Int32
	svc, err := NewService(
		WithSelfHostedOSS(),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				resolverCalls.Add(1)
				return time.Time{}, nil
			},
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	tests := []struct {
		name    string
		sess    *session.Session
		wantErr bool
	}{
		{name: "nil session"},
		{
			name:    "invalid user key",
			sess:    &session.Session{UserID: "user", ID: "session"},
			wantErr: true,
		},
		{
			name: "empty delta",
			sess: &session.Session{AppName: "app", UserID: "user", ID: "session"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			option := session.IngestOption(func(*session.IngestOptions) {
				calls++
			})
			err := svc.IngestSession(context.Background(), tt.sess, option)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Zero(t, calls)
			assert.Zero(t, resolverCalls.Load())
		})
	}
}

func TestIngestSession_NilContext(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	sess := &session.Session{AppName: "app", UserID: "user", ID: "s"}
	// Route through an interface var so staticcheck can't statically see the
	// literal nil; we're intentionally exercising the nil-context fallback.
	var nilCtx context.Context
	assert.NoError(t, svc.IngestSession(nilCtx, sess))
}

func TestIngestSession_ForwardsMessagesToBackend(t *testing.T) {
	gotBody := make(chan []byte, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			gotBody <- nil
			return
		}
		gotBody <- body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"x","status":"SUCCEEDED"}]`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithAPIKey("k"),
		WithHost(srv.URL),
		WithAsyncMode(false),
		WithMemoryJobTimeout(time.Second),
	)
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{AppName: "app", UserID: "user", ID: "s"}
	sess.Events = []event.Event{{
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}}

	require.NoError(t, svc.IngestSession(context.Background(), sess,
		session.WithIngestMetadata(map[string]any{"k": "v"}),
		session.WithIngestAgentID("agent-1"),
		session.WithIngestRunID("run-1"),
	))

	var body string
	select {
	case requestBody := <-gotBody:
		body = string(requestBody)
	case <-time.After(2 * time.Second):
		t.Fatal("backend was not hit")
	}
	for _, want := range []string{"hello", "agent-1", "run-1", `"k":"v"`, `"infer":true`} {
		assert.Contains(t, body, want)
	}
}

func TestIngestSession_ForwardsSelfHostedExtractionFields(t *testing.T) {
	var resolverCalls atomic.Int32
	expirationLocation := time.FixedZone("UTC-07", -7*60*60)
	gotBody := make(chan map[string]any, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !decodeTestJSONRequest(w, r, &body) {
			return
		}
		gotBody <- body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithMemoryJobTimeout(time.Second),
		WithSelfHostedIngestPrompt("extract deployment steps"),
		WithSelfHostedIngestExpirationDateResolver(
			func(_ context.Context, sess *session.Session) (time.Time, error) {
				resolverCalls.Add(1)
				return sess.CreatedAt.In(expirationLocation).AddDate(0, 0, 30), nil
			},
		),
		WithSelfHostedProceduralMemory(),
	)
	require.NoError(t, err)
	defer svc.Close()

	sess := newIngestTestSession()
	sess.CreatedAt = time.Date(2026, time.July, 2, 23, 0, 0, 0, expirationLocation)
	require.NoError(t, svc.IngestSession(
		context.Background(),
		sess,
		session.WithIngestMetadata(map[string]any{"source": "test"}),
		session.WithIngestAgentID("agent-1"),
		session.WithIngestRunID("run-1"),
	))

	select {
	case body := <-gotBody:
		assert.Equal(t, "agent-1", body["agent_id"])
		assert.Equal(t, "run-1", body["run_id"])
		assert.Equal(t, "extract deployment steps", body["prompt"])
		assert.Equal(t, "2026-08-01", body["expiration_date"])
		assert.Equal(t, true, body["infer"])
		assert.Equal(t, memoryTypeProcedural, body["memory_type"])
		metadata, ok := body["metadata"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "test", metadata["source"])
		assert.Equal(t, "app", metadata[metadataKeyTRPCAppName])
	case <-time.After(2 * time.Second):
		t.Fatal("backend was not hit")
	}
	assert.Equal(t, int32(1), resolverCalls.Load())
}

func TestIngestSession_ForwardsDisabledInference(t *testing.T) {
	var resolverCalls atomic.Int32
	gotBody := make(chan map[string]any, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !decodeTestJSONRequest(w, r, &body) {
			return
		}
		gotBody <- body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithMemoryJobTimeout(time.Second),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				resolverCalls.Add(1)
				return time.Time{}, nil
			},
		),
		WithIngestInference(false),
	)
	require.NoError(t, err)
	defer svc.Close()

	require.NoError(t, svc.IngestSession(context.Background(), newIngestTestSession()))
	select {
	case body := <-gotBody:
		assert.Equal(t, false, body["infer"])
		assert.NotContains(t, body, "expiration_date")
		assert.NotContains(t, body, "memory_type")
		assert.NotContains(t, body, "prompt")
	case <-time.After(2 * time.Second):
		t.Fatal("backend was not hit")
	}
	assert.Equal(t, int32(1), resolverCalls.Load())
}

func TestIngestSession_ExpirationResolverErrorDoesNotAdvanceWatermark(t *testing.T) {
	var requestCalls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wantErr := errors.New("expiration resolver failed")
	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				return time.Time{}, wantErr
			},
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	sess := newIngestTestSession()
	err = svc.IngestSession(context.Background(), sess)
	require.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "resolve ingest expiration date")
	assert.True(t, readLastExtractAt(sess).IsZero())
	assert.Zero(t, requestCalls.Load())
}

func TestIngestSession_InvalidExpirationDateDoesNotAdvanceWatermark(t *testing.T) {
	var requestCalls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				return time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC), nil
			},
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	sess := newIngestTestSession()
	err = svc.IngestSession(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the supported range")
	assert.True(t, readLastExtractAt(sess).IsZero())
	assert.Zero(t, requestCalls.Load())
}

func TestIngestSession_ExpirationResolverSupportsConcurrentCalls(t *testing.T) {
	const sessions = 16
	var resolverCalls atomic.Int32
	var requestCalls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithAsyncMemoryNum(4),
		WithMemoryQueueSize(sessions),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				resolverCalls.Add(1)
				return time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC), nil
			},
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	errCh := make(chan error, sessions)
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- svc.IngestSession(context.Background(), newIngestTestSession())
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	require.NoError(t, svc.Close())
	assert.Equal(t, int32(sessions), resolverCalls.Load())
	assert.Equal(t, int32(sessions), requestCalls.Load())
}

func TestIngestSession_SerializesConcurrentCallsForSameSession(t *testing.T) {
	var requestCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(server.Close)

	resolverEntered := make(chan struct{}, 2)
	releaseResolver := make(chan struct{})
	var resolverCalls atomic.Int32
	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(server.URL),
		WithMemoryQueueSize(2),
		WithSelfHostedIngestExpirationDateResolver(
			func(context.Context, *session.Session) (time.Time, error) {
				resolverCalls.Add(1)
				resolverEntered <- struct{}{}
				<-releaseResolver
				return time.Time{}, nil
			},
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	sess := newIngestTestSession()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- svc.IngestSession(context.Background(), sess)
	}()
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- svc.IngestSession(context.Background(), sess)
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-resolverEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent ingestion did not enter the expiration resolver")
		}
	}

	close(releaseResolver)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.NoError(t, svc.Close())
	assert.Equal(t, int32(2), resolverCalls.Load())
	assert.Equal(t, int32(1), requestCalls.Load())
}

func TestIngestSession_SyncFallbackWhenQueueFull(t *testing.T) {
	// Build a service pointing at a server that returns a quick SUCCEEDED
	// response, then manually stop the async worker so every subsequent
	// IngestSession call is forced onto the synchronous fallback path.
	var ingestCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/memories/" {
			atomic.AddInt32(&ingestCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"x","status":"SUCCEEDED"}]`))
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithAPIKey("k"),
		WithHost(srv.URL),
		WithAsyncMode(false),
		WithAsyncMemoryNum(1),
		WithMemoryQueueSize(1),
		WithMemoryJobTimeout(500*time.Millisecond),
	)
	require.NoError(t, err)
	defer svc.Close()

	// Kill the async worker so tryEnqueue always returns false, forcing
	// every IngestSession call to take the synchronous fallback path.
	svc.ingestWorker.Stop()

	sess := &session.Session{AppName: "app", UserID: "user", ID: "s1"}
	sess.Events = []event.Event{{
		Timestamp: time.Now(),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "hello"},
		}}},
	}}

	require.NoError(t, svc.IngestSession(context.Background(), sess))
	assert.Equal(t, int32(1), atomic.LoadInt32(&ingestCalls),
		"sync fallback must hit the backend exactly once")
}

func TestNewService_RejectsSelfHostedIngestOptionsInCloudMode(t *testing.T) {
	tests := []struct {
		name string
		opt  ServiceOpt
	}{
		{
			name: "prompt",
			opt:  WithSelfHostedIngestPrompt("extract deadlines"),
		},
		{
			name: "expiration date resolver",
			opt: WithSelfHostedIngestExpirationDateResolver(
				func(context.Context, *session.Session) (time.Time, error) {
					return time.Time{}, nil
				},
			),
		},
		{
			name: "procedural memory",
			opt:  WithSelfHostedProceduralMemory(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(WithAPIKey("key"), tt.opt)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "require self-hosted OSS mode")
		})
	}
}

func TestNewService_AllowsInferenceOptionInCloudMode(t *testing.T) {
	svc, err := NewService(WithAPIKey("key"), WithIngestInference(false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
}

func TestNewService_RejectsOptionsIgnoredWhenInferenceDisabled(t *testing.T) {
	tests := []struct {
		name string
		opt  ServiceOpt
		want string
	}{
		{
			name: "custom prompt",
			opt:  WithSelfHostedIngestPrompt("extract deadlines"),
			want: "custom ingest prompt requires inference",
		},
		{
			name: "procedural memory",
			opt:  WithSelfHostedProceduralMemory(),
			want: "procedural memory requires inference",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(
				WithSelfHostedOSS(),
				WithHost("http://localhost:8888"),
				WithIngestInference(false),
				tt.opt,
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestService_ValidateIngestOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    ingestOptions
		wantErr string
	}{
		{
			name:    "unsupported memory type",
			opts:    ingestOptions{infer: true, memoryType: "unsupported"},
			wantErr: "unsupported memory type",
		},
		{
			name:    "procedural memory without inference",
			opts:    ingestOptions{memoryType: memoryTypeProcedural},
			wantErr: "procedural memory requires inference",
		},
		{
			name:    "custom prompt without inference",
			opts:    ingestOptions{prompt: "extract facts"},
			wantErr: "custom ingest prompt requires inference",
		},
		{
			name:    "procedural memory without agent ID",
			opts:    ingestOptions{infer: true, memoryType: memoryTypeProcedural},
			wantErr: "procedural memory requires an agent ID",
		},
		{
			name: "valid procedural memory",
			opts: ingestOptions{
				agentID:    "agent",
				infer:      true,
				memoryType: memoryTypeProcedural,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIngestOptions(tt.opts)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestIngestSession_ProceduralMemoryRequiresAgentBeforeWatermark(t *testing.T) {
	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost("http://localhost:8888"),
		WithSelfHostedProceduralMemory(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	sess := newIngestTestSession()
	err = svc.IngestSession(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires an agent ID")
	assert.True(t, readLastExtractAt(sess).IsZero())
}

func TestResolveSessionIngestOptionsSnapshotsCallerData(t *testing.T) {
	metadata := map[string]any{"nested": map[string]any{"value": "before"}}
	config := ingestConfig{prompt: "extract deadlines", infer: true}
	opts := resolveSessionIngestOptions(config, []session.IngestOption{
		session.WithIngestMetadata(metadata),
		session.WithIngestAgentID("agent-1"),
		session.WithIngestRunID("run-1"),
	})
	metadata["nested"].(map[string]any)["value"] = "after"

	assert.Equal(t, "before", opts.metadata["nested"].(map[string]any)["value"])
	assert.Equal(t, "agent-1", opts.agentID)
	assert.Equal(t, "run-1", opts.runID)
	assert.Equal(t, "extract deadlines", opts.prompt)
	assert.True(t, opts.infer)
}

func newIngestTestSession() *session.Session {
	return &session.Session{
		AppName: "app",
		UserID:  "user",
		ID:      "session",
		Events: []event.Event{{
			Timestamp: time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "hello"},
			}}},
		}},
	}
}

// --- ReadMemories ---

func TestReadMemories_InvalidUserKeyReturnsError(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{}, 10)
	assert.Error(t, err)
}

func TestReadMemories_PaginatesAndSorts(t *testing.T) {
	var calls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		body := `[
			{"id":"a","memory":"first","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-05T00:00:00Z"},
			{"id":"b","memory":"second","created_at":"2024-02-01T00:00:00Z","updated_at":"2024-01-10T00:00:00Z"}
		]`
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	// b.UpdatedAt > a.UpdatedAt → b first.
	assert.Equal(t, "b", entries[0].ID)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(2), "pagination must hit more than one page")
}

func TestReadMemories_AppliesLimit(t *testing.T) {
	var calls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("page") != "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":"a","memory":"one","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"},
			{"id":"b","memory":"two","created_at":"2024-01-02T00:00:00Z","updated_at":"2024-01-02T00:00:00Z"},
			{"id":"c","memory":"three","created_at":"2024-01-03T00:00:00Z","updated_at":"2024-01-03T00:00:00Z"}
		]`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 1)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "limit should stop cloud pagination")
}

func TestReadMemories_StopsOnInvalidPage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Invalid page."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"a","memory":"x"}]`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestReadMemories_BackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()
	_, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, 0)
	assert.Error(t, err)
}

func TestReadMemories_SelfHostedOSSFiltersByAppMetadata(t *testing.T) {
	var gotPath string
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"a","memory":"keep","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-01T00:00:00Z"},
			{"id":"legacy","memory":"drop legacy without metadata","created_at":"2024-01-02T00:00:00Z"},
			{"id":"b","memory":"drop","metadata":{"trpc_app_name":"other"},"created_at":"2024-01-02T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "a", entries[0].ID)
	assert.Equal(t, "/memories", gotPath)
	assert.Contains(t, gotQuery, "user_id=user")
	assert.Contains(t, gotQuery, "top_k=")
}

func TestReadMemories_SelfHostedOSSCanIncludeUnscopedMemories(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/memories", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"app","memory":"keep app","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-03T00:00:00Z"},
			{"id":"legacy-missing","memory":"keep legacy missing","created_at":"2024-01-02T00:00:00Z"},
			{"id":"legacy-non-string","memory":"keep legacy non string","metadata":{"trpc_app_name":123},"created_at":"2024-01-01T00:00:00Z"},
			{"id":"other","memory":"drop other app","metadata":{"trpc_app_name":"other"},"created_at":"2024-01-04T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithSelfHostedOSSIncludeUnscopedMemories(),
	)
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "app", entries[0].ID)
	assert.Equal(t, "legacy-missing", entries[1].ID)
	assert.Equal(t, "legacy-non-string", entries[2].ID)
}

func TestReadMemories_SelfHostedOSSRejectsUnboundedLimit(t *testing.T) {
	svc, err := NewService(WithSelfHostedOSS(), WithHost("http://localhost:8888"))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive limit")

	_, err = svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, maxOSSListTopK+1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestReadMemories_SelfHostedOSSBackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/memories", r.URL.Path)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"detail":"upstream unavailable"}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestReadMemories_SelfHostedOSSSortsLimitsAndSkipsInvalidRecords(t *testing.T) {
	var gotQuery string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		assert.Equal(t, "/memories", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"older-update","memory":"older update","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-05T00:00:00Z","updated_at":"2024-01-06T00:00:00Z"},
			{"id":"newer-update","memory":"newer update","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-07T00:00:00Z"},
			{"id":"same-update-newer-created","memory":"same update newer created","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-06T00:00:00Z","updated_at":"2024-01-06T00:00:00Z"},
			{"id":"wrong-app","memory":"drop wrong app","metadata":{"trpc_app_name":"other"},"created_at":"2024-01-08T00:00:00Z","updated_at":"2024-01-08T00:00:00Z"},
			{"id":"","memory":"drop empty id","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-09T00:00:00Z","updated_at":"2024-01-09T00:00:00Z"},
			{"id":"empty-memory","memory":"","metadata":{"trpc_app_name":"app"},"created_at":"2024-01-10T00:00:00Z","updated_at":"2024-01-10T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.ReadMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "newer-update", entries[0].ID)
	assert.Equal(t, "same-update-newer-created", entries[1].ID)
	assert.Contains(t, gotQuery, "user_id=user")
	assert.Contains(t, gotQuery, "top_k=1000")
}

// --- SearchMemories ---

func TestSearchMemories_InvalidUserKey(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	calls := 0
	option := memory.SearchOption(func(*memory.SearchOptions) {
		calls++
	})
	_, err := svc.SearchMemories(context.Background(), memory.UserKey{}, "q", option)
	assert.Error(t, err)
	assert.Zero(t, calls)
}

func TestSearchMemories_EmptyQueryReturnsEmpty(t *testing.T) {
	svc, _ := NewService(WithAPIKey("k"))
	defer svc.Close()
	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "   ")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSearchMemories_ReturnsSortedMatches(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v2/memories/search/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[
			{"id":"a","memory":"low","score":0.1,"created_at":"2024-01-01T00:00:00Z"},
			{"id":"b","memory":"high","score":0.9,"created_at":"2024-01-02T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL), WithOrgProject("o", "p"))
	defer svc.Close()

	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "q")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "b", got[0].ID, "higher score should come first")
}

func TestSearchMemories_AppliesMaxResultsAndSimilarity(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[
			{"id":"a","memory":"low","score":0.1,"created_at":"2024-01-01T00:00:00Z"},
			{"id":"b","memory":"mid","score":0.5,"created_at":"2024-01-02T00:00:00Z"},
			{"id":"c","memory":"high","score":0.9,"created_at":"2024-01-03T00:00:00Z"}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()

	// SimilarityThreshold=0.3 drops "a", MaxResults=1 keeps only "c".
	opts := memory.SearchOptions{
		Query:               "q",
		MaxResults:          1,
		SimilarityThreshold: 0.3,
	}
	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "q",
		memory.WithSearchOptions(opts),
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "c", got[0].ID)
}

func TestSearchMemories_BackendErrorPropagates(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, _ := NewService(WithAPIKey("k"), WithHost(srv.URL))
	defer svc.Close()
	_, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Error(t, err)
}

func TestSearchMemories_SelfHostedOSSUsesMetadataFilter(t *testing.T) {
	var gotReq searchV2Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if !decodeTestJSONRequest(w, r, &gotReq) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"keep","memory":"match","metadata":{"trpc_app_name":"app"},"score":0.9},
			{"id":"drop","memory":"wrong app","metadata":{"trpc_app_name":"other"},"score":1}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL), WithAPIKey("oss-key"))
	require.NoError(t, err)
	defer svc.Close()

	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, "query")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "keep", got[0].ID)
	assert.Equal(t, "query", gotReq.Query)
	assert.Equal(t, map[string]any{
		queryKeyUserID:         "user",
		metadataKeyTRPCAppName: "app",
	}, gotReq.Filters)
}

func TestSearchMemories_SelfHostedOSSCanIncludeUnscopedMemories(t *testing.T) {
	var gotReq searchV2Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if !decodeTestJSONRequest(w, r, &gotReq) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[
			{"id":"app","memory":"keep app","metadata":{"trpc_app_name":"app"},"score":0.9},
			{"id":"legacy","memory":"keep legacy","score":0.8},
			{"id":"other","memory":"drop other app","metadata":{"trpc_app_name":"other"},"score":1}
		]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	svc, err := NewService(
		WithSelfHostedOSS(),
		WithHost(srv.URL),
		WithAPIKey("oss-key"),
		WithSelfHostedOSSIncludeUnscopedMemories(),
	)
	require.NoError(t, err)
	defer svc.Close()

	got, err := svc.SearchMemories(context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"}, "query")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "app", got[0].ID)
	assert.Equal(t, "legacy", got[1].ID)
	assert.Equal(t, map[string]any{queryKeyUserID: "user"}, gotReq.Filters)
}

func TestSearchMemories_SelfHostedOSSForwardsThreshold(t *testing.T) {
	var gotReq searchV2Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !decodeTestJSONRequest(w, r, &gotReq) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{
			"id":"memory-1",
			"memory":"match",
			"metadata":{"trpc_app_name":"app","custom":"value"},
			"score":0.9
		}]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, err := NewService(WithSelfHostedOSS(), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	entries, err := svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:               "query",
			SimilarityThreshold: 0.42,
		}),
	)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "user", gotReq.Filters[queryKeyUserID])
	assert.Equal(t, "app", gotReq.Filters[metadataKeyTRPCAppName])
	require.NotNil(t, gotReq.Threshold)
	assert.InDelta(t, 0.42, *gotReq.Threshold, 1e-9)
	assert.Equal(t, "memory-1", entries[0].ID)
	assert.InDelta(t, 0.9, entries[0].Score, 1e-9)
	assert.Equal(t, "match", entries[0].Memory.Memory)
}

func TestSearchMemories_CloudDoesNotForwardSelfHostedThreshold(t *testing.T) {
	var gotReq map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !decodeTestJSONRequest(w, r, &gotReq) {
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"memories":[]}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	svc, err := NewService(WithAPIKey("key"), WithHost(srv.URL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.SearchMemories(
		context.Background(),
		memory.UserKey{AppName: "app", UserID: "user"},
		"query",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:               "query",
			SimilarityThreshold: 0.42,
		}),
	)
	require.NoError(t, err)
	assert.NotContains(t, gotReq, "threshold")
}

func decodeTestJSONRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}
