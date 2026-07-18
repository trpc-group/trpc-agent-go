//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionPolicyAllowDenyAsk(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		action  string
	}{
		{"go", []string{"test", "./..."}, "allow"},
		{"go", []string{"test", "./...", "-exec", "malicious"}, "deny"},
		{"go", []string{"run", "./cmd"}, "deny"},
		{"bash", []string{"skills/code-review/scripts/diff_stats.sh", "work/change.diff", "out/diff_stats.json"}, "allow"},
		{"bash", []string{"skills/code-review/scripts/diff_stats.sh", "work/change.diff", "out/x;rm"}, "deny"},
		{"curl", []string{"https://example.com"}, "ask"},
	}
	for _, test := range cases {
		got := decide(context.Background(), test.command, test.args)
		if string(got.Action) != test.action {
			t.Fatalf("%s %v = %s, want %s", test.command, test.args, got.Action, test.action)
		}
	}
}

func TestPermissionPolicyRejectsMalformedPayload(t *testing.T) {
	decision, err := (commandPolicy{}).CheckToolPermission(context.Background(), &tool.PermissionRequest{Arguments: json.RawMessage(`{`)})
	if err != nil || decision.Action != tool.PermissionActionDeny {
		t.Fatalf("malformed payload was not denied: %+v, %v", decision, err)
	}
}

func TestRedactionCoversCredentialFamilies(t *testing.T) {
	raw := `password="correct-horse" token="ghp_abcdefghijklmnopqrstuvwxyz123456" api_key="sk-abcdefghijklmnopqrstuvwxyz123456" postgres://user:pass@host/db`
	got := redact(raw)
	for _, secret := range []string{"correct-horse", "ghp_abcdefghijklmnopqrstuvwxyz", "sk-abcdefghijklmnopqrstuvwxyz", "user:pass"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked in %q", secret, got)
		}
	}
}

func TestTruncateRedactsBeforeBounding(t *testing.T) {
	got, cut := truncate(`token="ghp_abcdefghijklmnopqrstuvwxyz123456" trailing`, 20)
	if !cut || strings.Contains(got, "ghp_") {
		t.Fatalf("unexpected truncation: %q, %t", got, cut)
	}
}

func TestSandboxFailureBecomesHumanReview(t *testing.T) {
	items := sandboxReviewItems([]SandboxRun{{Command: "go", Args: []string{"test", "./..."}, Status: "failed", ErrorType: "timeout", Stderr: "deadline"}})
	if len(items) != 1 || items[0].Category != "sandbox" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestSandboxHumanReviewDecisionsMatchDedupe(t *testing.T) {
	items := sandboxReviewItems([]SandboxRun{
		{Command: "go", Status: RunFailed, ErrorType: "timeout"},
		{Command: "go", Status: RunFailed, ErrorType: "executor_error"},
	})
	decisions := filterDecisions(items, "needs_human_review", FilterRouteHuman)
	kept, dropped := 0, 0
	for _, decision := range decisions {
		switch decision.Action {
		case FilterRouteHuman:
			kept++
		case FilterDropDuplicate:
			dropped++
		}
	}
	if len(dedupe(items)) != 1 || kept != 1 || dropped != 1 {
		t.Fatalf("sandbox filter audit diverged from final findings: items=%+v decisions=%+v", items, decisions)
	}
}
