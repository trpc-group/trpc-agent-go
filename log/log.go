//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package log provides logging utilities.
package log

import (
	"context"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"trpc.group/trpc-go/trpc-a2a-go/log"
)

// Log level constants
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelFatal = "fatal"
)

var (
	zapLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)

	traceEnabled = false
)

// Default borrows logging utilities from zap.
// You may replace it with whatever logger you like as long as it implements log.Logger interface.
var Default Logger = zap.New(
	zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		zapLevel,
	),
	zap.AddCaller(),
	zap.AddCallerSkip(1),
).Sugar()

// ContextDefault is the default logger used by *Context helpers.
// It uses a separate zap logger so that caller information for helpers
// like DebugContext can be tuned independently of Default.
var ContextDefault Logger = zap.New(
	zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		zapLevel,
	),
	zap.AddCaller(),
	zap.AddCallerSkip(2),
).Sugar()

func init() {
	log.Default = Default
}

// SetLevel sets the log level to the specified level.
// Valid levels are: "debug", "info", "warn", "error", "fatal"
func SetLevel(level string) {
	switch level {
	case LevelDebug:
		zapLevel.SetLevel(zapcore.DebugLevel)
	case LevelInfo:
		zapLevel.SetLevel(zapcore.InfoLevel)
	case LevelWarn:
		zapLevel.SetLevel(zapcore.WarnLevel)
	case LevelError:
		zapLevel.SetLevel(zapcore.ErrorLevel)
	case LevelFatal:
		zapLevel.SetLevel(zapcore.FatalLevel)
	default:
		// Default to info level if the level is not recognized
		zapLevel.SetLevel(zapcore.InfoLevel)
	}
}

var encoderConfig = zapcore.EncoderConfig{
	TimeKey:        "ts",
	LevelKey:       "lvl",
	NameKey:        "name",
	CallerKey:      "caller",
	MessageKey:     "message",
	StacktraceKey:  "stacktrace",
	LineEnding:     zapcore.DefaultLineEnding,
	EncodeLevel:    zapcore.CapitalColorLevelEncoder,
	EncodeTime:     zapcore.RFC3339TimeEncoder,
	EncodeDuration: zapcore.SecondsDurationEncoder,
	EncodeCaller:   zapcore.ShortCallerEncoder,
}

// Logger defines the logging interface used throughout trpc-agent-go.
//
// This interface matches trpc-a2a-go/log.Logger so that Default can be
// assigned to log.Default without introducing a direct dependency on
// any specific logging implementation.
type Logger interface {
	// Debug logs to DEBUG log. Arguments are handled in the manner of fmt.Print.
	Debug(args ...any)
	// Debugf logs to DEBUG log. Arguments are handled in the manner of fmt.Printf.
	Debugf(format string, args ...any)
	// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
	Info(args ...any)
	// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
	Infof(format string, args ...any)
	// Warn logs to WARNING log. Arguments are handled in the manner of fmt.Print.
	Warn(args ...any)
	// Warnf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
	Warnf(format string, args ...any)
	// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
	Error(args ...any)
	// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
	Errorf(format string, args ...any)
	// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
	Fatal(args ...any)
	// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
	Fatalf(format string, args ...any)
}

// Debug logs to DEBUG log. Arguments are handled in the manner of fmt.Print.
func Debug(args ...any) {
	Default.Debug(args...)
}

// DebugContext logs to DEBUG log with context.
// By default, context is ignored and logs are delegated to ContextDefault.
var DebugContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Debug(args...)
}

// Debugf logs to DEBUG log. Arguments are handled in the manner of fmt.Printf.
func Debugf(format string, args ...any) {
	Default.Debugf(format, args...)
}

// DebugfContext logs to DEBUG log with context and formatting.
var DebugfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Debugf(format, args...)
}

// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
func Info(args ...any) {
	Default.Info(args...)
}

// InfoContext logs to INFO log with context.
var InfoContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Info(args...)
}

// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
func Infof(format string, args ...any) {
	Default.Infof(format, args...)
}

// InfofContext logs to INFO log with context and formatting.
var InfofContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Infof(format, args...)
}

// Warn logs to WARNING log. Arguments are handled in the manner of fmt.Print.
func Warn(args ...any) {
	Default.Warn(args...)
}

// WarnContext logs to WARNING log with context.
var WarnContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Warn(args...)
}

// Warnf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
func Warnf(format string, args ...any) {
	Default.Warnf(format, args...)
}

// WarnfContext logs to WARNING log with context and formatting.
var WarnfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Warnf(format, args...)
}

// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func Error(args ...any) {
	Default.Error(args...)
}

// ErrorContext logs to ERROR log with context.
var ErrorContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Error(args...)
}

// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func Errorf(format string, args ...any) {
	Default.Errorf(format, args...)
}

// ErrorfContext logs to ERROR log with context and formatting.
var ErrorfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Errorf(format, args...)
}

// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func Fatal(args ...any) {
	Default.Fatal(args...)
}

// FatalContext logs to ERROR log with context.
var FatalContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Fatal(args...)
}

// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func Fatalf(format string, args ...any) {
	Default.Fatalf(format, args...)
}

// FatalfContext logs to ERROR log with context and formatting.
var FatalfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Fatalf(format, args...)
}

// Tracef logs a message at the trace level with formatting.
func Tracef(format string, args ...any) {
	if !traceEnabled {
		return
	}
	Default.Debugf("[TRACE] "+format, args...)
}

// SetTraceEnabled sets the trace enabled flag.
func SetTraceEnabled(enabled bool) {
	traceEnabled = enabled
}
