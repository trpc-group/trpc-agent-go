//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package partsuserinput holds the internal graph-state key for a user turn's
// parts projection. An empty string is a clear signal; a missing key is not.
package partsuserinput

// Key is the private graph-state entry for a user turn's parts projection.
//
// A nonempty string is the text projected from a ContentParts-only user
// message. An empty string is a clear signal for any other meaningful
// non-resume user turn, including nonempty Content and pure media, and is
// removed before fresh execution, checkpoints, and cache keys. A missing key
// is not a clear signal: a blank user message, a non-user message, and a
// plain resume leave Key absent. Ordinary resume treats a saved nonempty
// value as authoritative. Resume replaces or removes Key only when both
// messages and user_input were supplied and accepted. Key is not a public
// schema field. Its "__" prefix lets resume merge and time-travel guards
// preserve a checkpoint value except for that replacement.
const Key = "__content_parts_user_input__"
