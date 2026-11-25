//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessStderrLogsCompleteLinesAndFlushesPartialLine(t *testing.T) {
	var lines []string
	writer := newProcessStderr(
		"test-agent",
		testStderrLineSink{lines: &lines},
	)

	n, err := writer.Write([]byte("first\r\nsecond"))
	require.NoError(t, err)
	require.Equal(t, len("first\r\nsecond"), n)
	require.Equal(t, []string{"acpagent: test-agent stderr: first"}, lines)

	writer.Flush()
	require.Equal(t, []string{
		"acpagent: test-agent stderr: first",
		"acpagent: test-agent stderr: second",
	}, lines)
	require.Equal(t, "first\r\nsecond", writer.Tail())
}

func TestProcessStderrForwardsRawChunksWithoutLogging(t *testing.T) {
	var destination bytes.Buffer
	writer := newProcessStderr("test-agent", &destination)

	n, err := writer.Write([]byte("first\n"))
	require.NoError(t, err)
	require.Equal(t, len("first\n"), n)
	_, err = writer.Write([]byte("second"))
	require.NoError(t, err)
	writer.Flush()

	require.Equal(t, "first\nsecond", destination.String())
	require.Equal(t, "first\nsecond", writer.Tail())
}

func TestProcessStderrBoundsTailAndLongPendingLines(t *testing.T) {
	var lines []string
	writer := newProcessStderr(
		"test-agent",
		testStderrLineSink{lines: &lines},
	)
	writer.tail.max = 8

	line := strings.Repeat("x", maxPendingStderrLineBytes+3)
	_, err := writer.Write([]byte(line))
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "[line truncated]")
	require.Len(t, writer.tail.buf, 8)

	writer.Flush()
	require.Len(t, lines, 2)
}

func TestProcessFailureErrorIncludesStderrTail(t *testing.T) {
	process := &processConnection{
		waitErr: fmt.Errorf("exit status 7"),
		stderr: &processStderr{
			tail: stderrTail{max: 64, buf: []byte("fatal: startup failed")},
		},
	}

	err := process.failureError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "process exited")
	require.Contains(t, err.Error(), "fatal: startup failed")
}

type testStderrLineSink struct {
	lines *[]string
}

func (testStderrLineSink) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w testStderrLineSink) writeStderrLine(name string, line string) {
	*w.lines = append(*w.lines, fmt.Sprintf("acpagent: %s stderr: %s", name, line))
}
