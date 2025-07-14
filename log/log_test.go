//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package log_test

import (
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
