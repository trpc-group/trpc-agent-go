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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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

type failingWriter struct{}

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

func (failingWriter) Header() http.Header {
	return http.Header{}
}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func (failingWriter) WriteHeader(statusCode int) {
	_ = statusCode
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

func TestHandleRunsSupportsOptions(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, srv.RunsPath(), nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "*", recorder.Header().Get(headerAccessControlOrigin))
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

func TestHandleAsyncRunsRejectsInvalidMethodWhenManagerIsConfigured(t *testing.T) {
	srv := newTestServer(t,
		WithManager(&fakeManager{}),
	)
	req := httptest.NewRequest(http.MethodGet, srv.AsyncRunsPath(), nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, http.MethodPost, recorder.Header().Get(headerAllow))
}

func TestHandleAsyncRunsSupportsOptionsAndMapsStartErrors(t *testing.T) {
	srv := newTestServer(t,
		WithManager(&fakeManager{
			start: func(ctx context.Context, request *enginepkg.RunRequest) (*enginepkg.RunResult, error) {
				_ = ctx
				_ = request
				return nil, context.Canceled
			},
		}),
	)
	optionsReq := httptest.NewRequest(http.MethodOptions, srv.AsyncRunsPath(), nil)
	optionsRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(optionsRecorder, optionsReq)
	require.Equal(t, http.StatusNoContent, optionsRecorder.Code)
	body, err := json.Marshal(&RunRequest{
		Run: &enginepkg.RunRequest{
			TrainEvalSetIDs:      []string{"train"},
			ValidationEvalSetIDs: []string{"validation"},
			TargetSurfaceIDs:     []string{"candidate#instruction"},
			MaxRounds:            1,
		},
	})
	require.NoError(t, err)
	postReq := httptest.NewRequest(http.MethodPost, srv.AsyncRunsPath(), bytes.NewReader(body))
	postRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postRecorder, postReq)
	require.Equal(t, http.StatusRequestTimeout, postRecorder.Code)
	assert.Contains(t, postRecorder.Body.String(), context.Canceled.Error())
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

func TestHandleRunResourceSupportsOptions(t *testing.T) {
	srv := newTestServer(t,
		WithManager(&fakeManager{}),
	)
	req := httptest.NewRequest(http.MethodOptions, srv.AsyncRunsPath()+"/run_1/cancel", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "*", recorder.Header().Get(headerAccessControlOrigin))
}

func TestHandleStructureSupportsOptionsAndRejectsInvalidMethod(t *testing.T) {
	srv := newTestServer(t)
	optionsReq := httptest.NewRequest(http.MethodOptions, srv.StructurePath(), nil)
	optionsRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(optionsRecorder, optionsReq)
	require.Equal(t, http.StatusNoContent, optionsRecorder.Code)
	assert.Equal(t, "*", optionsRecorder.Header().Get(headerAccessControlOrigin))
	postReq := httptest.NewRequest(http.MethodPost, srv.StructurePath(), nil)
	postRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postRecorder, postReq)
	require.Equal(t, http.StatusMethodNotAllowed, postRecorder.Code)
	assert.Equal(t, http.MethodGet, postRecorder.Header().Get(headerAllow))
	assert.Contains(t, postRecorder.Body.String(), "method not allowed")
}

func TestHandleStructureMapsDescribeErrors(t *testing.T) {
	srv := newTestServer(t,
		WithEngine(&fakeEngine{
			describe: func(ctx context.Context) (*astructure.Snapshot, error) {
				_ = ctx
				return nil, context.DeadlineExceeded
			},
		}),
	)
	req := httptest.NewRequest(http.MethodGet, srv.StructurePath(), nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	assert.Contains(t, recorder.Body.String(), context.DeadlineExceeded.Error())
}

func TestHandleRunsMethodAndExecutionErrors(t *testing.T) {
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
				_ = request
				_ = opts
				return nil, context.Canceled
			},
		}),
	)
	getReq := httptest.NewRequest(http.MethodGet, srv.RunsPath(), nil)
	getRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRecorder, getReq)
	require.Equal(t, http.StatusMethodNotAllowed, getRecorder.Code)
	assert.Equal(t, http.MethodPost, getRecorder.Header().Get(headerAllow))
	body, err := json.Marshal(&RunRequest{
		Run: &enginepkg.RunRequest{
			TrainEvalSetIDs:      []string{"train"},
			ValidationEvalSetIDs: []string{"validation"},
			TargetSurfaceIDs:     []string{"candidate#instruction"},
			MaxRounds:            1,
		},
	})
	require.NoError(t, err)
	postReq := httptest.NewRequest(http.MethodPost, srv.RunsPath(), bytes.NewReader(body))
	postRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postRecorder, postReq)
	require.Equal(t, http.StatusRequestTimeout, postRecorder.Code)
	assert.Contains(t, postRecorder.Body.String(), context.Canceled.Error())
}

func TestHandleRunResourceNotFoundAndManagerErrors(t *testing.T) {
	srv := newTestServer(t,
		WithManager(&fakeManager{
			get: func(ctx context.Context, runID string) (*enginepkg.RunResult, error) {
				_ = ctx
				_ = runID
				return nil, os.ErrNotExist
			},
			cancel: func(ctx context.Context, runID string) error {
				_ = ctx
				_ = runID
				return context.DeadlineExceeded
			},
		}),
	)
	notFoundReq := httptest.NewRequest(http.MethodGet, srv.AsyncRunsPath()+"/run_1/extra", nil)
	notFoundRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(notFoundRecorder, notFoundReq)
	require.Equal(t, http.StatusNotFound, notFoundRecorder.Code)
	getReq := httptest.NewRequest(http.MethodGet, srv.AsyncRunsPath()+"/run_1", nil)
	getRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRecorder, getReq)
	require.Equal(t, http.StatusNotFound, getRecorder.Code)
	cancelReq := httptest.NewRequest(http.MethodPost, srv.AsyncRunsPath()+"/run_1/cancel", nil)
	cancelRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRecorder, cancelReq)
	require.Equal(t, http.StatusGatewayTimeout, cancelRecorder.Code)
	assert.Contains(t, cancelRecorder.Body.String(), context.DeadlineExceeded.Error())
	missingReq := httptest.NewRequest(http.MethodGet, srv.AsyncRunsPath()+"/run_1/unknown", nil)
	missingRecorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(missingRecorder, missingReq)
	require.Equal(t, http.StatusNotFound, missingRecorder.Code)
}

func TestRedirectTrailingSlashToCanonicalPath(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, srv.RunsPath()+"/?a=1", nil)
	recorder := httptest.NewRecorder()
	srv.redirectTrailingSlashToCanonicalPath(recorder, req)
	require.Equal(t, http.StatusPermanentRedirect, recorder.Code)
	assert.Equal(t, srv.RunsPath()+"?a=1", recorder.Header().Get("Location"))
}

func TestValidateTargetSurfaceIDs(t *testing.T) {
	srv := newTestServer(t)
	assert.NoError(t, srv.validateTargetSurfaceIDs(context.Background(), nil))
	assert.EqualError(t, srv.validateTargetSurfaceIDs(context.Background(), []string{}), "target surface ids must not be empty")
	assert.EqualError(t, srv.validateTargetSurfaceIDs(context.Background(), []string{""}), "target surface ids must not contain empty values")
	err := srv.validateTargetSurfaceIDs(context.Background(), []string{"unknown#instruction"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `target surface id "unknown#instruction" is unknown`)
	assert.NoError(t, srv.validateTargetSurfaceIDs(context.Background(), []string{"candidate#instruction"}))
	nilStructureServer := newTestServer(t,
		WithEngine(&fakeEngine{
			describe: func(ctx context.Context) (*astructure.Snapshot, error) {
				_ = ctx
				return nil, nil
			},
		}),
	)
	assert.EqualError(t, nilStructureServer.validateTargetSurfaceIDs(context.Background(), []string{"candidate#instruction"}), "structure is nil")
	describeErrServer := newTestServer(t,
		WithEngine(&fakeEngine{
			describe: func(ctx context.Context) (*astructure.Snapshot, error) {
				_ = ctx
				return nil, errors.New("boom")
			},
		}),
	)
	assert.EqualError(t, describeErrServer.validateTargetSurfaceIDs(context.Background(), []string{"candidate#instruction"}), "describe structure for target surface validation: boom")
	unsupportedSurfaceServer := newTestServer(t,
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
							SurfaceID: "candidate#unsupported",
							NodeID:    "node_1",
							Type:      astructure.SurfaceType("unsupported"),
						},
					},
				}, nil
			},
		}),
	)
	err = unsupportedSurfaceServer.validateTargetSurfaceIDs(context.Background(), []string{"candidate#unsupported"})
	assert.EqualError(t, err, `target surface id "candidate#unsupported" is unknown`)
}

func TestStatusCodeFromError(t *testing.T) {
	assert.Equal(t, http.StatusGatewayTimeout, statusCodeFromError(context.DeadlineExceeded))
	assert.Equal(t, http.StatusRequestTimeout, statusCodeFromError(context.Canceled))
	assert.Equal(t, http.StatusNotFound, statusCodeFromError(os.ErrNotExist))
	assert.Equal(t, http.StatusInternalServerError, statusCodeFromError(errors.New("boom")))
}

func TestServerHelpers(t *testing.T) {
	srv := newTestServer(t, WithTimeout(3*time.Second))
	assert.Equal(t, 3*time.Second, srv.timeout)
	assert.Equal(t, "/promptiter", normalizeBasePath("/promptiter/"))
	assert.Equal(t, "/promptiter", normalizeBasePath("promptiter"))
	assert.Equal(t, defaultBasePath, normalizeBasePath("   "))
	assert.NoError(t, srv.Close())
}

func TestRespondJSONWritesPayloadAndHandlesEncodeError(t *testing.T) {
	srv := newTestServer(t)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
	srv.respondJSON(recorder, req, http.StatusCreated, map[string]string{"status": "ok"})
	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, contentTypeJSON, recorder.Header().Get(headerContentType))
	assert.JSONEq(t, `{"status":"ok"}`, recorder.Body.String())
	fallbackRecorder := httptest.NewRecorder()
	srv.respondJSON(fallbackRecorder, req, http.StatusOK, map[string]any{"status": make(chan int)})
	require.Equal(t, http.StatusInternalServerError, fallbackRecorder.Code)
	assert.Equal(t, contentTypeJSON, fallbackRecorder.Header().Get(headerContentType))
	assert.Contains(t, fallbackRecorder.Body.String(), "encode response")
	srv.respondJSON(failingWriter{}, req, http.StatusOK, map[string]string{"status": "ok"})
}

func TestNewExecutionContext(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	derived, derivedCancel := newExecutionContext(parent, time.Second)
	defer derivedCancel()
	deadline, ok := derived.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(20*time.Millisecond), deadline, 20*time.Millisecond)
	noTimeoutCtx, noTimeoutCancel := newExecutionContext(context.Background(), 0)
	defer noTimeoutCancel()
	_, ok = noTimeoutCtx.Deadline()
	assert.False(t, ok)
}

func TestDecodeJSONBodyRejectsInvalidPayloads(t *testing.T) {
	respondCalls := make([]int, 0, 2)
	respond := func(w http.ResponseWriter, r *http.Request, statusCode int, payload any) {
		_ = w
		_ = r
		_ = payload
		respondCalls = append(respondCalls, statusCode)
	}
	invalidReq := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString("{"))
	_, err := decodeJSONBody[RunRequest](httptest.NewRecorder(), invalidReq, respond)
	assert.Error(t, err)
	extraReq := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{} {}`))
	_, err = decodeJSONBody[RunRequest](httptest.NewRecorder(), extraReq, respond)
	assert.Error(t, err)
	assert.Equal(t, []int{http.StatusBadRequest, http.StatusBadRequest}, respondCalls)
}

func TestValidateRunRequest(t *testing.T) {
	assert.EqualError(t, validateRunRequest(nil), "request must not be nil")
	assert.EqualError(t, validateRunRequest(&RunRequest{}), "run must not be nil")
	assert.NoError(t, validateRunRequest(&RunRequest{Run: &enginepkg.RunRequest{}}))
}

var _ managerpkg.Manager = (*fakeManager)(nil)
