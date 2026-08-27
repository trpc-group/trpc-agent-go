// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"math"
	"testing"
)

func TestNoProgressGuardZeroValueUsesMinimumThreshold(t *testing.T) {
	var guard NoProgressGuard
	if guard.Observe("lookup", nil, nil) {
		t.Fatal("zero-valued guard must not trigger on the first observation")
	}
	if !guard.Observe("lookup", nil, nil) {
		t.Fatal("zero-valued guard must trigger on the second identical observation")
	}
}

func TestNoProgressGuardTriggersOnlyOnConsecutiveIdenticalObservations(t *testing.T) {
	guard := NewNoProgressGuard(2)
	if guard.Observe("lookup", map[string]any{"q": "x"}, "empty") {
		t.Fatal("first observation must not trigger")
	}
	if !guard.Observe("lookup", map[string]any{"q": "x"}, "empty") {
		t.Fatal("second identical observation must trigger")
	}
	if guard.Observe("lookup", map[string]any{"q": "x"}, "found") {
		t.Fatal("changed observation must reset the repeat count")
	}
}

func TestNoProgressGuardCanonicalizesJSONArguments(t *testing.T) {
	guard := NewNoProgressGuard(2)
	if guard.Observe("lookup", map[string]any{"a": 1, "b": 2}, "empty") {
		t.Fatal("first observation must not trigger")
	}
	if !guard.Observe("lookup", map[string]any{"b": 2, "a": 1}, "empty") {
		t.Fatal("equivalent JSON objects must have the same fingerprint")
	}
}

func TestNoProgressGuardReset(t *testing.T) {
	guard := NewNoProgressGuard(2)
	guard.Observe("lookup", nil, nil)
	guard.Reset()
	if guard.Observe("lookup", nil, nil) {
		t.Fatal("reset must clear repeat state")
	}
}

func TestNoProgressGuardMarshalFailureResetsState(t *testing.T) {
	guard := NewNoProgressGuard(2)
	if guard.Observe("lookup", nil, "empty") {
		t.Fatal("first valid observation must not trigger")
	}
	if !guard.Observe("lookup", nil, "empty") {
		t.Fatal("second identical valid observation must trigger")
	}
	if guard.Observe("lookup", math.NaN(), nil) {
		t.Fatal("failed argument serialization must not trigger")
	}
	if guard.Observe("lookup", nil, math.Inf(1)) {
		t.Fatal("failed observation serialization must not trigger")
	}
	if guard.Observe("lookup", nil, "empty") {
		t.Fatal("marshal failure must reset the repeat state")
	}
}
