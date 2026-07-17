//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"math"
	"reflect"
	"testing"
)

func TestPairedBootstrap90Deterministic(t *testing.T) {
	deltas := []float64{0.1, 0.2, 0.3, 0.4}
	first, err := PairedBootstrap90(deltas, 42, 2000)
	if err != nil {
		t.Fatalf("PairedBootstrap90() error = %v", err)
	}
	second, err := PairedBootstrap90(deltas, 42, 2000)
	if err != nil {
		t.Fatalf("PairedBootstrap90() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed produced different intervals: %+v and %+v", first, second)
	}
	if first.Confidence != 0.9 || first.Lower <= 0 || first.Upper < first.Lower {
		t.Fatalf("invalid confidence interval: %+v", first)
	}
}

func TestPairedBootstrap90ConstantDelta(t *testing.T) {
	got, err := PairedBootstrap90([]float64{0.25, 0.25, 0.25}, 7, 100)
	if err != nil {
		t.Fatalf("PairedBootstrap90() error = %v", err)
	}
	if got.Lower != 0.25 || got.Upper != 0.25 {
		t.Fatalf("PairedBootstrap90() = %+v, want [0.25, 0.25]", got)
	}
}

func TestPairedBootstrap90RejectsInvalidInput(t *testing.T) {
	if _, err := PairedBootstrap90(nil, 1, 1); err == nil {
		t.Fatal("PairedBootstrap90() accepted empty deltas")
	}
	if _, err := PairedBootstrap90([]float64{1}, 1, 0); err == nil {
		t.Fatal("PairedBootstrap90() accepted zero resamples")
	}
	if _, err := PairedBootstrap90([]float64{math.Inf(1)}, 1, 1); err == nil {
		t.Fatal("PairedBootstrap90() accepted a non-finite delta")
	}
}

func TestPercentile(t *testing.T) {
	values := []float64{0, 10, 20}
	if got := percentile(values, 0.25); got != 5 {
		t.Fatalf("percentile(0.25) = %v, want 5", got)
	}
}
