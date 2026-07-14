//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package engine

import (
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestCaseResultEvidenceJSONCompatibility(t *testing.T) {
	legacy, err := json.Marshal(CaseResult{EvalSetID: "set", EvalCaseID: "case"})
	if err != nil {
		t.Fatal(err)
	}
	if string(legacy) != `{"EvalSetID":"set","EvalCaseID":"case","SessionID":"","Trace":null,"Metrics":null}` {
		t.Fatalf("legacy JSON changed: %s", legacy)
	}
	withEvidence, err := json.Marshal(CaseResult{EvalCaseID: "case", ActualInvocations: []*evalset.Invocation{{InvocationID: "actual"}}, ExpectedInvocations: []*evalset.Invocation{{InvocationID: "expected"}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(withEvidence, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["actualInvocations"]; !ok {
		t.Fatal("actual invocation evidence was omitted")
	}
	if _, ok := decoded["expectedInvocations"]; !ok {
		t.Fatal("expected invocation evidence was omitted")
	}
}
