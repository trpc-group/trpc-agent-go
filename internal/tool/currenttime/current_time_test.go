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

func TestTool_CallUTC(t *testing.T) {
	tl := New("")
	tl.now = func() time.Time {
		return time.Date(2026, 6, 10, 3, 4, 5, 0, time.UTC)
	}

	result, err := tl.Call(
		context.Background(),
		[]byte(`{"timezone":"UTC"}`),
	)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	resp, ok := result.(Response)
	if !ok {
		t.Fatalf("Expected Response, got %T", result)
	}
	if resp.CurrentTime != "2026-06-10 03:04:05 UTC" {
		t.Fatalf("Expected formatted time, got %q", resp.CurrentTime)
	}
	if resp.Timezone != "UTC" {
		t.Fatalf("Expected UTC timezone, got %q", resp.Timezone)
	}
	if resp.Note == "" {
		t.Fatal("Expected ephemeral-result note")
	}
}

func TestTool_UsesDefaultTimezone(t *testing.T) {
	tl := New("Asia/Shanghai")
	tl.now = func() time.Time {
		return time.Date(2026, 6, 10, 3, 4, 5, 0, time.UTC)
	}

	result, err := tl.Call(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	resp, ok := result.(Response)
	if !ok {
		t.Fatalf("Expected Response, got %T", result)
	}
	if resp.CurrentTime != "2026-06-10 11:04:05 CST" {
		t.Fatalf("Expected default timezone time, got %q", resp.CurrentTime)
	}
	if resp.Timezone != "Asia/Shanghai" {
		t.Fatalf("Expected default timezone, got %q", resp.Timezone)
	}
}

func TestTool_RequestTimezoneOverridesDefault(t *testing.T) {
	tl := New("Asia/Shanghai")
	tl.now = func() time.Time {
		return time.Date(2026, 6, 10, 3, 4, 5, 0, time.UTC)
	}

	result, err := tl.Call(
		context.Background(),
		[]byte(`{"timezone":"UTC"}`),
	)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	resp, ok := result.(Response)
	if !ok {
		t.Fatalf("Expected Response, got %T", result)
	}
	if resp.CurrentTime != "2026-06-10 03:04:05 UTC" {
		t.Fatalf("Expected requested timezone time, got %q", resp.CurrentTime)
	}
	if resp.Timezone != "UTC" {
		t.Fatalf("Expected requested timezone, got %q", resp.Timezone)
	}
}

func TestTool_EmptyTimezoneUsesLocal(t *testing.T) {
	originalLocal := time.Local
	local := time.FixedZone("TEST", 2*60*60)
	time.Local = local
	defer func() { time.Local = originalLocal }()

	tl := New("")
	tl.now = func() time.Time {
		return time.Date(2026, 6, 10, 3, 4, 5, 0, time.UTC)
	}

	result, err := tl.Call(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	resp, ok := result.(Response)
	if !ok {
		t.Fatalf("Expected Response, got %T", result)
	}
	if resp.CurrentTime != "2026-06-10 05:04:05 TEST" {
		t.Fatalf("Expected local timezone time, got %q", resp.CurrentTime)
	}
	if resp.Timezone != "TEST" {
		t.Fatalf("Expected local timezone, got %q", resp.Timezone)
	}
}

func TestTool_InvalidTimezoneFallsBackToUTC(t *testing.T) {
	tl := New("")
	tl.now = func() time.Time {
		return time.Date(2026, 6, 10, 3, 4, 5, 0, time.UTC)
	}

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
	if resp.CurrentTime != "2026-06-10 03:04:05 UTC" {
		t.Fatalf("Expected UTC fallback time, got %q", resp.CurrentTime)
	}
	if resp.Timezone != "UTC" {
		t.Fatalf("Expected UTC fallback timezone, got %q", resp.Timezone)
	}
}

func TestTool_InvalidJSONReturnsError(t *testing.T) {
	tl := New("")

	if _, err := tl.Call(context.Background(), []byte(`{`)); err == nil {
		t.Fatal("Expected invalid JSON error")
	}
}

func TestTool_DeclarationMinimalSchema(t *testing.T) {
	decl := New("").Declaration()

	if decl.Name != ToolName {
		t.Fatalf("Expected tool name %q, got %q", ToolName, decl.Name)
	}
	if _, ok := decl.InputSchema.Properties["timezone"]; !ok {
		t.Fatal("Expected timezone input property")
	}
	if _, ok := decl.InputSchema.Properties["format"]; ok {
		t.Fatal("Did not expect format input property")
	}
}
