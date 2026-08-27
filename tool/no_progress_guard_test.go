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
	"sync"
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
	if guard.Observe("lookup", nil, nil) {
		t.Fatal("first valid observation must not trigger")
	}
	if !guard.Observe("lookup", nil, nil) {
		t.Fatal("second identical valid observation must trigger")
	}
	if guard.Observe("lookup", math.NaN(), nil) {
		t.Fatal("failed argument serialization must not trigger")
	}
	if guard.Observe("lookup", math.Inf(1), nil) {
		t.Fatal("distinct failed argument serializations must not be treated as repeated")
	}
	if guard.Observe("lookup", nil, nil) {
		t.Fatal("marshal failure must reset the repeat state")
	}
}

func TestNoProgressGuardSupportsConcurrentUse(t *testing.T) {
	guard := NewNoProgressGuard(2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	trueResults := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if guard.Observe("lookup", nil, nil) {
					mu.Lock()
					trueResults++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if trueResults != 799 {
		t.Fatalf("expected 799 triggered observations, got %d", trueResults)
	}
}
