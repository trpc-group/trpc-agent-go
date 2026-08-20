//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package userinputkey

// PatchKey identifies the executor-owned current-invocation input patch.
// GraphAgent writes it; the executor consumes it during checkpoint restore.
// It is not a public RuntimeState contract and is not merged as a regular
// underscore-prefixed state value.
const PatchKey = "__user_input_invocation_patch__"

// Patch is a typed, executor-owned snapshot of the current invocation's
// user-input decision. Absence of user_input in initial state is not a
// deletion; ClearUserInput is the explicit tombstone.
//
// Baseline is the fingerprint of the current invocation's raw user_input
// projection. An empty Baseline means this invocation recorded none, so an
// authorized restore must drop any previous fingerprint rather than keep it.
type Patch struct {
	Baseline       string
	ClearUserInput bool
}
