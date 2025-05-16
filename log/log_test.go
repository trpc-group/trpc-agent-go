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

func (*noopLogger) Debug(args ...interface{})                 {}
func (*noopLogger) Debugf(format string, args ...interface{}) {}
func (*noopLogger) Info(args ...interface{})                  {}
func (*noopLogger) Infof(format string, args ...interface{})  {}
func (*noopLogger) Warn(args ...interface{})                  {}
func (*noopLogger) Warnf(format string, args ...interface{})  {}
func (*noopLogger) Error(args ...interface{})                 {}
func (*noopLogger) Errorf(format string, args ...interface{}) {}
func (*noopLogger) Fatal(args ...interface{})                 {}
func (*noopLogger) Fatalf(format string, args ...interface{}) {}
