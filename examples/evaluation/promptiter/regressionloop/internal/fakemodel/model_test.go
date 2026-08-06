//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package fakemodel

import (
	"context"
	"testing"
)

func TestModelUsesExplicitRole(t *testing.T) {
	for _, role := range []Role{RoleCandidate, RoleJudge, RoleOptimizer} {
		instance := New(role)
		responses, err := instance.GenerateContent(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		response := <-responses
		if response == nil || len(response.Choices) != 1 || response.Choices[0].Message.Content == "" {
			t.Fatalf("role %q returned %#v", role, response)
		}
		if instance.Info().Name != "fake-deterministic" {
			t.Fatalf("Info() = %#v", instance.Info())
		}
	}
}

func TestModelRejectsUnknownRole(t *testing.T) {
	if _, err := New("unknown").GenerateContent(context.Background(), nil); err == nil {
		t.Fatal("GenerateContent() returned nil error")
	}
}
