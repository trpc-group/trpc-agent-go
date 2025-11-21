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
	"errors"
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
	want, err := filepath.EvalSymlinks(sdir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = r.Path("missing")
	require.Error(t, err)
}

func TestFSRepository_Path_WithSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(target, 0o755))
	sdir := writeSkill(t, target, "alpha")

	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	r, err := NewFSRepository(link)
	require.NoError(t, err)

	p, err := r.Path("alpha")
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(sdir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	require.Equal(t, want, got)
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

func TestFSRepository_DuplicateSkill_PrefersFirst(t *testing.T) {
	r1 := t.TempDir()
	r2 := t.TempDir()
	// Same skill name in two roots; body texts differ.
	sdir1 := writeSkill(t, r1, "alpha")
	sdir2 := writeSkill(t, r2, "alpha")
	// modify bodies to distinguish
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir1, skillFile), []byte(
			"---\nname: alpha\n---\nfrom root1\n",
		), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir2, skillFile), []byte(
			"---\nname: alpha\n---\nfrom root2\n",
		), 0o644,
	))

	repo, err := NewFSRepository(r1, r2)
	require.NoError(t, err)

	sk, err := repo.Get("alpha")
	require.NoError(t, err)
	require.Contains(t, sk.Body, "from root1")
}

func TestSplitFrontMatter_NoClosing(t *testing.T) {
	txt := "---\nname: z\n"
	m, body := splitFrontMatter(txt)
	// No closing delimiter: body should be original text.
	require.Equal(t, 0, len(m))
	require.Equal(t, txt, body)
}

// errAfterReader returns one line then a non-EOF error to exercise the
// ioReadAll branch that returns accumulated text on unexpected errors.
type errAfterReader struct {
	gave bool
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	if !e.gave {
		e.gave = true
		copy(p, []byte("A\n"))
		return 2, nil
	}
	return 0, errors.New("boom")
}

func TestIOReadAll_NonEOFErrorReturnsAccumulated(t *testing.T) {
	rd := bufio.NewReader(&errAfterReader{})
	s, err := ioReadAll(rd)
	require.NoError(t, err)
	require.Equal(t, "A\n", s)
}

func TestFSRepository_Summaries_IgnoresBrokenAfterScan(t *testing.T) {
	root := t.TempDir()
	sdir := writeSkill(t, root, "alpha")
	repo, err := NewFSRepository(root)
	require.NoError(t, err)

	// Corrupt SKILL.md so parseSummary fails during Summaries.
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, skillFile), []byte("not-front-matter"), 0o644,
	))
	sums := repo.Summaries()
	// Should not panic and simply skip the broken entry.
	if len(sums) > 0 {
		for _, s := range sums {
			require.NotEmpty(t, s.Name)
		}
	}
}

func TestFSRepository_Get_MissingSkillError(t *testing.T) {
	root := t.TempDir()
	repo, err := NewFSRepository(root)
	require.NoError(t, err)

	_, err = repo.Get("nope")
	require.Error(t, err)
}

func TestParseFull_ErrorOnMissingFile(t *testing.T) {
	_, _, err := parseFull(filepath.Join(t.TempDir(),
		"does-not-exist.md"))
	require.Error(t, err)
}

func TestReadFrontMatter_UnclosedReturnsError(t *testing.T) {
	rd := bufio.NewReader(strings.NewReader(
		"---\nkey: v\n"))
	_, _, err := readFrontMatter(rd)
	require.Error(t, err)
}

// closedAfterReader yields one line then os.ErrClosed.
type closedAfterReader struct{ gave bool }

func (c *closedAfterReader) Read(p []byte) (int, error) {
	if !c.gave {
		c.gave = true
		copy(p, []byte("X\n"))
		return 2, nil
	}
	return 0, os.ErrClosed
}

func TestIOReadAll_ClosedReturnsAccumulated(t *testing.T) {
	rd := bufio.NewReader(&closedAfterReader{})
	s, err := ioReadAll(rd)
	require.NoError(t, err)
	require.Equal(t, "X\n", s)
}

func TestFSRepository_Summaries_NameFallback(t *testing.T) {
	root := t.TempDir()
	sdir := writeSkill(t, root, "alpha")
	repo, err := NewFSRepository(root)
	require.NoError(t, err)

	// Remove name from SKILL.md after scan; keep valid front matter.
	err = os.WriteFile(filepath.Join(sdir, skillFile), []byte(
		"---\n# no name now\n"+
			"description: something\n---\nbody\n"), 0o644)
	require.NoError(t, err)

	sums := repo.Summaries()
	require.Len(t, sums, 1)
	require.Equal(t, "alpha", sums[0].Name)
}

func TestFSRepository_Get_ReadSkillFileError(t *testing.T) {
	root := t.TempDir()
	sdir := writeSkill(t, root, "beta")
	repo, err := NewFSRepository(root)
	require.NoError(t, err)

	// Remove SKILL.md to force parseFull read error in Get.
	require.NoError(t, os.Remove(
		filepath.Join(sdir, skillFile)))

	_, err = repo.Get("beta")
	require.Error(t, err)
}

func TestIsDocFile_CaseInsensitive(t *testing.T) {
	require.True(t, isDocFile("README.TXT"))
	require.True(t, isDocFile("manual.MD"))
	require.False(t, isDocFile("image.BIN"))
}
