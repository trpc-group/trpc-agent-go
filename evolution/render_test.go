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
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- yamlScalar edge cases ---

func TestYamlScalar_Empty(t *testing.T) {
	assert.Equal(t, "", yamlScalar(""))
}

func TestYamlScalar_Whitespace(t *testing.T) {
	assert.Equal(t, "", yamlScalar("   "))
}

func TestYamlScalar_CarriageReturn(t *testing.T) {
	// \r\n -> space + space -> collapsed by TrimSpace on boundaries but not mid-string
	assert.Equal(t, "line one  line two", yamlScalar("line one\r\nline two"))
}

func TestYamlScalar_MultiNewlines(t *testing.T) {
	assert.Equal(t, "a  b  c", yamlScalar("a\n\nb\n\nc"))
}

func TestYamlScalar_LeadingTrailingWhitespace(t *testing.T) {
	assert.Equal(t, "hello", yamlScalar("  hello  "))
}

// --- sanitizeSkillName edge cases ---

func TestSanitizeSkillName_OnlyDots(t *testing.T) {
	// Dots are stripped, resulting in empty -> "unnamed-skill"
	result := sanitizeSkillName("...")
	assert.Equal(t, "unnamed-skill", result)
}

func TestSanitizeSkillName_Hyphen(t *testing.T) {
	assert.Equal(t, "my-skill", sanitizeSkillName("My Skill"))
}

func TestSanitizeSkillName_Underscore(t *testing.T) {
	assert.Equal(t, "my_skill", sanitizeSkillName("my_skill"))
}

// --- writeFileAtomically edge cases ---

func TestWriteFileAtomically_NonExistentDir(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nonexistent", "subdir", "file.txt")
	err := writeFileAtomically(target, []byte("hello"), 0o644)
	require.Error(t, err, "should fail when directory does not exist")
	assert.Contains(t, err.Error(), "create temp file")
}

func TestWriteFileAtomically_Success(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	err := writeFileAtomically(target, []byte("content"), 0o644)
	require.NoError(t, err)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
}

func TestWriteFileAtomically_Overwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	require.NoError(t, writeFileAtomically(target, []byte("first"), 0o644))
	require.NoError(t, writeFileAtomically(target, []byte("second"), 0o644))

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "second", string(data))
}

func TestWriteFileAtomically_Permission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "perm.txt")
	err := writeFileAtomically(target, []byte("data"), 0o600)
	require.NoError(t, err)

	info, err := os.Stat(target)
	require.NoError(t, err)
	// Check permission (masking out system bits)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// --- RenderSkillMarkdown edge cases ---

func TestRenderSkillMarkdown_EmptySteps(t *testing.T) {
	spec := &SkillSpec{
		Name:        "No Steps",
		Description: "desc",
		WhenToUse:   "never",
		Steps:       nil,
	}
	md := renderSkillMarkdown(spec)
	assert.Contains(t, md, "## Steps")
	// No numbered items should appear
	assert.NotContains(t, md, "1.")
}

func TestRenderSkillMarkdown_EmptyPitfalls(t *testing.T) {
	spec := &SkillSpec{
		Name:        "X",
		Description: "d",
		WhenToUse:   "w",
		Steps:       []string{"step"},
		Pitfalls:    []string{},
	}
	md := renderSkillMarkdown(spec)
	assert.NotContains(t, md, "## Pitfalls")
}

func TestRenderSkillMarkdown_MultiplePitfalls(t *testing.T) {
	spec := &SkillSpec{
		Name:        "X",
		Description: "d",
		WhenToUse:   "w",
		Steps:       []string{"s"},
		Pitfalls:    []string{"p1", "p2", "p3"},
	}
	md := renderSkillMarkdown(spec)
	assert.Contains(t, md, "- p1")
	assert.Contains(t, md, "- p2")
	assert.Contains(t, md, "- p3")
}

func TestRenderSkillMarkdown_SpecialCharsInName(t *testing.T) {
	spec := &SkillSpec{
		Name:        "Deploy: Multi-Region",
		Description: "desc",
		WhenToUse:   "when",
		Steps:       []string{"go"},
	}
	md := renderSkillMarkdown(spec)
	assert.Contains(t, md, "name: Deploy: Multi-Region")
	assert.Contains(t, md, "# Deploy: Multi-Region")
}

func TestWriteFileAtomically_ReadonlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	dir := t.TempDir()
	// Create a readonly subdirectory.
	roDir := filepath.Join(dir, "readonly")
	require.NoError(t, os.MkdirAll(roDir, 0o755))
	require.NoError(t, os.Chmod(roDir, 0o444))
	defer os.Chmod(roDir, 0o755) // cleanup

	target := filepath.Join(roDir, "file.txt")
	err := writeFileAtomically(target, []byte("data"), 0o644)
	require.Error(t, err, "should fail when directory is readonly")
	assert.Contains(t, err.Error(), "create temp file")
}

func TestRenderSkillMarkdown_NewlineInDescription(t *testing.T) {
	spec := &SkillSpec{
		Name:        "Skill",
		Description: "line one\nline two",
		WhenToUse:   "when",
		Steps:       []string{"s"},
	}
	md := renderSkillMarkdown(spec)
	// yamlScalar should collapse newlines
	assert.Contains(t, md, "description: line one line two")
}
