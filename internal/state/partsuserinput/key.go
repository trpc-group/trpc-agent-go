//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package partsuserinput holds the internal graph-state key for the original
// text of a ContentParts-only user turn.
package partsuserinput

// Key is the graph-state entry for the original nonempty text projected from
// a user message whose Content is empty.
//
// GraphAgent sets Key when it projects that turn. Checkpoint snapshots keep
// the string, and resume treats the saved value as authoritative. A missing
// value keeps the legacy text-only user-input behavior. Key is internal
// graph state, not a public schema field. Its "__" prefix lets the existing
// resume merge and time-travel guards preserve a checkpoint value.
const Key = "__content_parts_user_input__"
