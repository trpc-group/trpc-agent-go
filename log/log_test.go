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
	"testing"

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
	if !ok {
		t.Fatalf("ContextDefault is not *countLogger")
	}

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

	if logger.debugCalls != 1 {
		t.Fatalf(
			"DebugContext should call Debug once; got %d",
			logger.debugCalls,
		)
	}
	if logger.debugfCalls != 1 {
		t.Fatalf(
			"DebugfContext should call Debugf once; got %d",
			logger.debugfCalls,
		)
	}
	if logger.infoCalls != 1 {
		t.Fatalf(
			"InfoContext should call Info once; got %d",
			logger.infoCalls,
		)
	}
	if logger.infofCalls != 1 {
		t.Fatalf(
			"InfofContext should call Infof once; got %d",
			logger.infofCalls,
		)
	}
	if logger.warnCalls != 1 {
		t.Fatalf(
			"WarnContext should call Warn once; got %d",
			logger.warnCalls,
		)
	}
	if logger.warnfCalls != 1 {
		t.Fatalf(
			"WarnfContext should call Warnf once; got %d",
			logger.warnfCalls,
		)
	}
	if logger.errorCalls != 1 {
		t.Fatalf(
			"ErrorContext should call Error once; got %d",
			logger.errorCalls,
		)
	}
	if logger.errorfCalls != 1 {
		t.Fatalf(
			"ErrorfContext should call Errorf once; got %d",
			logger.errorfCalls,
		)
	}
	if logger.fatalCalls != 1 {
		t.Fatalf(
			"FatalContext should call Fatal once; got %d",
			logger.fatalCalls,
		)
	}
	if logger.fatalfCalls != 1 {
		t.Fatalf(
			"FatalfContext should call Fatalf once; got %d",
			logger.fatalfCalls,
		)
	}
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
