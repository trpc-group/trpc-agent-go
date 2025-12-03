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
	defer func() {
		log.ContextDefault = original
	}()

	logger := &countLogger{}
	log.ContextDefault = logger

	log.InfoContext(ctx, "test")

	if logger.infoCalls != 1 {
		t.Fatalf("expected infoCalls=1, got %d", logger.infoCalls)
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
	infoCalls int
}

func (*countLogger) Debug(args ...any)                 {}
func (*countLogger) Debugf(format string, args ...any) {}
func (c *countLogger) Info(args ...any) {
	if len(args) == 0 {
		return
	}
	c.infoCalls++
}
func (*countLogger) Infof(format string, args ...any)  {}
func (*countLogger) Warn(args ...any)                  {}
func (*countLogger) Warnf(format string, args ...any)  {}
func (*countLogger) Error(args ...any)                 {}
func (*countLogger) Errorf(format string, args ...any) {}
func (*countLogger) Fatal(args ...any)                 {}
func (*countLogger) Fatalf(format string, args ...any) {}
