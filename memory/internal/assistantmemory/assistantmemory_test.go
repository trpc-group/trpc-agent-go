//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package assistantmemory

import (
	"context"
	"testing"
)

type configuredProvider struct {
	enabled Value
}

func (p configuredProvider) ConfiguredAssistantEpisodeExtraction() Value {
	return p.enabled
}

func TestEnabled(t *testing.T) {
	if Enabled(nil) {
		t.Fatal("nil value enabled assistant extraction")
	}
	if Enabled(struct{}{}) {
		t.Fatal("unrelated value enabled assistant extraction")
	}
	if Enabled(configuredProvider{}) {
		t.Fatal("disabled provider enabled assistant extraction")
	}
	if !Enabled(configuredProvider{enabled: true}) {
		t.Fatal("enabled provider did not enable assistant extraction")
	}
}

func TestWorkerConfiguration(t *testing.T) {
	if _, ok := WorkerConfiguration(context.Background()); ok {
		t.Fatal("unmanaged context reported worker configuration")
	}
	for _, enabled := range []bool{false, true} {
		ctx := WithWorkerConfiguration(context.Background(), enabled)
		got, ok := WorkerConfiguration(ctx)
		if !ok || got != enabled {
			t.Fatalf("worker configuration = (%v, %v), want (%v, true)", got, ok, enabled)
		}
	}
}
