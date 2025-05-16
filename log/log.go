// Package log provides logging utilities.
package log

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Log level constants
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelFatal = "fatal"
)

// Default borrows logging utilities from zap.
// You may replace it with whatever logger you like as long as it implements log.Logger interface.
var Default Logger = zap.New(
	zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		zap.NewAtomicLevelAt(zapcore.InfoLevel), // Default to info level
	),
	zap.AddCaller(),
	zap.AddCallerSkip(1),
).Sugar()

// zapLogger keeps track of the zap atomic level for dynamic level changes
var zapLogger *zap.Logger
var zapLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)

func init() {
	// Initialize zapLogger with an atomic level that can be changed
	zapLogger = zap.New(
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			&zapLevel,
		),
		zap.AddCaller(),
		zap.AddCallerSkip(1),
	)

	// Update the default logger to use this configurable level
	Default = zapLogger.Sugar()
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

// Logger is the underlying logging work for trpc-a2a-go.
type Logger interface {
	// Debug logs to DEBUG log. Arguments are handled in the manner of fmt.Print.
	Debug(args ...interface{})
	// Debugf logs to DEBUG log. Arguments are handled in the manner of fmt.Printf.
	Debugf(format string, args ...interface{})
	// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
	Info(args ...interface{})
	// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
	Infof(format string, args ...interface{})
	// Warn logs to WARNING log. Arguments are handled in the manner of fmt.Print.
	Warn(args ...interface{})
	// Warnf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
	Warnf(format string, args ...interface{})
	// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
	Error(args ...interface{})
	// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
	Errorf(format string, args ...interface{})
	// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
	Fatal(args ...interface{})
	// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
	Fatalf(format string, args ...interface{})
}

// Debug logs to DEBUG log. Arguments are handled in the manner of fmt.Print.
func Debug(args ...interface{}) {
	Default.Debug(args...)
}

// Debugf logs to DEBUG log. Arguments are handled in the manner of fmt.Printf.
func Debugf(format string, args ...interface{}) {
	Default.Debugf(format, args...)
}

// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
func Info(args ...interface{}) {
	Default.Info(args...)
}

// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
func Infof(format string, args ...interface{}) {
	Default.Infof(format, args...)
}

// Warn logs to WARNING log. Arguments are handled in the manner of fmt.Print.
func Warn(args ...interface{}) {
	Default.Warn(args...)
}

// Warnf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
func Warnf(format string, args ...interface{}) {
	Default.Warnf(format, args...)
}

// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func Error(args ...interface{}) {
	Default.Error(args...)
}

// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func Errorf(format string, args ...interface{}) {
	Default.Errorf(format, args...)
}

// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func Fatal(args ...interface{}) {
	Default.Fatal(args...)
}

// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func Fatalf(format string, args ...interface{}) {
	Default.Fatalf(format, args...)
}

// Tracef logs a message at the trace level with formatting.
func Tracef(format string, args ...interface{}) {
	// Trace is more detailed than debug, so we'll log it at debug level
	// until we have proper trace level support in the underlying logger
	Default.Debugf("[TRACE] "+format, args...)
}
