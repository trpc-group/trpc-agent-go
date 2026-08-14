//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package userinputkey defines dependency-free internal graph state keys used
// to coordinate the current invocation's user input across framework layers.
package userinputkey

import (
	"crypto/sha256"
	"encoding/hex"
)

// Baseline identifies the fingerprint of the raw invocation user input
// captured before session processors enrich the corresponding user message.
//
// GraphAgent writes it into initial execution state. Checkpoints preserve it
// across interruptions that happen before the LLM consumes user_input. After
// a successful user-input stage the executor deletes it so later rewrite
// decisions and subsequent checkpoints cannot see a stale fingerprint. It is
// never exposed in completion events or child-agent RuntimeState.
const Baseline = "__user_input_baseline_fingerprint__"

// Fingerprint returns a stable, non-plaintext user-input fingerprint suitable
// for checkpoints and telemetry snapshots.
func Fingerprint(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
