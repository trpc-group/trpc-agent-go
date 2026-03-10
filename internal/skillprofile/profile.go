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

import "strings"

const (
	// Full keeps the existing behavior and enables the complete built-in
	// skill tool suite, including execution tools.
	Full = "full"
	// KnowledgeOnly enables progressive-disclosure skill tools used for
	// knowledge injection without exposing execution tools.
	KnowledgeOnly = "knowledge_only"
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

// ResolveFlags returns the enabled tool set for a profile.
func ResolveFlags(profile string) Flags {
	switch Normalize(profile) {
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

// IsKnowledgeOnly reports whether the profile is knowledge-only after
// normalization.
func IsKnowledgeOnly(profile string) bool {
	return Normalize(profile) == KnowledgeOnly
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
