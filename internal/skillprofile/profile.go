//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package skillprofile centralizes built-in skill tool profile
// normalization and tool-flag resolution.
package skillprofile

import (
	"fmt"
	"strings"
)

const (
	// Full keeps the existing behavior and enables the complete built-in
	// skill tool suite, including execution tools.
	Full = "full"
	// KnowledgeOnly enables progressive-disclosure skill tools used for
	// knowledge injection without exposing execution tools.
	KnowledgeOnly = "knowledge_only"

	// ToolLoad is the built-in tool name for loading SKILL.md and optional docs.
	ToolLoad = "skill_load"
	// ToolListDocs is the built-in tool name for listing skill docs.
	ToolListDocs = "skill_list_docs"
	// ToolSelectDocs is the built-in tool name for selecting skill docs.
	ToolSelectDocs = "skill_select_docs"
	// ToolRun is the built-in tool name for non-interactive skill execution.
	ToolRun = "skill_run"
	// ToolExec is the built-in tool name for interactive skill execution.
	ToolExec = "skill_exec"
	// ToolWriteStdin is the built-in tool name for writing to skill_exec stdin.
	ToolWriteStdin = "skill_write_stdin"
	// ToolPollSession is the built-in tool name for polling skill_exec sessions.
	ToolPollSession = "skill_poll_session"
	// ToolKillSession is the built-in tool name for terminating skill_exec sessions.
	ToolKillSession = "skill_kill_session"
)

// Flags describes which built-in skill tools are enabled for a profile.
type Flags struct {
	Load        bool
	SelectDocs  bool
	ListDocs    bool
	Run         bool
	Exec        bool
	WriteStdin  bool
	PollSession bool
	KillSession bool
}

// Normalize canonicalizes a profile name and falls back to Full.
func Normalize(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case KnowledgeOnly:
		return KnowledgeOnly
	case "", Full:
		return Full
	default:
		return Full
	}
}

// NormalizeTool canonicalizes a built-in skill tool name.
func NormalizeTool(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ResolveFlags returns the enabled built-in skill tool set.
//
// When allowedTools is nil, the resolved flags come from the profile preset.
// When allowedTools is non-nil, it is treated as an explicit whitelist that
// overrides the profile.
func ResolveFlags(profile string, allowedTools []string) (Flags, error) {
	var flags Flags
	if allowedTools == nil {
		flags = presetFlags(Normalize(profile))
	} else {
		var err error
		flags, err = flagsFromAllowedTools(allowedTools)
		if err != nil {
			return Flags{}, err
		}
	}
	if err := validateFlags(flags); err != nil {
		return Flags{}, err
	}
	return flags, nil
}

// IsKnowledgeOnly reports whether the profile is knowledge-only after
// normalization.
func IsKnowledgeOnly(profile string) bool {
	return Normalize(profile) == KnowledgeOnly
}

// Any reports whether any built-in skill tool is enabled.
func (f Flags) Any() bool {
	return f.Load ||
		f.SelectDocs ||
		f.ListDocs ||
		f.Run ||
		f.Exec ||
		f.WriteStdin ||
		f.PollSession ||
		f.KillSession
}

// HasKnowledgeTools reports whether any non-executing skill disclosure tools
// are enabled.
func (f Flags) HasKnowledgeTools() bool {
	return f.Load || f.SelectDocs || f.ListDocs
}

// HasDocHelpers reports whether any doc inspection/selection helpers are
// enabled beyond skill_load itself.
func (f Flags) HasDocHelpers() bool {
	return f.SelectDocs || f.ListDocs
}

// RequiresExecutionTools reports whether the profile needs an executor.
func (f Flags) RequiresExecutionTools() bool {
	return f.Run || f.Exec || f.WriteStdin || f.PollSession || f.KillSession
}

// RequiresExecSessionTools reports whether the profile exposes interactive
// exec session helpers.
func (f Flags) RequiresExecSessionTools() bool {
	return f.Exec || f.WriteStdin || f.PollSession || f.KillSession
}

// WithoutInteractiveExecution removes interactive execution capabilities while
// preserving non-interactive execution.
func (f Flags) WithoutInteractiveExecution() Flags {
	f.Exec = false
	f.WriteStdin = false
	f.PollSession = false
	f.KillSession = false
	return f
}

func presetFlags(profile string) Flags {
	switch profile {
	case KnowledgeOnly:
		return Flags{
			Load:       true,
			SelectDocs: true,
			ListDocs:   true,
		}
	default:
		return Flags{
			Load:        true,
			SelectDocs:  true,
			ListDocs:    true,
			Run:         true,
			Exec:        true,
			WriteStdin:  true,
			PollSession: true,
			KillSession: true,
		}
	}
}

func flagsFromAllowedTools(allowedTools []string) (Flags, error) {
	var flags Flags
	for _, raw := range allowedTools {
		switch NormalizeTool(raw) {
		case ToolLoad:
			flags.Load = true
		case ToolListDocs:
			flags.ListDocs = true
		case ToolSelectDocs:
			flags.SelectDocs = true
		case ToolRun:
			flags.Run = true
		case ToolExec:
			flags.Exec = true
		case ToolWriteStdin:
			flags.WriteStdin = true
		case ToolPollSession:
			flags.PollSession = true
		case ToolKillSession:
			flags.KillSession = true
		default:
			return Flags{}, fmt.Errorf("unknown skill tool %q", raw)
		}
	}
	return flags, nil
}

func validateFlags(flags Flags) error {
	if flags.Exec && !flags.Run {
		return fmt.Errorf("%s requires %s", ToolExec, ToolRun)
	}
	if flags.WriteStdin && !flags.Exec {
		return fmt.Errorf("%s requires %s", ToolWriteStdin, ToolExec)
	}
	if flags.PollSession && !flags.Exec {
		return fmt.Errorf("%s requires %s", ToolPollSession, ToolExec)
	}
	if flags.KillSession && !flags.Exec {
		return fmt.Errorf("%s requires %s", ToolKillSession, ToolExec)
	}
	return nil
}
