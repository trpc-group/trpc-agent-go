//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// ParsedCommand is a structured representation of a shell command that
// has been validated by the shellsafe parser.
//
// The shell pipeline is split into a sequence of segments; each segment
// is a list of argv tokens. Operators between segments ("|", "&&",
// "||", ";") are intentionally discarded because the safety rules
// only need the executable names of each segment.
//
// Fields are exported so the rule implementations and tests can inspect
// them directly without re-parsing the original command string.
type ParsedCommand struct {
	// Segments lists the argv slices of every pipeline segment in
	// source order.
	Segments [][]string
	// Raw is the original command string, preserved for audit
	// messages and error reporting.
	Raw string
}

// ParseCommand validates cmd against the shellsafe grammar and returns
// the resulting ParsedCommand. The shellsafe parser rejects a wide
// range of unsafe constructs (command substitution, redirections,
// subshells, variable expansion, ...) before any rule lookup happens,
// so a deny on "curl" cannot be sidestepped via "$(c\\url)" or
// "${X}url".
//
// On parse failure the error is returned to the caller unchanged; the
// safety scanner maps a non-nil error to DecisionDeny so the framework
// fails closed.
func ParseCommand(cmd string) (*ParsedCommand, error) {
	if cmd == "" {
		return nil, fmt.Errorf("empty command")
	}
	pipe, err := shellsafe.Parse(cmd)
	if err != nil {
		return nil, fmt.Errorf("shellsafe parse: %w", err)
	}
	if pipe == nil || len(pipe.Commands) == 0 {
		return nil, fmt.Errorf("shellsafe parse: empty pipeline")
	}
	return &ParsedCommand{
		Segments: pipe.Commands,
		Raw:      cmd,
	}, nil
}

// Executables returns the basename of argv[0] for every segment in p.
// The returned slice has the same length as p.Segments.
//
// This is the data the rule implementations need in most cases: the
// "command name" (curl, rm, sudo, ...) regardless of any leading path
// prefix that may have been supplied.
func (p *ParsedCommand) Executables() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Segments))
	for _, seg := range p.Segments {
		if len(seg) == 0 || seg[0] == "" {
			continue
		}
		out = append(out, seg[0])
	}
	return out
}

// FirstExecutable returns the executable name of the first segment
// (i.e. argv[0] of the leftmost pipeline segment). For an empty
// pipeline it returns "".
func (p *ParsedCommand) FirstExecutable() string {
	if p == nil || len(p.Segments) == 0 || len(p.Segments[0]) == 0 {
		return ""
	}
	return p.Segments[0][0]
}

// ShellWrapper is a small, conservative list of shell wrappers and
// re-executing builtins. Membership is independent of the shellsafe
// implicit-deny set and is duplicated here so the safety package can
// match it without depending on the shellsafe API surface.
//
// If you need to widen this list, update it in two places: shellsafe's
// implicitDeny map (the source of truth) and this list.
var ShellWrapper = []string{
	"sh", "bash", "zsh", "ash", "dash", "ksh", "mksh", "fish",
	"pwsh", "powershell", "cmd",
	"busybox", "toybox",
	"eval", "exec", "command", "source", ".",
	"xargs", "env", "nohup", "timeout",
	"sudo", "su", "doas",
	"setsid", "unshare", "chroot", "runuser",
	"time", "nice", "ionice", "taskset",
	"stdbuf", "strace", "ltrace",
	"script", "flock",
	"trap", "alias", "unalias",
	"export", "unset", "readonly", "local", "declare", "typeset",
	"set", "shopt",
	"cd", "pushd", "popd",
	"printf", "read", "getopts",
}

// IsShellWrapper reports whether name is one of the built-in shell
// wrappers / re-executing builtins. Matching is case-insensitive and
// ignores any path prefix; "/usr/bin/SH", "sh.exe" and "Sh" all
// return true.
func IsShellWrapper(name string) bool {
	if name == "" {
		return false
	}
	low := toLowerASCII(name)
	// Strip a leading path to get the bare executable name.
	if i := lastIndexAny(low, "/\\"); i >= 0 {
		low = low[i+1:]
	}
	// Strip a Windows-style executable suffix.
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		if len(low) > len(ext) && low[len(low)-len(ext):] == ext {
			low = low[:len(low)-len(ext)]
			break
		}
	}
	for _, w := range ShellWrapper {
		if low == w {
			return true
		}
	}
	return false
}

// toLowerASCII lower-cases ASCII bytes only. This matches the
// shellsafe normalisation policy and avoids the C-locale surprises of
// strings.ToLower for non-ASCII inputs.
func toLowerASCII(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// lastIndexAny returns the byte index of the last occurrence of any
// rune in chars inside s, or -1 if none.
func lastIndexAny(s, chars string) int {
	for i := len(s) - 1; i >= 0; i-- {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return i
			}
		}
	}
	return -1
}
