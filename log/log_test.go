//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package log_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

func TestLog(t *testing.T) {
	log.Default = &noopLogger{}
	log.Debug("test")
	log.Debugf("test")
	log.Info("test")
	log.Infof("test")
	log.Warn("test")
	log.Warnf("test")
	log.Error("test")
	log.Errorf("test")
	log.Fatal("test")
	log.Fatalf("test")
}

func TestContextHelpersUseContextDefault(t *testing.T) {
	ctx := context.Background()

	original := log.ContextDefault
	log.ContextDefault = &countLogger{}
	t.Cleanup(func() {
		log.ContextDefault = original
	})

	logger, ok := log.ContextDefault.(*countLogger)
	require.True(t, ok, "ContextDefault should be *countLogger")

	log.DebugContext(ctx, "test")
	log.DebugfContext(ctx, "test %s", "value")
	log.InfoContext(ctx, "test")
	log.InfofContext(ctx, "test %s", "value")
	log.WarnContext(ctx, "test")
	log.WarnfContext(ctx, "test %s", "value")
	log.ErrorContext(ctx, "test")
	log.ErrorfContext(ctx, "test %s", "value")
	log.FatalContext(ctx, "test")
	log.FatalfContext(ctx, "test %s", "value")

	assert.Equal(t, 1, logger.debugCalls,
		"DebugContext should call Debug once")
	assert.Equal(t, 1, logger.debugfCalls,
		"DebugfContext should call Debugf once")
	assert.Equal(t, 1, logger.infoCalls,
		"InfoContext should call Info once")
	assert.Equal(t, 1, logger.infofCalls,
		"InfofContext should call Infof once")
	assert.Equal(t, 1, logger.warnCalls,
		"WarnContext should call Warn once")
	assert.Equal(t, 1, logger.warnfCalls,
		"WarnfContext should call Warnf once")
	assert.Equal(t, 1, logger.errorCalls,
		"ErrorContext should call Error once")
	assert.Equal(t, 1, logger.errorfCalls,
		"ErrorfContext should call Errorf once")
	assert.Equal(t, 1, logger.fatalCalls,
		"FatalContext should call Fatal once")
	assert.Equal(t, 1, logger.fatalfCalls,
		"FatalfContext should call Fatalf once")
}

type noopLogger struct{}

func (*noopLogger) Debug(args ...any)                 {}
func (*noopLogger) Debugf(format string, args ...any) {}
func (*noopLogger) Info(args ...any)                  {}
func (*noopLogger) Infof(format string, args ...any)  {}
func (*noopLogger) Warn(args ...any)                  {}
func (*noopLogger) Warnf(format string, args ...any)  {}
func (*noopLogger) Error(args ...any)                 {}
func (*noopLogger) Errorf(format string, args ...any) {}
func (*noopLogger) Fatal(args ...any)                 {}
func (*noopLogger) Fatalf(format string, args ...any) {}

type countLogger struct {
	debugCalls  int
	debugfCalls int
	infoCalls   int
	infofCalls  int
	warnCalls   int
	warnfCalls  int
	errorCalls  int
	errorfCalls int
	fatalCalls  int
	fatalfCalls int
}

func (c *countLogger) Debug(args ...any) {
	c.debugCalls++
}

func (c *countLogger) Debugf(format string, args ...any) {
	c.debugfCalls++
}

func (c *countLogger) Info(args ...any) {
	if len(args) == 0 {
		return
	}
	c.infoCalls++
}

func (c *countLogger) Infof(format string, args ...any) {
	c.infofCalls++
}

func (c *countLogger) Warn(args ...any) {
	c.warnCalls++
}

func (c *countLogger) Warnf(format string, args ...any) {
	c.warnfCalls++
}

func (c *countLogger) Error(args ...any) {
	c.errorCalls++
}

func (c *countLogger) Errorf(format string, args ...any) {
	c.errorfCalls++
}

func (c *countLogger) Fatal(args ...any) {
	c.fatalCalls++
}

func (c *countLogger) Fatalf(format string, args ...any) {
	c.fatalfCalls++
}

// traceLogger captures Debugf calls for TracefContext testing.
type traceLogger struct {
	debugfCalls int
	lastFormat  string
	lastArgs    []any
}

func (t *traceLogger) Debug(args ...any) {}
func (t *traceLogger) Debugf(format string, args ...any) {
	t.debugfCalls++
	t.lastFormat = format
	t.lastArgs = args
}
func (t *traceLogger) Info(args ...any)                  {}
func (t *traceLogger) Infof(format string, args ...any)  {}
func (t *traceLogger) Warn(args ...any)                  {}
func (t *traceLogger) Warnf(format string, args ...any)  {}
func (t *traceLogger) Error(args ...any)                 {}
func (t *traceLogger) Errorf(format string, args ...any) {}
func (t *traceLogger) Fatal(args ...any)                 {}
func (t *traceLogger) Fatalf(format string, args ...any) {}

// TestTracefContext verifies that TracefContext calls ContextDefault.Debugf
// with the correct format string prefixed with "[TRACE] ".
func TestTracefContext(t *testing.T) {
	ctx := context.Background()
	stub := &traceLogger{}
	original := log.ContextDefault
	log.ContextDefault = stub
	t.Cleanup(func() {
		log.ContextDefault = original
	})

	log.TracefContext(ctx, "hello %s", "world")

	assert.Equal(t, 1, stub.debugfCalls,
		"TracefContext should call Debugf once")
	assert.True(t, strings.HasPrefix(stub.lastFormat, "[TRACE] "),
		"TracefContext should prefix format with \"[TRACE] \"; got %q",
		stub.lastFormat)
	assert.Equal(t, "[TRACE] hello %s", stub.lastFormat,
		"TracefContext format should match expected")
	require.Len(t, stub.lastArgs, 1,
		"TracefContext should pass args correctly")
	assert.Equal(t, "world", stub.lastArgs[0],
		"TracefContext args should match expected")
}
