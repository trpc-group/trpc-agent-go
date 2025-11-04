//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeSkill(t *testing.T, dir, name string) string {
	t.Helper()
	sdir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: d\n---\nbody\n"
	err := os.WriteFile(filepath.Join(sdir, skillFile),
		[]byte(data), 0o644)
	require.NoError(t, err)
	return sdir
}

func TestFSRepository_Path(t *testing.T) {
	root := t.TempDir()
	sdir := writeSkill(t, root, "alpha")

	r, err := NewFSRepository(root)
	require.NoError(t, err)

	p, err := r.Path("alpha")
	require.NoError(t, err)
	require.Equal(t, sdir, p)

	_, err = r.Path("missing")
	require.Error(t, err)
}

func TestFSRepository_Summaries_And_Get_WithDocs(t *testing.T) {
	root := t.TempDir()
	sdir := writeSkill(t, root, "one")
	// Add docs
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "A.md"), []byte("doc A"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "b.txt"), []byte("doc b"), 0o644,
	))
	// Non-doc should be ignored
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "img.bin"), []byte{1, 2}, 0o644,
	))

	r, err := NewFSRepository(root)
	require.NoError(t, err)

	sums := r.Summaries()
	require.Len(t, sums, 1)
	require.Equal(t, "one", sums[0].Name)
	require.Equal(t, "d", sums[0].Description)

	sk, err := r.Get("one")
	require.NoError(t, err)
	require.Equal(t, "one", sk.Summary.Name)
	require.Contains(t, sk.Body, "body")
	// Docs included
	names := map[string]bool{}
	for _, d := range sk.Docs {
		names[d.Path] = true
	}
	require.True(t, names["A.md"])
	require.True(t, names["b.txt"])
	require.False(t, names["img.bin"])
}

func TestParseHelpers_And_DocFlags(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	data := "---\nname: nm\n# cmt\n" +
		"description: desc\n---\nBody here\n"
	require.NoError(t, os.WriteFile(p, []byte(data), 0o644))

	// parseSummary
	sum, err := parseSummary(p)
	require.NoError(t, err)
	require.Equal(t, "nm", sum.Name)
	require.Equal(t, "desc", sum.Description)

	// parseFull
	full, body, err := parseFull(p)
	require.NoError(t, err)
	require.Equal(t, "nm", full.Name)
	require.Contains(t, body, "Body here")

	// readFrontMatter: missing leading '---' should error.
	rd := bufio.NewReader(strings.NewReader("nope\n"))
	_, _, err = readFrontMatter(rd)
	require.Error(t, err)

	// splitFrontMatter variations.
	m, bod := splitFrontMatter("hello")
	require.Equal(t, 0, len(m))
	require.Equal(t, "hello", bod)
	m, bod = splitFrontMatter("---\nname: z\n---\nB")
	require.Equal(t, "z", m["name"])
	require.Equal(t, "B", bod)

	// ioReadAll helper returns the remaining text.
	rd2 := bufio.NewReader(strings.NewReader("A\nB\n"))
	s, err := ioReadAll(rd2)
	require.NoError(t, err)
	require.Equal(t, "A\nB\n", s)

	// isDocFile helper
	require.True(t, isDocFile("x.md"))
	require.True(t, isDocFile("y.TXT"))
	require.False(t, isDocFile("z.bin"))
}
