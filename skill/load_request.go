//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import "errors"

var (
	// ErrInvalidLoadRequest indicates that a skill load declaration is
	// malformed or cannot be satisfied together with the other declarations.
	ErrInvalidLoadRequest = errors.New("invalid skill load request")

	// ErrSkillUnavailable indicates that a requested skill or supporting
	// document is not available through the invocation's effective repository.
	// Implementations intentionally do not distinguish missing content from
	// content hidden by an invocation-scoped visibility policy.
	ErrSkillUnavailable = errors.New("skill unavailable")
)

// LoadRequest declares a skill that must be loaded before the first model
// request of an invocation.
//
// Name identifies a skill in the effective repository; it is not a filesystem
// path. SKILL.md is always loaded. Docs contains additional paths relative to
// the skill root. IncludeAllDocs loads every supporting document exposed by
// the repository and is mutually exclusive with Docs. When Docs is empty and
// IncludeAllDocs is false, an existing document selection is preserved. Under
// the default turn load mode, the previous turn's selection is cleared before
// this request is applied.
type LoadRequest struct {
	Name           string
	Docs           []string
	IncludeAllDocs bool
}
