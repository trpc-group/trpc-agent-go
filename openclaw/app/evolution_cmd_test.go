//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
)

// setupEvolutionTestDir creates a temp dir with revision data for testing.
func setupEvolutionTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	store := evolution.NewFileCandidateStore(dir)
	ctx := context.Background()

	rev := &evolution.Revision{
		SkillID:    "weather-monitor",
		RevisionID: "rev-pending-001",
		Source:     "reviewer",
		Action:     "create",
		Status:     evolution.RevisionPendingApproval,
		Spec: &evolution.SkillSpec{
			Name:        "Weather Monitor",
			Description: "Monitors weather for cities",
			WhenToUse:   "When monitoring weather",
			Steps:       []string{"Get coords", "Fetch data", "Save JSON"},
			Pitfalls:    []string{"Do not repeat API calls"},
		},
		CreatedAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	rev2 := &evolution.Revision{
		SkillID:    "recipe-cookbook",
		RevisionID: "rev-active-002",
		Source:     "reviewer",
		Action:     "update",
		Status:     evolution.RevisionActive,
		Spec: &evolution.SkillSpec{
			Name:        "Recipe Cookbook",
			Description: "Builds cookbooks",
			WhenToUse:   "When building cookbooks",
			Steps:       []string{"Read spec", "Call APIs", "Build JSON", "Save"},
		},
		CreatedAt: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.WriteRevision(ctx, rev2))

	auditDir := filepath.Join(dir, "recipe-cookbook")
	require.NoError(t, os.MkdirAll(auditDir, 0o755))
	auditLog := `{"at":"2026-05-01T09:00:00Z","action":"promote","skill_id":"recipe-cookbook","revision_id":"rev-active-002","status":"active","reason":"update"}
`
	require.NoError(t, os.WriteFile(filepath.Join(auditDir, "audit.log"), []byte(auditLog), 0o644))

	return dir
}

func runEvo(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := evoEnv{stdout: &out, stderr: &errOut}
	c := env.dispatch(args)
	return out.String(), errOut.String(), c
}

func TestEvolution_NoArgs(t *testing.T) {
	_, stderr, code := runEvo(t, nil)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "Usage: openclaw evolution")
}

func TestEvolution_Help(t *testing.T) {
	stdout, _, code := runEvo(t, []string{"help"})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, evoCmdPending)
	assert.Contains(t, stdout, evoCmdApprove)
	assert.Contains(t, stdout, evoCmdReject)
	assert.Contains(t, stdout, evoCmdDiff)
	assert.Contains(t, stdout, evoCmdAudit)
}

func TestEvolution_UnknownCommand(t *testing.T) {
	_, stderr, code := runEvo(t, []string{"foobar"})
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "unknown evolution command: foobar")
}

func TestEvolution_Pending_NoDir(t *testing.T) {
	t.Setenv("EVOLUTION_REVISIONS_DIR", "")
	_, stderr, code := runEvo(t, []string{evoCmdPending})
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--dir is required")
}

func TestEvolution_Pending_Empty(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runEvo(t, []string{evoCmdPending, "--dir", dir})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "No revisions pending approval.")
}

func TestEvolution_Pending_ShowsPending(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdPending, "--dir", dir})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "rev-pending-001")
	assert.Contains(t, stdout, "Weather Monitor")
	assert.Contains(t, stdout, "create")
	assert.Contains(t, stdout, "1 revision(s) pending approval.")
}

func TestEvolution_Diff_ShowsContent(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdDiff, "--dir", dir, "rev-pending-001"})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Skill:    weather-monitor")
	assert.Contains(t, stdout, "Revision: rev-pending-001")
	assert.Contains(t, stdout, "Status:   pending_approval")
	assert.Contains(t, stdout, "Name:        Weather Monitor")
	assert.Contains(t, stdout, "1. Get coords")
	assert.Contains(t, stdout, "- Do not repeat API calls")
}

func TestEvolution_Diff_MissingRevID(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	_, stderr, code := runEvo(t, []string{evoCmdDiff, "--dir", dir})
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "revision ID required")
}

func TestEvolution_Diff_NotFound(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	_, stderr, code := runEvo(t, []string{evoCmdDiff, "--dir", dir, "nonexistent"})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not found")
}

func TestEvolution_Approve(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdApprove, "--dir", dir, "rev-pending-001", "--comment", "lgtm"})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "promoted to active")

	store := evolution.NewFileCandidateStore(dir)
	rev, err := store.ReadRevision(context.Background(), "weather-monitor", "rev-pending-001")
	require.NoError(t, err)
	assert.Equal(t, evolution.RevisionActive, rev.Status)
}

func TestEvolution_Reject(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdReject, "--dir", dir, "rev-pending-001", "--comment", "too vague"})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "rejected")

	store := evolution.NewFileCandidateStore(dir)
	rev, err := store.ReadRevision(context.Background(), "weather-monitor", "rev-pending-001")
	require.NoError(t, err)
	assert.Equal(t, evolution.RevisionRejected, rev.Status)
}

func TestEvolution_Approve_MissingRevID(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	_, stderr, code := runEvo(t, []string{evoCmdApprove, "--dir", dir})
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "revision ID required")
}

func TestEvolution_Approve_AlreadyDecided(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	_, stderr, code := runEvo(t, []string{evoCmdApprove, "--dir", dir, "rev-active-002"})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "already decided")
}

func TestEvolution_Audit(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdAudit, "--dir", dir})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "recipe-cookbook")
	assert.Contains(t, stdout, "promote")
	assert.Contains(t, stdout, "update")
}

func TestEvolution_Audit_Empty(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runEvo(t, []string{evoCmdAudit, "--dir", dir})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "No audit events found.")
}

func TestEvolution_Pending_EnvVar(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	t.Setenv("EVOLUTION_REVISIONS_DIR", dir)
	stdout, _, code := runEvo(t, []string{evoCmdPending})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "rev-pending-001")
}

func TestFindSkillForRevision(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	store := evolution.NewFileCandidateStore(dir)

	skillID, err := findSkillForRevision(context.Background(), store, "rev-pending-001")
	require.NoError(t, err)
	assert.Equal(t, "weather-monitor", skillID)

	_, err = findSkillForRevision(context.Background(), store, "nonexistent")
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not found"))
}

func TestReadAuditLog(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	events, err := readAuditLog(dir, "recipe-cookbook")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "promote", events[0].Action)

	_, err = readAuditLog(dir, "nonexistent")
	assert.Error(t, err)
}

func TestTruncateID(t *testing.T) {
	assert.Equal(t, "short", truncateID("short"))
	assert.Equal(t, "123456789012", truncateID("1234567890123456"))
}

func TestEvolution_RunEvolution_Wrapper(t *testing.T) {
	// runEvolution is a thin wrapper that calls dispatch with os.Stdout/Stderr.
	// Calling with no args returns exit code 2 (usage).
	code := runEvolution(nil)
	assert.Equal(t, 2, code)
}

func TestEvolution_Pending_ExtraArgs(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	_, stderr, code := runEvo(t, []string{evoCmdPending, "--dir", dir, "extra"})
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "unexpected arguments")
}

func TestEvolution_Audit_WithLimit(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdAudit, "--dir", dir, "--limit", "1"})
	assert.Equal(t, 0, code)
	// Should have header + 1 data row only.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.LessOrEqual(t, len(lines), 3) // header + separator + 1 row (tabwriter may differ)
}

func TestEvolution_Reject_NotFound(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	_, stderr, code := runEvo(t, []string{evoCmdReject, "--dir", dir, "nonexistent"})
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not found")
}

func TestEvolution_Diff_ActiveRevision(t *testing.T) {
	dir := setupEvolutionTestDir(t)
	stdout, _, code := runEvo(t, []string{evoCmdDiff, "--dir", dir, "rev-active-002"})
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Recipe Cookbook")
	assert.Contains(t, stdout, "update")
	assert.Contains(t, stdout, "active")
}

func TestEvoFlags_Parse_FlagError(t *testing.T) {
	fl := newEvoFlags("test")
	_, _, err := fl.parse([]string{"--invalid-flag"})
	assert.Error(t, err)
}

func TestFindSkillForRevision_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	store := evolution.NewFileCandidateStore(dir)
	_, err := findSkillForRevision(context.Background(), store, "any")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
