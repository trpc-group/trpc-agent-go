//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replayconsistency drives one deterministic operation script against
// several Session and Memory backends and reports every observable difference
// between them.
//
// Applications routinely develop against the in-memory backends and switch to
// SQLite, Redis, or a SQL database later. When backends disagree about event
// ordering, state, memory, or summaries, the resulting failures surface far
// from their cause: replay reorders, context is lost, long-term memory is
// polluted, and summaries overwrite each other. This package turns that class
// of bug into a test.
//
// # Model
//
// A [Scenario] is a list of [Op] values. Ops are declarative data rather than
// closures, so the identical script reaches every backend. Each op carries its
// own event IDs and timestamp offsets, which removes the two largest sources
// of nondeterminism before comparison begins.
//
// Running a scenario against one backend produces an [Observation]: the
// canonical projection of everything that backend persisted. Observations are
// compared field by field against the baseline backend, and each difference
// becomes a [Divergence] carrying a structured path such as
//
//	session[app/user/sess-1].events[3].toolCalls[0].function.name
//	session[app/user/sess-1].summaries[filterKey="tool/code"].text
//	memory[app/user/mem-2].topics
//
// # Fail-closed comparison
//
// Normalization hides differences, so an over-eager normalizer produces a test
// that passes while the backends genuinely disagree. Every value that reaches
// the comparator is therefore either compared exactly, normalized by an
// explicitly named rule, or declared as an allowed difference with a written
// reason. A field covered by none of the three is reported as a divergence
// rather than skipped. Adding a field to [Observation] cannot silently widen
// what the harness ignores.
//
// # Backends
//
// The lightweight mode requires no external service and is the mode CI runs:
// in-memory, SQLite on a temporary file, and Redis against an in-process
// miniredis. Integration backends are enabled through environment variables
// and skipped when unset. Backends declare what they support through
// [Capabilities], so a backend that lacks a feature is reported as
// unsupported instead of as a difference.
package replayconsistency
