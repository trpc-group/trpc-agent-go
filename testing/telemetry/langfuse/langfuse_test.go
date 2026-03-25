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
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
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

	require.NoError(t, startWithConfig(langfuseConfig{}))
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

func TestAutoStartLogsResolveConfigError(t *testing.T) {
	resetGlobals(t)
	t.Setenv(envTelemetryEnabled, "not-a-bool")

	var logged string
	var startCalls int
	logf = func(format string, args ...any) {
		logged = fmt.Sprintf(format, args...)
	}
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error { return nil }, nil
	}

	autoStart()
	assert.Equal(t, 0, startCalls)
	assert.Contains(t, logged, "parse "+envTelemetryEnabled)
}

func TestAutoStartLogsStartError(t *testing.T) {
	resetGlobals(t)
	t.Setenv(envTelemetryEnabled, "true")

	sentinel := errors.New("start failed")
	var logged string
	logf = func(format string, args ...any) {
		logged = fmt.Sprintf(format, args...)
	}
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		return nil, sentinel
	}

	autoStart()
	assert.Contains(t, logged, sentinel.Error())
}

func TestStartWithConfigSkipsWhenAlreadyStarted(t *testing.T) {
	resetGlobals(t)

	stateMu.Lock()
	state.cleanup = func(context.Context) error { return nil }
	stateMu.Unlock()

	var startCalls int
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error { return nil }, nil
	}

	require.NoError(t, startWithConfig(langfuseConfig{}))
	assert.Equal(t, 0, startCalls)
}

func TestStartWithConfigReturnsErrorWhenOptionsFail(t *testing.T) {
	resetGlobals(t)

	var startCalls int
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error { return nil }, nil
	}

	err := startWithConfig(langfuseConfig{Processor: "invalid"})
	require.Error(t, err)
	assert.Equal(t, 0, startCalls)
}

func TestStartWithConfigReturnsErrorWhenStartFails(t *testing.T) {
	resetGlobals(t)

	sentinel := errors.New("start failed")
	startLangfuse = func(context.Context, ...baselangfuse.Option) (func(context.Context) error, error) {
		return nil, sentinel
	}

	err := startWithConfig(langfuseConfig{})
	require.ErrorIs(t, err, sentinel)
}

func TestRegisterSignalCleanupLockedSkipsWhenAlreadyRegistered(t *testing.T) {
	resetGlobals(t)

	done := make(chan struct{})
	stateMu.Lock()
	state.signalCh = make(chan os.Signal, 1)
	state.signalDone = done
	stateMu.Unlock()

	var notifyCalls int
	notifySignals = func(chan<- os.Signal, ...os.Signal) {
		notifyCalls++
	}

	stateMu.Lock()
	registerSignalCleanupLocked()
	stateMu.Unlock()

	assert.Equal(t, 0, notifyCalls)
}

func TestAwaitSignalClosesAndReemitsSignal(t *testing.T) {
	resetGlobals(t)

	var closeCalls int
	var closeCtx context.Context
	stateMu.Lock()
	state.cleanup = func(ctx context.Context) error {
		closeCalls++
		closeCtx = ctx
		return nil
	}
	stateMu.Unlock()

	var gotSignal os.Signal
	reemitSignal = func(sig os.Signal) {
		gotSignal = sig
	}

	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	ch <- os.Interrupt

	awaitSignal(ch, done)

	assert.Equal(t, 1, closeCalls)
	assert.NotNil(t, closeCtx)
	assert.Equal(t, os.Interrupt, gotSignal)
}

func TestAwaitSignalIgnoresNilSignal(t *testing.T) {
	resetGlobals(t)

	var closeCalls int
	var reemitCalls int
	stateMu.Lock()
	state.cleanup = func(context.Context) error {
		closeCalls++
		return nil
	}
	stateMu.Unlock()
	reemitSignal = func(os.Signal) {
		reemitCalls++
	}

	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	ch <- nil

	awaitSignal(ch, done)

	assert.Equal(t, 0, closeCalls)
	assert.Equal(t, 0, reemitCalls)
}

func TestAwaitSignalReturnsWhenDoneClosed(t *testing.T) {
	resetGlobals(t)

	var closeCalls int
	var reemitCalls int
	stateMu.Lock()
	state.cleanup = func(context.Context) error {
		closeCalls++
		return nil
	}
	stateMu.Unlock()
	reemitSignal = func(os.Signal) {
		reemitCalls++
	}

	done := make(chan struct{})
	close(done)

	awaitSignal(make(chan os.Signal), done)

	assert.Equal(t, 0, closeCalls)
	assert.Equal(t, 0, reemitCalls)
}

func TestDefaultReemitSignalDoesNotPanicForSignalZero(t *testing.T) {
	assert.NotPanics(t, func() {
		defaultReemitSignal(syscall.Signal(0))
	})
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
