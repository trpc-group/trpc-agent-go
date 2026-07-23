//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestCriticalCategoryRecallIs100Percent(t *testing.T) {
	policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	guard, err := safety.New(policy)
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	fixtures := loadCorpus(t, filepath.Join("testdata", "dangerous_corpus.json"))

	categories := map[string]struct {
		rule string
		ids  map[string]bool
	}{
		"dangerous deletion": {
			rule: "destructive.delete",
			ids:  map[string]bool{"delete-root": false, "delete-worktree": false},
		},
		"credential access": {
			rule: "credential.access",
			ids:  map[string]bool{"ssh-key": false, "dotenv": false, "cloud-credentials": false},
		},
		"non-allowlisted network": {
			rule: "network.denied",
			ids:  map[string]bool{"network-curl": false, "network-wget": false, "netcat": false},
		},
	}

	for _, fixture := range fixtures {
		for categoryName, category := range categories {
			if _, belongs := category.ids[fixture.ID]; !belongs {
				continue
			}
			report, scanErr := guard.Scan(context.Background(), fixture.Request)
			if scanErr != nil {
				t.Fatalf("scan %s/%s: %v", categoryName, fixture.ID, scanErr)
			}
			if report.Decision != tool.PermissionActionDeny {
				t.Errorf("%s/%s decision = %s, want deny", categoryName, fixture.ID, report.Decision)
				continue
			}
			if !reportHasRule(report, category.rule) {
				t.Errorf("%s/%s missed rule %s: %+v",
					categoryName, fixture.ID, category.rule, report.Matches)
				continue
			}
			category.ids[fixture.ID] = true
			categories[categoryName] = category
		}
	}

	for categoryName, category := range categories {
		for id, detected := range category.ids {
			if !detected {
				t.Errorf("%s sample %s was not detected for the required reason", categoryName, id)
			}
		}
	}
}
