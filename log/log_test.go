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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

func TestLog(t *testing.T) {
	original := log.Default
	t.Cleanup(func() {
		log.Default = original
	})
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

	assert.Equal(t, 1, logger.debugCalls, "DebugContext should call Debug once")
	assert.Equal(t, 1, logger.debugfCalls, "DebugfContext should call Debugf once")
	assert.Equal(t, 1, logger.infoCalls, "InfoContext should call Info once")
	assert.Equal(t, 1, logger.infofCalls, "InfofContext should call Infof once")
	assert.Equal(t, 1, logger.warnCalls, "WarnContext should call Warn once")
	assert.Equal(t, 1, logger.warnfCalls, "WarnfContext should call Warnf once")
	assert.Equal(t, 1, logger.errorCalls, "ErrorContext should call Error once")
	assert.Equal(t, 1, logger.errorfCalls, "ErrorfContext should call Errorf once")
	assert.Equal(t, 1, logger.fatalCalls, "FatalContext should call Fatal once")
	assert.Equal(t, 1, logger.fatalfCalls, "FatalfContext should call Fatalf once")
}

func TestInfofCallerReportsCallSite(t *testing.T) {
	original := log.Default
	observed, wrapped := wrapLoggerWithObserver(t, log.Default)
	log.Default = wrapped
	t.Cleanup(func() {
		log.Default = original
	})

	format := "infof caller check %s"
	expectedMessage := fmt.Sprintf(format, "ok")
	expectedFile, expectedLine := captureInfofCall(t, format, "ok")

	entries := observed.FilterMessage(expectedMessage).All()
	require.Len(t, entries, 1)
	assertCallerMatches(t, entries[0].Entry.Caller, expectedFile, expectedLine)
}

func TestContextInfofCallerReportsCallSite(t *testing.T) {
	original := log.ContextDefault
	observed, wrapped := wrapLoggerWithObserver(t, log.ContextDefault)
	log.ContextDefault = wrapped
	t.Cleanup(func() {
		log.ContextDefault = original
	})

	format := "context infof caller check %s"
	expectedMessage := fmt.Sprintf(format, "ok")
	expectedFile, expectedLine := captureContextInfofCall(t, context.Background(), format, "ok")

	entries := observed.FilterMessage(expectedMessage).All()
	require.Len(t, entries, 1)
	assertCallerMatches(t, entries[0].Entry.Caller, expectedFile, expectedLine)
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
	log.SetTraceEnabled(true)
	t.Cleanup(func() {
		log.SetTraceEnabled(false)
	})

	log.TracefContext(ctx, "hello %s", "world")

	assert.Equal(t, 1, stub.debugfCalls, "TracefContext should call Debugf once")
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

func TestTracefContext_Disabled(t *testing.T) {
	ctx := context.Background()
	stub := &traceLogger{}
	original := log.ContextDefault
	log.ContextDefault = stub
	t.Cleanup(func() {
		log.ContextDefault = original
	})

	log.SetTraceEnabled(false)
	t.Cleanup(func() {
		log.SetTraceEnabled(false)
	})

	log.TracefContext(ctx, "hello %s", "world")

	assert.Equal(t, 0, stub.debugfCalls, "TracefContext should not call Debugf when trace is disabled")
}

func wrapLoggerWithObserver(t *testing.T, logger log.Logger) (*observer.ObservedLogs, log.Logger) {
	t.Helper()
	sugar, ok := logger.(*zap.SugaredLogger)
	require.True(t, ok, "Logger is not *zap.SugaredLogger")
	core, observed := observer.New(zapcore.DebugLevel)
	wrapped := sugar.Desugar().WithOptions(zap.WrapCore(func(existing zapcore.Core) zapcore.Core {
		return zapcore.NewTee(existing, core)
	}))
	return observed, wrapped.Sugar()
}

func captureInfofCall(t *testing.T, format string, args ...any) (string, int) {
	t.Helper()
	file := currentTestFile(t)
	line := findLogCallLine(t, file, "captureInfofCall", "Infof")
	log.Infof(format, args...)
	return file, line
}

func captureContextInfofCall(t *testing.T, ctx context.Context, format string, args ...any) (string, int) {
	t.Helper()
	file := currentTestFile(t)
	line := findLogCallLine(t, file, "captureContextInfofCall", "InfofContext")
	log.InfofContext(ctx, format, args...)
	return file, line
}

func assertCallerMatches(t *testing.T, caller zapcore.EntryCaller, expectedFile string, expectedLine int) {
	t.Helper()
	require.True(t, caller.Defined, "Caller should be defined")
	assert.Equal(t, filepath.Base(expectedFile), filepath.Base(caller.File))
	assert.Equal(t, expectedLine, caller.Line)
}

func currentTestFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return file
}

// findLogCallLine locates the line of log.<selector> in the named helper function.
func findLogCallLine(t *testing.T, file string, funcName string, selector string) int {
	t.Helper()
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, file, nil, 0)
	require.NoError(t, err, "parse file failed")

	var line int
	ast.Inspect(node, func(n ast.Node) bool {
		if line != 0 {
			return false
		}
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if line != 0 {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name == "log" && sel.Sel.Name == selector {
				line = fset.Position(call.Pos()).Line
				return false
			}
			return true
		})
		return false
	})

	require.NotZero(t, line, "log call not found")
	return line
}
