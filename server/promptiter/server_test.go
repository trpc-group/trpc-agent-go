//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	enginepkg "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	managerpkg "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
)

type fakeEngine struct {
	describe func(ctx context.Context) (*astructure.Snapshot, error)
	run      func(ctx context.Context, request *enginepkg.RunRequest, opts ...enginepkg.Option) (*enginepkg.RunResult, error)
}

func (f *fakeEngine) Describe(ctx context.Context) (*astructure.Snapshot, error) {
	if f.describe != nil {
		return f.describe(ctx)
	}
	return nil, errors.New("describe is not configured")
}

func (f *fakeEngine) Run(
	ctx context.Context,
	request *enginepkg.RunRequest,
	opts ...enginepkg.Option,
) (*enginepkg.RunResult, error) {
	if f.run != nil {
		return f.run(ctx, request, opts...)
	}
	return nil, errors.New("run is not configured")
}

type fakeManager struct {
	start  func(ctx context.Context, request *enginepkg.RunRequest) (*enginepkg.RunResult, error)
	get    func(ctx context.Context, runID string) (*enginepkg.RunResult, error)
	cancel func(ctx context.Context, runID string) error
}

func (f *fakeManager) Start(ctx context.Context, request *enginepkg.RunRequest) (*enginepkg.RunResult, error) {
	if f.start != nil {
		return f.start(ctx, request)
	}
	return nil, errors.New("start is not configured")
}

func (f *fakeManager) Get(ctx context.Context, runID string) (*enginepkg.RunResult, error) {
	if f.get != nil {
		return f.get(ctx, runID)
	}
	return nil, errors.New("get is not configured")
}

func (f *fakeManager) Cancel(ctx context.Context, runID string) error {
	if f.cancel != nil {
		return f.cancel(ctx, runID)
	}
	return errors.New("cancel is not configured")
}

func (f *fakeManager) Close() error {
	return nil
}

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	baseOpts := []Option{
		WithAppName("demo-app"),
		WithEngine(&fakeEngine{
			describe: func(ctx context.Context) (*astructure.Snapshot, error) {
				_ = ctx
				return &astructure.Snapshot{
					StructureID: "struct_1",
					EntryNodeID: "node_1",
					Nodes: []astructure.Node{
						{NodeID: "node_1", Name: "candidate", Kind: astructure.NodeKindLLM},
					},
					Surfaces: []astructure.Surface{
						{
							SurfaceID: "candidate#instruction",
							NodeID:    "node_1",
							Type:      astructure.SurfaceTypeInstruction,
						},
					},
				}, nil
			},
			run: func(ctx context.Context, request *enginepkg.RunRequest, opts ...enginepkg.Option) (*enginepkg.RunResult, error) {
				_ = ctx
				_ = request
				_ = opts
				return &enginepkg.RunResult{Status: enginepkg.RunStatusSucceeded}, nil
			},
		}),
	}
	baseOpts = append(baseOpts, opts...)
	srv, err := New(baseOpts...)
	require.NoError(t, err)
	return srv
}

func TestNewValidation(t *testing.T) {
	_, err := New(WithAppName("demo-app"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine")
	_, err = New(WithEngine(&fakeEngine{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app name")
}

func TestDefaultAndCustomPaths(t *testing.T) {
	defaultServer := newTestServer(t)
	assert.Equal(t, "/promptiter/v1/apps/demo-app", defaultServer.BasePath())
	assert.Equal(t, "/promptiter/v1/apps/demo-app/structure", defaultServer.StructurePath())
	assert.Equal(t, "/promptiter/v1/apps/demo-app/runs", defaultServer.RunsPath())
	assert.Equal(t, "/promptiter/v1/apps/demo-app/async-runs", defaultServer.AsyncRunsPath())
	customServer := newTestServer(t,
		WithBasePath("/api/promptiter"),
		WithStructurePath("/describe"),
		WithRunsPath("/executions"),
		WithAsyncRunsPath("/background-executions"),
	)
	assert.Equal(t, "/api/promptiter/demo-app", customServer.BasePath())
	assert.Equal(t, "/api/promptiter/demo-app/describe", customServer.StructurePath())
	assert.Equal(t, "/api/promptiter/demo-app/executions", customServer.RunsPath())
	assert.Equal(t, "/api/promptiter/demo-app/background-executions", customServer.AsyncRunsPath())
}

func TestHandleStructure(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, srv.StructurePath(), nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	var resp GetStructureResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.NotNil(t, resp.Structure)
	assert.Equal(t, "struct_1", resp.Structure.StructureID)
}

func TestHandleRunsPassesRequestThrough(t *testing.T) {
	var captured *enginepkg.RunRequest
	srv := newTestServer(t,
		WithEngine(&fakeEngine{
			describe: func(ctx context.Context) (*astructure.Snapshot, error) {
				_ = ctx
				return &astructure.Snapshot{
					StructureID: "struct_1",
					EntryNodeID: "node_1",
					Nodes: []astructure.Node{
						{NodeID: "node_1", Name: "candidate", Kind: astructure.NodeKindLLM},
					},
					Surfaces: []astructure.Surface{
						{
							SurfaceID: "candidate#instruction",
							NodeID:    "node_1",
							Type:      astructure.SurfaceTypeInstruction,
						},
					},
				}, nil
			},
			run: func(ctx context.Context, request *enginepkg.RunRequest, opts ...enginepkg.Option) (*enginepkg.RunResult, error) {
				_ = ctx
				captured = request
				_ = opts
				return &enginepkg.RunResult{
					Structure: &astructure.Snapshot{StructureID: "struct_1", EntryNodeID: "node_1"},
					Status:    enginepkg.RunStatusSucceeded,
				}, nil
			},
		}),
	)
	body, err := json.Marshal(&RunRequest{
		Run: &enginepkg.RunRequest{
			TrainEvalSetIDs:      []string{"train"},
			ValidationEvalSetIDs: []string{"validation"},
			TargetSurfaceIDs:     []string{"candidate#instruction"},
			MaxRounds:            1,
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, captured)
	assert.Equal(t, []string{"train"}, captured.TrainEvalSetIDs)
	assert.Equal(t, []string{"validation"}, captured.ValidationEvalSetIDs)
	assert.Equal(t, []string{"candidate#instruction"}, captured.TargetSurfaceIDs)
	assert.Equal(t, 1, captured.MaxRounds)
	var resp RunResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.NotNil(t, resp.Result)
	assert.Equal(t, enginepkg.RunStatusSucceeded, resp.Result.Status)
}

func TestHandleRunsRejectsInvalidRequest(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewBufferString(`{"run":null}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "run must not be nil")
}

func TestHandleRunsRejectsUnknownTargetSurfaceID(t *testing.T) {
	srv := newTestServer(t)
	body, err := json.Marshal(&RunRequest{
		Run: &enginepkg.RunRequest{
			TrainEvalSetIDs:      []string{"train"},
			ValidationEvalSetIDs: []string{"validation"},
			TargetSurfaceIDs:     []string{"unknown#instruction"},
			MaxRounds:            1,
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "target surface id")
	assert.Contains(t, recorder.Body.String(), "unknown")
}

func TestHandleAsyncRunsIsNotExposedWithoutManager(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, srv.AsyncRunsPath(), bytes.NewBufferString(`{"run":null}`))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestHandleAsyncRunsStartsRunWhenManagerIsConfigured(t *testing.T) {
	var captured *enginepkg.RunRequest
	srv := newTestServer(t,
		WithManager(&fakeManager{
			start: func(ctx context.Context, request *enginepkg.RunRequest) (*enginepkg.RunResult, error) {
				_ = ctx
				captured = request
				return &enginepkg.RunResult{
					ID:     "run_1",
					Status: enginepkg.RunStatusQueued,
				}, nil
			},
		}),
	)
	body, err := json.Marshal(&RunRequest{
		Run: &enginepkg.RunRequest{
			TrainEvalSetIDs:      []string{"train"},
			ValidationEvalSetIDs: []string{"validation"},
			TargetSurfaceIDs:     []string{"candidate#instruction"},
			MaxRounds:            1,
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, srv.AsyncRunsPath(), bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.NotNil(t, captured)
	assert.Equal(t, []string{"train"}, captured.TrainEvalSetIDs)
	assert.Equal(t, "/promptiter/v1/apps/demo-app/async-runs/run_1", recorder.Header().Get("Location"))
	var resp RunResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.NotNil(t, resp.Result)
	assert.Equal(t, "run_1", resp.Result.ID)
	assert.Equal(t, enginepkg.RunStatusQueued, resp.Result.Status)
}

func TestHandleGetRunReturnsRun(t *testing.T) {
	srv := newTestServer(t,
		WithManager(&fakeManager{
			get: func(ctx context.Context, runID string) (*enginepkg.RunResult, error) {
				_ = ctx
				return &enginepkg.RunResult{
					ID:           runID,
					Status:       enginepkg.RunStatusRunning,
					CurrentRound: 2,
				}, nil
			},
		}),
	)
	req := httptest.NewRequest(http.MethodGet, srv.AsyncRunsPath()+"/run_1", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	var resp RunResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.NotNil(t, resp.Result)
	assert.Equal(t, "run_1", resp.Result.ID)
	assert.Equal(t, enginepkg.RunStatusRunning, resp.Result.Status)
	assert.Equal(t, 2, resp.Result.CurrentRound)
}

func TestHandleCancelRunCancelsAsyncRun(t *testing.T) {
	var capturedRunID string
	srv := newTestServer(t,
		WithManager(&fakeManager{
			cancel: func(ctx context.Context, runID string) error {
				_ = ctx
				capturedRunID = runID
				return nil
			},
		}),
	)
	req := httptest.NewRequest(http.MethodPost, srv.AsyncRunsPath()+"/run_1/cancel", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	assert.Equal(t, "run_1", capturedRunID)
}

var _ managerpkg.Manager = (*fakeManager)(nil)
