// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"strings"
	"testing"
)

func TestValidate_RejectsUnknownOp(t *testing.T) {
	s := validSpec()
	s.Operations[0].Op = "bad_op"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("expected unknown op error, got: %v", err)
	}
}

func TestValidate_RejectsUnknownWhat(t *testing.T) {
	s := validSpec()
	s.Verifies[0].What = "bad_what"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown what") {
		t.Errorf("expected unknown what error, got: %v", err)
	}
}

func TestValidate_RejectsUnknownBackend(t *testing.T) {
	s := validSpec()
	s.Operations[0].Backend = "database"
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("expected unknown backend error, got: %v", err)
	}
}

func TestValidate_RejectsZeroOperations(t *testing.T) {
	s := validSpec()
	s.Operations = nil
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one operation") {
		t.Errorf("expected zero operations error, got: %v", err)
	}
}

func TestValidate_RejectsZeroVerifies(t *testing.T) {
	s := validSpec()
	s.Verifies = nil
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one verify") {
		t.Errorf("expected zero verifies error, got: %v", err)
	}
}

func validSpec() *Spec {
	return &Spec{
		Name:        "test",
		Description: "desc",
		Backends:    BackendConfig{Session: []string{"inmemory"}, Memory: []string{"inmemory"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: nil}},
		Verifies:    []VerifySpec{{What: "events"}},
	}
}
