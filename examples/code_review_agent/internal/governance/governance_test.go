//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package governance

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGateDeniesZeroValuePolicyAndDoesNotStage(t *testing.T) {
	var policy StaticPolicy
	spy := &StageSpy{}
	decision, err := Gate(context.Background(), policy, Plan{CommandID: CommandGoTest}, nil, nil, spy.Stage)
	if err == nil {
		t.Fatalf("zero policy accepted")
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("decision = %s, want deny", decision.Action)
	}
	if spy.Count != 0 {
		t.Fatalf("stage count = %d, want 0", spy.Count)
	}
}

func TestGateAllowsOnlyDigestBoundFixedCommand(t *testing.T) {
	plan := validPlan()
	policy := StaticPolicy{Enabled: true, Action: tool.PermissionActionAllow, ExpectedPlanDigest: plan.Digest()}
	spy := &StageSpy{}
	decision, err := Gate(context.Background(), policy, plan, nil, nil, spy.Stage)
	if err != nil {
		t.Fatalf("gate failed: %v", err)
	}
	if decision.Action != tool.PermissionActionAllow || spy.Count != 1 {
		t.Fatalf("decision=%s stage=%d", decision.Action, spy.Count)
	}
	plan.Args[0] = "sh -c"
	if _, err := Gate(context.Background(), policy, plan, nil, nil, spy.Stage); err == nil {
		t.Fatalf("argument injection accepted")
	}
}

func TestPlanDigestBindsExecutionLimits(t *testing.T) {
	base := validPlan()
	tests := []struct {
		name   string
		mutate func(*Plan)
	}{
		{"command timeout", func(p *Plan) { p.CommandTimeoutMS++ }},
		{"task timeout", func(p *Plan) { p.TaskTimeoutMS++ }},
		{"stdout", func(p *Plan) { p.StdoutLimitBytes++ }},
		{"stderr", func(p *Plan) { p.StderrLimitBytes++ }},
		{"artifact count", func(p *Plan) { p.ArtifactMaxFiles++ }},
		{"artifact file", func(p *Plan) { p.ArtifactFileBytes++ }},
		{"artifact total", func(p *Plan) { p.ArtifactTotalBytes++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			tt.mutate(&changed)
			if changed.Digest() == base.Digest() {
				t.Fatalf("plan digest did not bind %s", tt.name)
			}
		})
	}
}

func TestGateRechecksApprovedSourcesBeforeStage(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_checks.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("approved"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := validPlan()
	plan.SkillDigest, _ = DigestTree(root)
	plan.ScriptDigest, _ = DigestFile(script)
	policy := StaticPolicy{Enabled: true, Action: tool.PermissionActionAllow, ExpectedPlanDigest: plan.Digest()}
	spy := &StageSpy{}
	verify := func() error {
		return VerifySources(plan, root, "")
	}
	record := func(DecisionRecord) error {
		return os.WriteFile(script, []byte("mutated"), 0o700)
	}
	if _, err := Gate(context.Background(), policy, plan, record, verify, spy.Stage); err == nil {
		t.Fatalf("mutated approved script was staged")
	}
	if spy.Count != 0 {
		t.Fatalf("stage count = %d, want 0", spy.Count)
	}
}

func TestVerifySourcesRejectsMutationImmediatelyBeforeRun(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_checks.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("approved"), 0o700); err != nil {
		t.Fatal(err)
	}
	plan := validPlan()
	plan.SkillDigest, _ = DigestTree(root)
	plan.ScriptDigest, _ = DigestFile(script)
	if err := VerifySources(plan, root, ""); err != nil {
		t.Fatalf("approved source failed verification: %v", err)
	}
	if err := os.WriteFile(script, []byte("mutated after staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := VerifySources(plan, root, ""); err == nil {
		t.Fatalf("source mutation immediately before run was accepted")
	}
}

func TestGateAskDoesNotStage(t *testing.T) {
	plan := validPlan()
	policy := StaticPolicy{Enabled: true, Action: tool.PermissionActionAsk, Reason: "human approval required", ExpectedPlanDigest: plan.Digest()}
	spy := &StageSpy{}
	decision, err := Gate(context.Background(), policy, plan, nil, nil, spy.Stage)
	if err != nil {
		t.Fatalf("ask gate failed: %v", err)
	}
	if decision.Action != tool.PermissionActionAsk {
		t.Fatalf("decision = %s, want ask", decision.Action)
	}
	if spy.Count != 0 {
		t.Fatalf("stage count = %d, want 0", spy.Count)
	}
}

func TestGatePersistsDecisionBeforeStageAndOnDeny(t *testing.T) {
	plan := validPlan()
	t.Run("allow order", func(t *testing.T) {
		var events []string
		rec := DecisionSpy{RecordFunc: func(DecisionRecord) error {
			events = append(events, "decision")
			return nil
		}}
		stage := func(context.Context) error {
			events = append(events, "stage")
			return nil
		}
		policy := StaticPolicy{Enabled: true, Action: tool.PermissionActionAllow, ExpectedPlanDigest: plan.Digest()}
		if _, err := Gate(context.Background(), policy, plan, rec.Record, nil, stage); err != nil {
			t.Fatalf("gate: %v", err)
		}
		if got := joinEvents(events); got != "decision,stage" {
			t.Fatalf("events = %s, want decision,stage", got)
		}
	})
	t.Run("deny persists without stage", func(t *testing.T) {
		var events []string
		rec := DecisionSpy{RecordFunc: func(DecisionRecord) error {
			events = append(events, "decision")
			return nil
		}}
		stage := func(context.Context) error {
			events = append(events, "stage")
			return nil
		}
		policy := StaticPolicy{Enabled: true, Action: tool.PermissionActionDeny, ExpectedPlanDigest: plan.Digest()}
		decision, err := Gate(context.Background(), policy, plan, rec.Record, nil, stage)
		if err != nil {
			t.Fatalf("deny should persist and return decision without error: %v", err)
		}
		if decision.Action != tool.PermissionActionDeny {
			t.Fatalf("decision = %s, want deny", decision.Action)
		}
		if got := joinEvents(events); got != "decision" {
			t.Fatalf("events = %s, want decision", got)
		}
	})
}

func validPlan() Plan {
	return Plan{
		Runtime: RuntimeFake, SkillDigest: DigestString("skill"), SnapshotDigest: DigestString("snapshot"),
		CommandID: CommandGoTest, ScriptDigest: DigestString("script"), Args: []string{"test", "./..."},
		Cwd: "work/repo", Env: map[string]string{"PATH": "/usr/bin"}, CommandTimeoutMS: 60000,
		TaskTimeoutMS: 120000, StdoutLimitBytes: 1 << 20, StderrLimitBytes: 1 << 20,
		ArtifactMaxFiles: 20, ArtifactFileBytes: 1 << 20, ArtifactTotalBytes: 8 << 20,
	}
}

func TestDigestTreeAndFileDetectSkillMutation(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "scripts", "run_checks.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("go test"), 0o700); err != nil {
		t.Fatal(err)
	}
	treeBefore, err := DigestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	fileBefore, err := DigestFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("go test ./..."), 0o700); err != nil {
		t.Fatal(err)
	}
	treeAfter, err := DigestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	fileAfter, err := DigestFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if treeBefore == treeAfter || fileBefore == fileAfter {
		t.Fatalf("mutation was not reflected in digests")
	}
}

type StageSpy struct {
	Count int
}

func (s *StageSpy) Stage(context.Context) error {
	s.Count++
	return nil
}

type DecisionSpy struct {
	RecordFunc func(DecisionRecord) error
}

func (s DecisionSpy) Record(rec DecisionRecord) error {
	if s.RecordFunc == nil {
		return nil
	}
	return s.RecordFunc(rec)
}

func joinEvents(events []string) string {
	out := ""
	for i, event := range events {
		if i > 0 {
			out += ","
		}
		out += event
	}
	return out
}
