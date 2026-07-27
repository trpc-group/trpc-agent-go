//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2b

import (
	"strings"
	"testing"
)

func TestFramedCaptureBoundsOutputAndKeepsProtocolTail(t *testing.T) {
	capture := newFramedCapture(32)
	capture.WriteString(sentinelStdoutBegin + "\n")
	capture.WriteString(strings.Repeat("x", 4096))
	capture.WriteString("\n" + sentinelStdoutEnd + "\n" + sentinelExitPrefix + "7\n")
	stdout, _, exit := parseFramedOutput(capture.String(), "")
	stdout, truncated := limitOutput(stdout, 32)
	if len(stdout) != 32 || !truncated || exit != 7 {
		t.Fatalf("stdout=%q truncated=%t exit=%d", stdout, truncated, exit)
	}
}

func TestLimitOutputPreservesUTF8(t *testing.T) {
	got, truncated := limitOutput("你好世界", 5)
	if got != "你" || !truncated {
		t.Fatalf("limitOutput() = %q, %t", got, truncated)
	}
}
