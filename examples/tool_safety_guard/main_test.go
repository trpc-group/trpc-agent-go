//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestCheckedInArtifactsMatchSamples(t *testing.T) {
	policy, err := safety.LoadPolicy("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	got, err := encodeReports(sampleReports(policy))
	if err != nil {
		t.Fatalf("encodeReports() error = %v", err)
	}
	want, err := os.ReadFile("tool_safety_report.json")
	if err != nil {
		t.Fatalf("ReadFile(report) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tool_safety_report.json does not match generated samples")
	}

	var gotAudit bytes.Buffer
	for _, report := range sampleReports(policy) {
		if err := json.NewEncoder(&gotAudit).Encode(report.AuditEvent(sampleTimestamp)); err != nil {
			t.Fatalf("Encode(audit) error = %v", err)
		}
	}
	wantAudit, err := os.ReadFile("tool_safety_audit.jsonl")
	if err != nil {
		t.Fatalf("ReadFile(audit) error = %v", err)
	}
	if !bytes.Equal(gotAudit.Bytes(), wantAudit) {
		t.Fatalf("tool_safety_audit.jsonl does not match generated samples")
	}
}
