//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewModelRouter_ValidatesCandidates(t *testing.T) {
	t.Parallel()

	_, err := NewModelRouter("https://decision.example")
	require.ErrorContains(t, err, "WithModelCandidate")

	fast := &stubModel{name: "fast"}
	_, err = NewModelRouter("https://decision.example",
		WithModelCandidate("fast", fast, "cheap tasks"),
		WithModelCandidate("fast", fast, "cheap tasks"),
	)
	require.ErrorContains(t, err, "duplicate")

	_, err = NewModelRouter("https://decision.example",
		WithModelCandidate("  ", fast, "cheap tasks"),
	)
	require.ErrorContains(t, err, "blank")

	_, err = NewModelRouter("https://decision.example",
		WithModelCandidate("fast", nil, "cheap tasks"),
	)
	require.ErrorContains(t, err, "non-nil")

	_, err = NewModelRouter("")
	require.Error(t, err)
}

func newRoutingEnv(
	t *testing.T,
	status int,
	respond func(map[string]string) string,
	opts ...Option,
) (*ModelRouter, *decisionRecorder) {
	t.Helper()

	server, rec := decisionServer(t, respond, status)
	baseOpts := append([]Option{WithHTTPClient(server.Client())}, opts...)
	router, err := NewModelRouter(server.URL, baseOpts...)
	require.NoError(t, err)
	return router, rec
}

func routingInv() *agent.Invocation {
	return agent.NewInvocation(agent.WithInvocationMessage(
		model.NewUserMessage("review this long legal contract"),
	))
}

// choiceResponder answers the routing question with a fixed two-option
// distribution; choice names outside the requested options are rejected by
// the wire validator, which tests the fail-open path.
func choiceResponder(choice string) func(map[string]string) string {
	return func(map[string]string) string {
		return choiceResponse("model_route", choice,
			map[string]float64{"fast": 0.1, "smart": 0.9}, 0.9)
	}
}

func TestSelect_RoutesToChosenCandidate(t *testing.T) {
	t.Parallel()

	smart := &stubModel{name: "smart"}
	router, rec := newRoutingEnv(t, http.StatusOK, choiceResponder("smart"),
		WithModelCandidate("fast", &stubModel{name: "fast"}, "cheap tasks"),
		WithModelCandidate("smart", smart, "complex reasoning"),
	)

	inv := routingInv()
	m, err := router.Select(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, smart, m)
	require.Equal(t, 1, rec.requests())

	// Cached per invocation: a ReAct loop routes once per run.
	m, err = router.Select(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, smart, m)
	require.Equal(t, 1, rec.requests())
}

func TestSelect_ConcurrentWaiterCanCancel(t *testing.T) {
	started, release := make(chan struct{}, 8), make(chan struct{}, 1)
	defer close(release)
	smart := &stubModel{name: "smart"}
	fallback := &stubModel{name: "fallback"}
	router, rec := newRoutingEnv(t, http.StatusOK, func(q map[string]string) string {
		started <- struct{}{}
		<-release
		return choiceResponder("smart")(q)
	}, WithModelCandidate("fast", &stubModel{name: "fast"}, "simple"),
		WithModelCandidate("smart", smart, "reasoning"), WithFallbackModel(fallback))
	inv := routingInv()
	done := make(chan model.Model, 1)
	go func() { m, _ := router.Select(context.Background(), inv); done <- m }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("decision did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan model.Model, 1)
	go func() { m, _ := router.Select(ctx, inv); waiter <- m }()
	select {
	case <-started:
		t.Fatal("waiter started a duplicate decision")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case m := <-waiter:
		require.Same(t, fallback, m)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter did not return")
	}
	require.Equal(t, 1, rec.requests())
	release <- struct{}{}
	select {
	case m := <-done:
		require.Same(t, smart, m)
	case <-time.After(2 * time.Second):
		t.Fatal("decision did not finish")
	}
	m, err := router.Select(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, smart, m)
	require.Equal(t, 1, rec.requests())
}

func TestSelect_FailOpenToFallback(t *testing.T) {
	t.Parallel()

	fallback := &stubModel{name: "fallback"}
	router, rec := newRoutingEnv(t, http.StatusInternalServerError, nil,
		WithModelCandidate("fast", &stubModel{name: "fast"}, "cheap tasks"),
		WithModelCandidate("smart", &stubModel{name: "smart"}, "reasoning"),
		WithFallbackModel(fallback),
	)

	inv := routingInv()
	m, err := router.Select(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, fallback, m)
	require.Equal(t, 1, rec.requests())

	// The fail-open result is cached for the rest of the run.
	m, err = router.Select(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, fallback, m)
	require.Equal(t, 1, rec.requests())
}

func TestSelect_FailOpenKeepsBaseModelWithoutFallback(t *testing.T) {
	t.Parallel()

	router, _ := newRoutingEnv(t, http.StatusInternalServerError, nil,
		WithModelCandidate("fast", &stubModel{name: "fast"}, "cheap tasks"),
	)

	// A nil model with a nil error keeps the invocation's base model.
	m, err := router.Select(context.Background(), routingInv())
	require.NoError(t, err)
	require.Nil(t, m)
}

func TestSelect_BlankMessageFallsBack(t *testing.T) {
	t.Parallel()

	fallback := &stubModel{name: "fallback"}
	router, rec := newRoutingEnv(t, http.StatusOK, choiceResponder("fast"),
		WithModelCandidate("fast", &stubModel{name: "fast"}, "cheap tasks"),
		WithFallbackModel(fallback),
	)

	inv := agent.NewInvocation() // no user message
	m, err := router.Select(context.Background(), inv)
	require.NoError(t, err)
	require.Same(t, fallback, m)
	require.Equal(t, 0, rec.requests())
}

func TestSelect_InvalidAnswerFallsBack(t *testing.T) {
	t.Parallel()

	fallback := &stubModel{name: "fallback"}
	router, _ := newRoutingEnv(t, http.StatusOK, choiceResponder("unknown-choice"),
		WithModelCandidate("fast", &stubModel{name: "fast"}, "cheap tasks"),
		WithFallbackModel(fallback),
	)

	// The server validator rejects choices that were not requested, so
	// the answer decodes to an error and routing fails open.
	m, err := router.Select(context.Background(), routingInv())
	require.NoError(t, err)
	require.Same(t, fallback, m)
}
