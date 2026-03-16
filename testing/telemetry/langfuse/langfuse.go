//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package langfuse provides test helpers that auto-start Langfuse telemetry
// from environment configuration and clean it up on process shutdown.
package langfuse

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	baselangfuse "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

const signalCleanupTimeout = 5 * time.Second

var (
	startLangfuse = baselangfuse.Start
	logf          = log.Printf
	notifySignals = signal.Notify
	stopSignals   = signal.Stop
	reemitSignal  = defaultReemitSignal

	stateMu sync.Mutex
	state   runtimeState
)

type runtimeState struct {
	cleanup    func(context.Context) error
	signalCh   chan os.Signal
	signalDone chan struct{}
}

func init() {
	autoStart()
}

func autoStart() {
	cfg, enabled, err := resolveConfigFromEnv()
	if err != nil {
		logf("testing/telemetry/langfuse: %v", err)
		return
	}
	if !enabled {
		return
	}
	if err := startWithConfig(cfg); err != nil {
		logf("testing/telemetry/langfuse: %v", err)
	}
}

func startWithConfig(cfg langfuseConfig) error {
	stateMu.Lock()
	if state.cleanup != nil {
		stateMu.Unlock()
		return nil
	}
	stateMu.Unlock()

	opts, err := buildStartOptions(cfg)
	if err != nil {
		return err
	}
	cleanup, err := startLangfuse(context.Background(), opts...)
	if err != nil {
		return err
	}

	stateMu.Lock()
	defer stateMu.Unlock()
	if state.cleanup != nil {
		return nil
	}
	state.cleanup = cleanup
	registerSignalCleanupLocked()
	return nil
}

// Close releases the Langfuse telemetry pipeline started by this helper.
func Close(ctx context.Context) error {
	stateMu.Lock()
	cleanup := state.cleanup
	state.cleanup = nil
	stopSignalCleanupLocked()
	stateMu.Unlock()

	if cleanup == nil {
		return nil
	}
	return cleanup(ctx)
}

func registerSignalCleanupLocked() {
	if state.signalCh != nil {
		return
	}
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	notifySignals(ch, os.Interrupt, syscall.SIGTERM)
	state.signalCh = ch
	state.signalDone = done
	go awaitSignal(ch, done)
}

func stopSignalCleanupLocked() {
	if state.signalDone != nil {
		close(state.signalDone)
		state.signalDone = nil
	}
	if state.signalCh != nil {
		stopSignals(state.signalCh)
		state.signalCh = nil
	}
}

func awaitSignal(ch <-chan os.Signal, done <-chan struct{}) {
	select {
	case sig := <-ch:
		if sig == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), signalCleanupTimeout)
		defer cancel()
		_ = Close(ctx)
		reemitSignal(sig)
	case <-done:
	}
}

func defaultReemitSignal(sig os.Signal) {
	signal.Reset(sig)
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}
	_ = process.Signal(sig)
}
