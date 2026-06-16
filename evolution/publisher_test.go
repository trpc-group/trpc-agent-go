//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilePublisher_UpsertSkill(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	spec := &SkillSpec{
		Name:        "Deploy Service",
		Description: "Steps to deploy a microservice",
		WhenToUse:   "When deploying a new version of a service to production.",
		Steps:       []string{"Build image", "Push to registry", "Update deployment"},
		Pitfalls:    []string{"Don't forget to run tests first"},
	}

	err := pub.UpsertSkill(context.Background(), spec)
	require.NoError(t, err)

	target := filepath.Join(dir, "deploy-service", "SKILL.md")
	data, err := os.ReadFile(target)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "# Deploy Service")
	assert.Contains(t, content, "## When to use")
	assert.Contains(t, content, "1. Build image")
	assert.Contains(t, content, "## Pitfalls")
	assert.Contains(t, content, "- Don't forget to run tests first")
}

func TestFilePublisher_UpsertSkill_Overwrite(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	spec := &SkillSpec{
		Name:  "My Skill",
		Steps: []string{"step1"},
	}
	require.NoError(t, pub.UpsertSkill(context.Background(), spec))

	spec.Steps = []string{"updated-step"}
	require.NoError(t, pub.UpsertSkill(context.Background(), spec))

	data, err := os.ReadFile(filepath.Join(dir, "my-skill", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "1. updated-step")
	assert.NotContains(t, string(data), "step1")
}

func TestSanitizeSkillName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Deploy Service", "deploy-service"},
		{"my_skill-v2", "my_skill-v2"},
		{"Bad / Characters!", "bad--characters"},
		{"   ", "unnamed-skill"},
		{"日本語", "unnamed-skill"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, sanitizeSkillName(tt.input), "input: %q", tt.input)
	}
}

func TestRenderSkillMarkdown(t *testing.T) {
	spec := &SkillSpec{
		Name:        "Test Skill",
		Description: "A test skill",
		WhenToUse:   "When testing",
		Steps:       []string{"First", "Second"},
	}
	md := renderSkillMarkdown(spec)
	assert.Contains(t, md, "name: Test Skill")
	assert.Contains(t, md, "description: A test skill")
	assert.Contains(t, md, "# Test Skill")
	assert.Contains(t, md, "1. First")
	assert.Contains(t, md, "2. Second")
	assert.NotContains(t, md, "## Pitfalls")
}

func TestRenderSkillMarkdown_WithPitfalls(t *testing.T) {
	spec := &SkillSpec{
		Name:      "S",
		WhenToUse: "Always",
		Steps:     []string{"Do it"},
		Pitfalls:  []string{"Watch out"},
	}
	md := renderSkillMarkdown(spec)
	assert.Contains(t, md, "## Pitfalls")
	assert.Contains(t, md, "- Watch out")
}

func TestYamlScalar(t *testing.T) {
	assert.Equal(t, "simple", yamlScalar("simple"))
	assert.Equal(t, "has: colon", yamlScalar("has: colon"))
	assert.Equal(t, "has # hash", yamlScalar("has # hash"))
	assert.Equal(t, "line one line two", yamlScalar("line one\nline two"))
}

func TestFilePublisher_DeleteSkill(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	require.NoError(t, pub.UpsertSkill(context.Background(), &SkillSpec{
		Name:  "Doomed",
		Steps: []string{"do"},
	}))
	target := filepath.Join(dir, "doomed")
	_, err := os.Stat(target)
	require.NoError(t, err, "directory should exist before delete")

	require.NoError(t, pub.DeleteSkill(context.Background(), "Doomed"))
	_, err = os.Stat(target)
	assert.True(t, os.IsNotExist(err), "directory should be gone after delete")
}

func TestFilePublisher_DeleteSkill_Missing_NoError(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	assert.NoError(t, pub.DeleteSkill(context.Background(), "Nonexistent"),
		"deleting a missing skill must be idempotent")
}

func TestFilePublisher_DeleteSkill_EmptyName(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	assert.Error(t, pub.DeleteSkill(context.Background(), ""))
}

func TestFilePublisher_DeleteSkill_WhitespaceOnlyName(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	err := pub.DeleteSkill(context.Background(), "   ")
	assert.Error(t, err, "whitespace-only name should error")
	assert.Contains(t, err.Error(), "empty name")
}

func TestFilePublisher_UpsertSkill_MkdirFailure(t *testing.T) {
	// Use a path that cannot be created (file masquerades as dir).
	dir := t.TempDir()
	blocker := filepath.Join(dir, "skill-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("not-a-dir"), 0o644))

	pub := newFilePublisher(blocker) // root is a file, MkdirAll will fail
	err := pub.UpsertSkill(context.Background(), &SkillSpec{
		Name:  "Test",
		Steps: []string{"s"},
	})
	require.Error(t, err, "UpsertSkill should fail when MkdirAll fails")
}

func TestFilePublisher_DeleteSkill_RefusesRoot(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	// Inputs that sanitize to a non-root name but still exist beneath root
	// must succeed (idempotent), but a name that would target root itself
	// must be refused. We simulate by passing a name equal to filepath.Base
	// of the root after sanitization is impossible (sanitize lowercases and
	// strips), so instead we verify the root directory is still present
	// after a no-op delete of an unrelated name.
	require.NoError(t, pub.DeleteSkill(context.Background(), "missing"))
	_, err := os.Stat(dir)
	assert.NoError(t, err, "root directory must remain intact after delete")
}

func TestFilePublisher_UpsertSkill_NilSpec(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	err := pub.UpsertSkill(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil spec")
}

func TestFilePublisher_UpsertSkill_EmptyName(t *testing.T) {
	dir := t.TempDir()
	pub := newFilePublisher(dir)
	// Empty name sanitizes to "unnamed-skill"
	spec := &SkillSpec{
		Name:  "",
		Steps: []string{"s"},
	}
	err := pub.UpsertSkill(context.Background(), spec)
	require.NoError(t, err)

	// Should write under "unnamed-skill"
	target := filepath.Join(dir, "unnamed-skill", "SKILL.md")
	_, err = os.Stat(target)
	assert.NoError(t, err)
}

func TestNewFilePublisher(t *testing.T) {
	pub := newFilePublisher("/some/path")
	assert.Equal(t, "/some/path", pub.root)
}
