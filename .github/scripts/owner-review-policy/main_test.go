//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfigDoesNotForceLocalCodeownersPath(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("CODEOWNERS_PATH", "")
	t.Setenv("GITHUB_EVENT_PATH", "/tmp/event.json")
	t.Setenv("GITHUB_REPOSITORY", "trpc-group/trpc-agent-go")
	t.Setenv("GITHUB_TOKEN", "token")
	cfg, err := loadConfig()
	assert.NoError(t, err)
	assert.Empty(t, cfg.CodeownersPath)
	assert.Equal(t, defaultGitHubAPIURL, cfg.APIURL)
}

func TestParseCodeownersFile(t *testing.T) {
	tempDir := t.TempDir()
	codeownersPath := filepath.Join(tempDir, "CODEOWNERS")
	content := "# Comment line.\n\n* @sandyskies\n/agent/ @sandyskies @WineChord\n"
	err := os.WriteFile(codeownersPath, []byte(content), 0o644)
	assert.NoError(t, err)
	rules, err := parseCodeownersFile(codeownersPath)
	assert.NoError(t, err)
	assert.Len(t, rules, 2)
	assert.Equal(t, codeOwnerRule{
		Pattern: "*",
		Owners:  []string{"@sandyskies"},
	}, rules[0])
	assert.Equal(t, codeOwnerRule{
		Pattern: "/agent/",
		Owners:  []string{"@sandyskies", "@winechord"},
	}, rules[1])
}

func TestOwnersForPathUsesLastMatch(t *testing.T) {
	rules := []codeOwnerRule{
		{Pattern: "*", Owners: []string{"@sandyskies"}},
		{Pattern: "/agent/", Owners: []string{"@sandyskies", "@winechord"}},
		{Pattern: "/agent/internal/", Owners: []string{"@hyprh"}},
	}
	assert.Equal(t, []string{"@sandyskies"}, ownersForPath(rules, "model/openai/client.go"))
	assert.Equal(t, []string{"@sandyskies", "@winechord"}, ownersForPath(rules, "agent/runtime.go"))
	assert.Equal(t, []string{"@hyprh"}, ownersForPath(rules, "agent/internal/plan.go"))
}

func TestEvaluatePolicySkipsExternalOwnerRequirementForOwnedPaths(t *testing.T) {
	rules := []codeOwnerRule{
		{Pattern: "*", Owners: []string{"@sandyskies"}},
		{Pattern: "/agent/", Owners: []string{"@sandyskies", "@winechord"}},
	}
	result, err := evaluatePolicy(rules, "WineChord", []string{"agent/runtime.go"}, []string{"flash-lhr"})
	assert.NoError(t, err)
	assert.Empty(t, result.RequiredOwners)
	assert.Empty(t, result.ExternalFiles)
	assert.True(t, isSatisfied(result))
}

func TestEvaluatePolicyRequiresAnyExternalOwnerAcrossMultipleModules(t *testing.T) {
	rules := []codeOwnerRule{
		{Pattern: "*", Owners: []string{"@sandyskies"}},
		{Pattern: "/agent/", Owners: []string{"@sandyskies", "@winechord"}},
		{Pattern: "/artifact/", Owners: []string{"@sandyskies", "@liuzengh"}},
		{Pattern: "/benchmark/", Owners: []string{"@sandyskies", "@flash-lhr"}},
	}
	result, err := evaluatePolicy(rules, "WineChord", []string{"artifact/store.go", "benchmark/run.go"}, []string{"flash-lhr"})
	assert.NoError(t, err)
	assert.Equal(t, []string{"@flash-lhr", "@liuzengh", "@sandyskies"}, result.RequiredOwners)
	assert.Equal(t, []string{"flash-lhr"}, result.MatchingApprovers)
	assert.True(t, isSatisfied(result))
}

func TestEvaluatePolicyFailsWithoutExternalOwnerApproval(t *testing.T) {
	rules := []codeOwnerRule{
		{Pattern: "*", Owners: []string{"@sandyskies"}},
		{Pattern: "/artifact/", Owners: []string{"@sandyskies", "@liuzengh"}},
	}
	result, err := evaluatePolicy(rules, "WineChord", []string{"artifact/store.go"}, []string{"flash-lhr"})
	assert.NoError(t, err)
	assert.Equal(t, []string{"@liuzengh", "@sandyskies"}, result.RequiredOwners)
	assert.Empty(t, result.MatchingApprovers)
	assert.False(t, isSatisfied(result))
}

func TestLatestApprovedReviewersUsesLatestReviewStatePerReviewer(t *testing.T) {
	submittedAt := time.Date(2026, time.March, 19, 10, 0, 0, 0, time.UTC)
	reviews := []pullRequestReview{
		{ID: 1, State: "APPROVED", SubmittedAt: ptrTime(submittedAt), User: userPayload{Login: "flash-lhr"}},
		{ID: 2, State: "CHANGES_REQUESTED", SubmittedAt: ptrTime(submittedAt.Add(time.Minute)), User: userPayload{Login: "flash-lhr"}},
		{ID: 3, State: "APPROVED", SubmittedAt: ptrTime(submittedAt.Add(2 * time.Minute)), User: userPayload{Login: "liuzengh"}},
		{ID: 4, State: "APPROVED", SubmittedAt: ptrTime(submittedAt.Add(3 * time.Minute)), User: userPayload{Login: "author"}},
	}
	assert.Equal(t, []string{"liuzengh"}, latestApprovedReviewers(reviews, "author"))
}

func TestLatestApprovedReviewersKeepsApprovalAfterCommentOnlyReview(t *testing.T) {
	submittedAt := time.Date(2026, time.March, 19, 10, 0, 0, 0, time.UTC)
	reviews := []pullRequestReview{
		{ID: 1, State: "APPROVED", SubmittedAt: ptrTime(submittedAt), User: userPayload{Login: "flash-lhr"}},
		{ID: 2, State: "COMMENTED", SubmittedAt: ptrTime(submittedAt.Add(time.Minute)), User: userPayload{Login: "flash-lhr"}},
		{ID: 3, State: "COMMENTED", SubmittedAt: ptrTime(submittedAt.Add(2 * time.Minute)), User: userPayload{Login: "liuzengh"}},
	}
	assert.Equal(t, []string{"flash-lhr"}, latestApprovedReviewers(reviews, "author"))
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
