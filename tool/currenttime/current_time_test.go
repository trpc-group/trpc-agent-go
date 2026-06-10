//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package currenttime

import (
	"context"
	"testing"
	"time"
)

func TestTool_CallUTCDate(t *testing.T) {
	tl := New()
	tl.now = func() time.Time {
		return time.Date(2026, 6, 10, 3, 4, 5, 0, time.UTC)
	}

	result, err := tl.Call(
		context.Background(),
		[]byte(`{"timezone":"UTC","format":"date"}`),
	)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	resp, ok := result.(Response)
	if !ok {
		t.Fatalf("Expected Response, got %T", result)
	}
	if !resp.Success {
		t.Fatalf("Expected success response, got %#v", resp)
	}
	if resp.CurrentTime != "2026-06-10" {
		t.Fatalf("Expected formatted date, got %q", resp.CurrentTime)
	}
	if resp.Timezone != "UTC" {
		t.Fatalf("Expected UTC timezone, got %q", resp.Timezone)
	}
	if resp.Note == "" {
		t.Fatal("Expected ephemeral-result note")
	}
}

func TestTool_InvalidTimezone(t *testing.T) {
	tl := New()

	result, err := tl.Call(
		context.Background(),
		[]byte(`{"timezone":"Invalid/Timezone"}`),
	)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	resp, ok := result.(Response)
	if !ok {
		t.Fatalf("Expected Response, got %T", result)
	}
	if resp.Success {
		t.Fatalf("Expected unsuccessful response, got %#v", resp)
	}
	if resp.Error == "" {
		t.Fatal("Expected invalid timezone error")
	}
}
