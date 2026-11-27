//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdprocess

import (
	"strconv"
	"strings"

	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
)

// endEventExitCode normalizes the pinned envd Handler.Wait representation.
// Go's ProcessState.Exited is false for signal termination; its ExitCode is -1
// and its String is the only signal information carried by this protocol.
// Unknown statuses remain failures rather than looking like successful exits.
func endEventExitCode(end *process.ProcessEvent_EndEvent) (int32, bool) {
	if end.Exited {
		return end.ExitCode, true
	}
	if end.ExitCode != -1 {
		return 0, false
	}
	name, ok := strings.CutPrefix(end.Status, "signal: ")
	if !ok {
		return 0, false
	}
	name = strings.TrimSuffix(name, " (core dumped)")
	for signal := int32(1); signal < int32(len(linuxSignalNames)); signal++ {
		if name == linuxSignalNames[signal] {
			return 128 + signal, true
		}
	}
	// Go formats Linux signals outside its named table (32 through 64,
	// including real-time signals) as "signal N".
	number, ok := strings.CutPrefix(name, "signal ")
	if !ok {
		return 0, false
	}
	signal, err := strconv.ParseInt(number, 10, 32)
	if err != nil || signal < int64(len(linuxSignalNames)) || signal > 64 || strconv.FormatInt(signal, 10) != number {
		return 0, false
	}
	return 128 + int32(signal), true
}

// linuxSignalNames follows Go's syscall signal table for Linux amd64/arm64.
// Host syscall constants/strings differ on clients such as
// macOS and must not determine a remote process's exit status.
var linuxSignalNames = [...]string{
	1: "hangup", 2: "interrupt", 3: "quit", 4: "illegal instruction",
	5: "trace/breakpoint trap", 6: "aborted", 7: "bus error",
	8: "floating point exception", 9: "killed", 10: "user defined signal 1",
	11: "segmentation fault", 12: "user defined signal 2", 13: "broken pipe",
	14: "alarm clock", 15: "terminated", 16: "stack fault", 17: "child exited",
	18: "continued", 19: "stopped (signal)", 20: "stopped",
	21: "stopped (tty input)", 22: "stopped (tty output)",
	23: "urgent I/O condition", 24: "CPU time limit exceeded",
	25: "file size limit exceeded", 26: "virtual timer expired",
	27: "profiling timer expired", 28: "window changed", 29: "I/O possible",
	30: "power failure", 31: "bad system call",
}
