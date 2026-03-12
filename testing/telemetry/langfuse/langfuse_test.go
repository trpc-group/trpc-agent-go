//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"context"
	"log"
	"os"
	"os/signal"
	"testing"

	baselangfuse "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartWithConfigAndCloseIsIdempotent(t *testing.T) {
	resetGlobals(t)

	var startCalls int
	var closeCalls int
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error {
			closeCalls++
			return nil
		}, nil
	}

	require.NoError(t, startWithConfig(config{}))
	require.NoError(t, Close(context.Background()))
	require.NoError(t, Close(context.Background()))
	assert.Equal(t, 1, startCalls)
	assert.Equal(t, 1, closeCalls)
}

func TestAutoStartStartsWhenEnabled(t *testing.T) {
	resetGlobals(t)
	t.Setenv(envTelemetryEnabled, "true")

	var startCalls int
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error { return nil }, nil
	}

	autoStart()
	assert.Equal(t, 1, startCalls)
	require.NoError(t, Close(context.Background()))
}

func TestAutoStartSkipsWhenDisabled(t *testing.T) {
	resetGlobals(t)
	t.Setenv(envTelemetryEnabled, "false")

	var startCalls int
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error { return nil }, nil
	}

	autoStart()
	assert.Equal(t, 0, startCalls)
}

func resetGlobals(t *testing.T) {
	t.Helper()
	_ = Close(context.Background())
	stateMu.Lock()
	state = runtimeState{}
	stateMu.Unlock()
	startLangfuse = baselangfuse.Start
	logf = func(string, ...any) {}
	notifySignals = func(chan<- os.Signal, ...os.Signal) {}
	stopSignals = func(chan<- os.Signal) {}
	reemitSignal = func(os.Signal) {}
	t.Cleanup(func() {
		_ = Close(context.Background())
		stateMu.Lock()
		state = runtimeState{}
		stateMu.Unlock()
		startLangfuse = baselangfuse.Start
		logf = log.Printf
		notifySignals = signal.Notify
		stopSignals = signal.Stop
		reemitSignal = defaultReemitSignal
	})
}
