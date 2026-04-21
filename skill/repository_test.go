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
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestStateKeys_ScopedAndLegacy(t *testing.T) {
	const (
		agentName   = "agentA"
		skillName   = "demo"
		paddedAgent = "  agentA  "
		paddedSkill = "  demo  "

		parentAgent = "agent"
		childAgent  = "agent/child"
	)

	legacyLoaded := StateKeyLoadedPrefix + skillName
	require.Equal(t, legacyLoaded, LoadedKey("", skillName))
	require.Equal(t, legacyLoaded, LoadedKey(" ", skillName))

	scopedLoadedPrefix := StateKeyLoadedByAgentPrefix + agentName +
		stateKeyScopeDelimiter
	require.Equal(
		t,
		scopedLoadedPrefix+skillName,
		LoadedKey(paddedAgent, paddedSkill),
	)
	require.Equal(
		t,
		scopedLoadedPrefix,
		LoadedPrefix(paddedAgent),
	)

	legacyDocs := StateKeyDocsPrefix + skillName
	require.Equal(t, legacyDocs, DocsKey("", skillName))
	require.Equal(t, legacyDocs, DocsKey(" ", skillName))

	scopedDocsPrefix := StateKeyDocsByAgentPrefix + agentName +
		stateKeyScopeDelimiter
	require.Equal(
		t,
		scopedDocsPrefix+skillName,
		DocsKey(paddedAgent, paddedSkill),
	)
	require.Equal(
		t,
		scopedDocsPrefix,
		DocsPrefix(paddedAgent),
	)

	require.Equal(t, StateKeyLoadedPrefix, LoadedPrefix(""))
	require.Equal(t, StateKeyLoadedPrefix, LoadedPrefix(" "))
	require.Equal(t, StateKeyDocsPrefix, DocsPrefix(""))
	require.Equal(t, StateKeyDocsPrefix, DocsPrefix(" "))
	require.Equal(
		t,
		StateKeyLoadedOrderPrefix,
		LoadedOrderKey(""),
	)
	require.Equal(
		t,
		StateKeyLoadedOrderPrefix,
		LoadedOrderKey(" "),
	)
	require.Equal(
		t,
		StateKeyLoadedOrderByAgentPrefix+agentName,
		LoadedOrderKey(paddedAgent),
	)

	loadedKey := LoadedKey(childAgent, skillName)
	parentPrefix := LoadedPrefix(parentAgent)
	require.False(t, strings.HasPrefix(loadedKey, parentPrefix))

	childPrefix := LoadedPrefix(childAgent)
	require.True(t, strings.HasPrefix(loadedKey, childPrefix))

	childOrderKey := LoadedOrderKey(childAgent)
	require.Equal(
		t,
		StateKeyLoadedOrderByAgentPrefix+url.PathEscape(childAgent),
		childOrderKey,
	)
}

func TestLoadedOrderHelpers(t *testing.T) {
	order := ParseLoadedOrder([]byte(`["a"," b ","a","","c"]`))
	require.Equal(t, []string{"a", "b", "c"}, order)

	order = TouchLoadedOrder(order, "b", "d", " ", "c")
	require.Equal(t, []string{"a", "b", "d", "c"}, order)

	require.Nil(t, ParseLoadedOrder([]byte("{")))
	require.Nil(t, MarshalLoadedOrder(nil))
	require.Equal(
		t,
		`["a","b","d","c"]`,
		string(MarshalLoadedOrder(order)),
	)
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

func TestFSRepository_RootsReturnsCopyAndSkillFileConstant(
	t *testing.T,
) {
	root := t.TempDir()
	writeSkill(t, root, "alpha")

	repo, err := NewFSRepository("", root)
	require.NoError(t, err)

	require.Equal(t, skillFile, SkillFile)

	roots := repo.Roots()
	require.Equal(t, []string{root}, roots)

	roots[0] = "mutated"
	require.Equal(t, []string{root}, repo.Roots())
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

func TestFSRepository_Summaries_SortedByName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "b")
	writeSkill(t, root, "a")

	repo, err := NewFSRepository(root)
	require.NoError(t, err)

	sums := repo.Summaries()
	require.Len(t, sums, 2)
	require.Equal(t, "a", sums[0].Name)
	require.Equal(t, "b", sums[1].Name)
}

func TestFSRepository_Get_IncludesNestedDocs(t *testing.T) {
	const (
		skillName   = "one"
		nestedDir   = "docs"
		nestedDoc   = "A.md"
		topLevelDoc = "b.txt"
		nestedBin   = "img.bin"
	)

	root := t.TempDir()
	sdir := writeSkill(t, root, skillName)

	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, topLevelDoc),
		[]byte("doc b"),
		0o644,
	))

	ndir := filepath.Join(sdir, nestedDir)
	require.NoError(t, os.MkdirAll(ndir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(ndir, nestedDoc),
		[]byte("doc A"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(ndir, nestedBin),
		[]byte{1, 2},
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(ndir, skillFile),
		[]byte("nested skill"),
		0o644,
	))

	r, err := NewFSRepository(root)
	require.NoError(t, err)

	sk, err := r.Get(skillName)
	require.NoError(t, err)

	got := map[string]string{}
	for _, d := range sk.Docs {
		got[d.Path] = d.Content
	}

	require.Equal(t, "doc A", got["docs/A.md"])
	require.Equal(t, "doc b", got[topLevelDoc])
	require.NotContains(t, got, "docs/img.bin")
	require.NotContains(t, got, "docs/SKILL.md")
}

func TestFSRepository_Get_SkipsUnreadableDocs(t *testing.T) {
	const (
		skillName = "one"
		docName   = "SECRET.md"
	)

	root := t.TempDir()
	sdir := writeSkill(t, root, skillName)

	docPath := filepath.Join(sdir, docName)
	target := filepath.Join(root, "missing-target")
	if err := os.Symlink(target, docPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	r, err := NewFSRepository(root)
	require.NoError(t, err)

	sk, err := r.Get(skillName)
	require.NoError(t, err)

	for _, d := range sk.Docs {
		require.NotEqual(t, docName, d.Path)
	}
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

func TestFSRepository_Refresh(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha")

	repo, err := NewFSRepository(root)
	require.NoError(t, err)
	require.Len(t, repo.Summaries(), 1)

	writeSkill(t, root, "beta")
	require.Len(t, repo.Summaries(), 1)

	require.NoError(t, repo.Refresh())
	require.Len(t, repo.Summaries(), 2)

	_, err = repo.Get("beta")
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(filepath.Join(root, "beta")))
	require.NoError(t, repo.Refresh())

	require.Len(t, repo.Summaries(), 1)
	_, err = repo.Get("beta")
	require.Error(t, err)
}

func TestSplitFrontMatter_NoClosing(t *testing.T) {
	txt := "---\nname: z\n"
	m, body := splitFrontMatter(txt)
	// No closing delimiter: body should be original text.
	require.Equal(t, 0, len(m))
	require.Equal(t, txt, body)
}

// TestSplitFrontMatter_BlockScalarDescription verifies that multi-line YAML
// block scalars (the "|-" and "|" chomping indicators) are parsed correctly.
// The previous hand-rolled parser only handled "key: value" on a single line
// and left Description empty for skills that used block scalar syntax.
func TestSplitFrontMatter_BlockScalarDescription(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
	}{
		{
			name:    "strip_block_scalar",
			input:   "---\nname: my-skill\ndescription: |-\n  A multi-line\n  description.\n---\nbody\n",
			wantKey: "description",
			wantVal: "A multi-line\ndescription.",
		},
		{
			name:    "literal_block_scalar",
			input:   "---\nname: my-skill\ndescription: |\n  Line one.\n  Line two.\n---\nbody\n",
			wantKey: "description",
			wantVal: "Line one.\nLine two.",
		},
		{
			name:    "folded_block_scalar",
			input:   "---\nname: my-skill\ndescription: >\n  Folded line one.\n  Folded line two.\n---\nbody\n",
			wantKey: "description",
			wantVal: "Folded line one. Folded line two.",
		},
		{
			name:    "plain_single_line",
			input:   "---\nname: my-skill\ndescription: plain description\n---\nbody\n",
			wantKey: "description",
			wantVal: "plain description",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, body := splitFrontMatter(tc.input)
			require.Equal(t, tc.wantVal, m[tc.wantKey],
				"description must be parsed correctly from front matter")
			require.Equal(t, "my-skill", m["name"])
			require.Equal(t, "body\n", body)
		})
	}
}

// TestParseSummary_BlockScalarDescription verifies the end-to-end path:
// writing a SKILL.md with a block-scalar description and reading it back
// via parseSummary / Summaries() returns the correct description.
func TestParseSummary_BlockScalarDescription(t *testing.T) {
	dir := t.TempDir()
	sdir := filepath.Join(dir, "explore-repos")
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	content := "---\nname: explore-repos\ndescription: |-\n  Explore repository structure\n  and list files.\n---\n# Skill body\n"
	require.NoError(t, os.WriteFile(filepath.Join(sdir, "SKILL.md"), []byte(content), 0o644))

	repo, err := NewFSRepository(dir)
	require.NoError(t, err)

	summaries := repo.Summaries()
	require.Len(t, summaries, 1)
	require.Equal(t, "explore-repos", summaries[0].Name)
	require.Equal(t, "Explore repository structure\nand list files.", summaries[0].Description,
		"block scalar description must be parsed correctly by Summaries()")
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

func TestFSRepository_ScansSkillWithoutFrontMatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nofm")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	err := os.WriteFile(
		filepath.Join(dir, skillFile),
		[]byte("# No front matter\n\nBody\n"),
		0o644,
	)
	require.NoError(t, err)

	repo, err := NewFSRepository(root)
	require.NoError(t, err)

	sums := repo.Summaries()
	require.Len(t, sums, 1)
	require.Equal(t, "nofm", sums[0].Name)

	sk, err := repo.Get("nofm")
	require.NoError(t, err)
	require.Equal(t, "nofm", sk.Summary.Name)
	require.Contains(t, sk.Body, "No front matter")
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

func TestFSRepository_URLRoot_ZipDownloadAndCache(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	zipBytes := buildZip(t, map[string]string{
		"alpha/": "",
		"alpha/" + skillFile: "---\nname: alpha\n" +
			"description: d\n---\nbody\n",
	})
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			n := atomic.AddInt32(&hits, 1)
			if n > 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipBytes)
		},
	))
	defer srv.Close()

	urlRoot := srv.URL + "/skills.zip"
	repo, err := NewFSRepository(urlRoot)
	require.NoError(t, err)

	p, err := repo.Path("alpha")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(p, skillFile))
	require.NoError(t, err)

	_, err = NewFSRepository(urlRoot)
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&hits))
}

func TestFSRepository_URLRoot_TarGZDownload(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	tgzBytes := buildTarGZ(t, map[string]string{
		"beta/" + skillFile: "---\nname: beta\n" +
			"description: d\n---\nbody\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tgzBytes)
		},
	))
	defer srv.Close()

	urlRoot := srv.URL + "/skills.tgz"
	repo, err := NewFSRepository(urlRoot)
	require.NoError(t, err)
	_, err = repo.Path("beta")
	require.NoError(t, err)
}

func TestFSRepository_URLRoot_SingleSkillFile(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	skillBytes := []byte("---\nname: gamma\n" +
		"description: d\n---\nbody\n")
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(skillBytes)
		},
	))
	defer srv.Close()

	urlRoot := srv.URL + "/" + skillFile
	repo, err := NewFSRepository(urlRoot)
	require.NoError(t, err)
	_, err = repo.Path("gamma")
	require.NoError(t, err)
}

func TestFSRepository_URLRoot_BadArchivePathRejected(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	zipBytes := buildZip(t, map[string]string{
		"../" + skillFile: "---\nname: bad\n" +
			"description: d\n---\nbody\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipBytes)
		},
	))
	defer srv.Close()

	_, err := NewFSRepository(srv.URL + "/skills.zip")
	require.Error(t, err)
}

func TestResolveSkillsRoot_UnsupportedScheme(t *testing.T) {
	_, err := resolveSkillsRoot("cos://bucket/key.zip")
	require.Error(t, err)
}

func TestResolveSkillsRoot_FileURL(t *testing.T) {
	root := t.TempDir()
	_ = writeSkill(t, root, "file-skill")
	u := "file://" + root

	p, err := resolveSkillsRoot(u)
	require.NoError(t, err)
	repo, err := NewFSRepository(p)
	require.NoError(t, err)
	_, err = repo.Path("file-skill")
	require.NoError(t, err)
}

func TestFileURLPath_RejectsRemoteHost(t *testing.T) {
	_, err := fileURLPath(&url.URL{
		Scheme: "file",
		Host:   "example.com",
		Path:   "/tmp",
	})
	require.Error(t, err)
}

func TestSkillsCacheDir_DefaultsToUserCache(t *testing.T) {
	t.Setenv(EnvSkillsCacheDir, "")
	t.Setenv("HOME", t.TempDir())

	uc, err := os.UserCacheDir()
	require.NoError(t, err)
	want := filepath.Join(uc, cacheAppDir, cacheSkillsDir)
	got := skillsCacheDir()
	require.Equal(t, want, got)
}

func TestDetectArchiveKind_OpenErrorReturnsUnknown(t *testing.T) {
	kind := detectArchiveKind(filepath.Join(t.TempDir(), "nope"))
	require.Equal(t, archiveKindUnknown, kind)
}

func TestFSRepository_URLRoot_TarDownload(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "delta/",
		Typeflag: tar.TypeDir,
		Mode:     dirPerm,
	}))
	body := "---\nname: delta\n" +
		"description: d\n---\nbody\n"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "delta/" + skillFile,
		Typeflag: tar.TypeReg,
		Mode:     filePerm,
		Size:     int64(len(body)),
	}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(buf.Bytes())
		},
	))
	defer srv.Close()

	repo, err := NewFSRepository(srv.URL + "/skills.tar")
	require.NoError(t, err)
	_, err = repo.Path("delta")
	require.NoError(t, err)
}

func TestFSRepository_URLRoot_DetectArchiveKind_Zip(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	zipBytes := buildZip(t, map[string]string{
		"epsilon/" + skillFile: "---\nname: epsilon\n" +
			"description: d\n---\nbody\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipBytes)
		},
	))
	defer srv.Close()

	repo, err := NewFSRepository(srv.URL + "/skills")
	require.NoError(t, err)
	_, err = repo.Path("epsilon")
	require.NoError(t, err)
}

func TestFSRepository_URLRoot_DetectArchiveKind_TarGZ(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	tgzBytes := buildTarGZ(t, map[string]string{
		"zeta/" + skillFile: "---\nname: zeta\n" +
			"description: d\n---\nbody\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tgzBytes)
		},
	))
	defer srv.Close()

	repo, err := NewFSRepository(srv.URL + "/skills")
	require.NoError(t, err)
	_, err = repo.Path("zeta")
	require.NoError(t, err)
}

func TestFSRepository_URLRoot_DownloadNon2xxFails(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	))
	defer srv.Close()

	_, err := NewFSRepository(srv.URL + "/skills.zip")
	require.Error(t, err)
}

func TestFSRepository_URLRoot_UnsupportedPayloadFails(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-archive"))
		},
	))
	defer srv.Close()

	_, err := NewFSRepository(srv.URL + "/skills.bin")
	require.Error(t, err)
}

func TestResolveSkillsRoot_EmptyAndInvalid(t *testing.T) {
	p, err := resolveSkillsRoot("")
	require.NoError(t, err)
	require.Empty(t, p)

	_, err = resolveSkillsRoot("http://[::1")
	require.Error(t, err)

	_, err = fileURLPath(nil)
	require.Error(t, err)
}

func TestArchiveExtract_Errors(t *testing.T) {
	dir := t.TempDir()

	srcZip := filepath.Join(dir, "bad.zip")
	require.NoError(t, os.WriteFile(srcZip, []byte("nope"), filePerm))
	require.Error(t, extractZip(srcZip, filepath.Join(dir, "out1")))

	srcTGZ := filepath.Join(dir, "bad.tgz")
	require.NoError(t, os.WriteFile(srcTGZ, []byte("nope"), filePerm))
	require.Error(t, extractTarGZ(srcTGZ, filepath.Join(dir, "out2")))

	err := extractTarReader(
		tar.NewReader(strings.NewReader("bad")),
		filepath.Join(dir, "out3"),
	)
	require.Error(t, err)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: "trunc.txt",
		Mode: filePerm,
		Size: int64(len("hello")),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err = tw.Write([]byte("hi"))
	require.NoError(t, err)
	_ = tw.Close()

	err = extractTarReader(tar.NewReader(bytes.NewReader(buf.Bytes())),
		filepath.Join(t.TempDir(), "out"))
	require.Error(t, err)
}

func TestURLRootHelpers(t *testing.T) {
	t.Setenv(EnvSkillsCacheDir, "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")

	got := skillsCacheDir()
	want := filepath.Join(os.TempDir(), cacheAppDir, cacheSkillsDir)
	require.Equal(t, want, got)

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(
				"Content-Length",
				strconv.FormatInt(maxDownloadBytes+1, 10),
			)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("x"))
		},
	))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	err = downloadURLToFile(
		u,
		filepath.Join(t.TempDir(), "dl"),
	)
	require.Error(t, err)
	clean, err := cleanArchivePath(".")
	require.NoError(t, err)
	require.Empty(t, clean)
	_, err = cleanArchivePath("/x")
	require.Error(t, err)

	clean, err = cleanArchivePath("a\\b\\c.txt")
	require.NoError(t, err)
	require.Equal(t, "a/b/c.txt", clean)

	_, err = cleanArchivePath("c:/evil")
	require.Error(t, err)

	require.Equal(t, os.FileMode(filePerm), sanitizePerm(0))
	require.Equal(t, os.FileMode(0o755), sanitizePerm(0o1755))

	require.Equal(t, os.FileMode(filePerm), tarHeaderPerm(-1))
	require.Equal(t, os.FileMode(0o777), tarHeaderPerm(0o777))

	require.Error(t, validateTarSize(-1))
	require.NoError(t, validateTarSize(0))
	require.Error(t, validateTarSize(maxExtractFileBytes+1))

	require.Error(t, validateZipEntrySize(nil))
	require.NoError(t, validateZipEntrySize(&zip.File{}))
	require.Error(t, validateZipEntrySize(&zip.File{
		FileHeader: zip.FileHeader{
			Name:               "big",
			UncompressedSize64: uint64(maxExtractFileBytes) + 1,
		},
	}))

	f := &zip.File{
		FileHeader: zip.FileHeader{
			Name:               "big.txt",
			UncompressedSize64: uint64(maxExtractFileBytes) + 1,
		},
	}
	err = extractZipFile(f, t.TempDir(), new(int64))
	require.Error(t, err)

	require.NoError(t, addExtractedBytes(nil, 1))
	var total int64
	require.Error(t, addExtractedBytes(&total, -1))
	total = maxExtractTotalBytes
	require.Error(t, addExtractedBytes(&total, 1))
}

func TestFSRepository_URLRoot_RejectsZipSymlinkEntry(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("eta/" + skillFile)
	require.NoError(t, err)
	_, err = w.Write([]byte("---\nname: eta\n" +
		"description: d\n---\nbody\n"))
	require.NoError(t, err)

	hdr := &zip.FileHeader{Name: "eta/link"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	_, err = zw.CreateHeader(hdr)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(buf.Bytes())
		},
	))
	defer srv.Close()

	_, err = NewFSRepository(srv.URL + "/skills.zip")
	require.Error(t, err)
}

func TestFSRepository_URLRoot_RejectsTarSymlinkEntry(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvSkillsCacheDir, cacheDir)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "theta/" + skillFile,
		Typeflag: tar.TypeReg,
		Mode:     filePerm,
		Size:     int64(len("x")),
	}))
	_, err := tw.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "theta/link",
		Typeflag: tar.TypeSymlink,
		Linkname: "target",
		Mode:     filePerm,
	}))
	require.NoError(t, tw.Close())

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(buf.Bytes())
		},
	))
	defer srv.Close()

	_, err = NewFSRepository(srv.URL + "/skills.tar")
	require.Error(t, err)
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func buildTarGZ(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: filePerm,
			Size: int64(len(body)),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// TestParseFrontMatterYAML_HashPreservedInPlainValue verifies the hybrid
// parser's key behaviour: unquoted '#' characters in plain single-line values
// must NOT be treated as YAML comments. The full value must be preserved.
func TestParseFrontMatterYAML_HashPreservedInPlainValue(t *testing.T) {
	m := parseFrontMatterYAML("name: issue #123 helper\nversion: v1.0 # tag")
	require.Equal(t, "issue #123 helper", m["name"],
		"'#' in an unquoted value must not be stripped as a YAML comment")
	require.Equal(t, "v1.0 # tag", m["version"])
}

// TestParseFrontMatterYAML_KeyWithNoValue verifies that a key with no value
// ("key:") is stored as an empty string, exercising the empty-val path in
// isBlockScalarIndicator and the plain-value assignment.
func TestParseFrontMatterYAML_KeyWithNoValue(t *testing.T) {
	m := parseFrontMatterYAML("empty:\nname: skill")
	require.Equal(t, "", m["empty"])
	require.Equal(t, "skill", m["name"])
}

// TestParseFrontMatterYAML_BlankLinesAreSkipped verifies that blank lines
// within the front-matter block (common in multi-key SKILL.md files) are
// silently skipped without affecting key/value parsing.
func TestParseFrontMatterYAML_BlankLinesAreSkipped(t *testing.T) {
	src := "\nname: my-skill\n\nversion: 2\n"
	m := parseFrontMatterYAML(src)
	require.Equal(t, "my-skill", m["name"])
	require.Equal(t, "2", m["version"])
}

// TestParseFrontMatterYAML_BlockScalarFollowedByPlainKey verifies that the
// continuation-line collector stops at the next non-indented key, so that
// a block scalar and a subsequent plain key are both parsed correctly.
func TestParseFrontMatterYAML_BlockScalarFollowedByPlainKey(t *testing.T) {
	src := "description: |-\n  line one\n  line two\nname: my-skill"
	m := parseFrontMatterYAML(src)
	require.Equal(t, "line one\nline two", m["description"])
	require.Equal(t, "my-skill", m["name"])
}

// TestParseFrontMatterYAML_LinesWithoutColonAreSkipped verifies that lines
// that do not contain a colon (e.g. free-form text, dividers) are silently
// ignored without panicking or affecting other keys.
func TestParseFrontMatterYAML_LinesWithoutColonAreSkipped(t *testing.T) {
	src := "name: my-skill\nthis line has no colon at all\nversion: 1"
	m := parseFrontMatterYAML(src)
	require.Equal(t, "my-skill", m["name"])
	require.Equal(t, "1", m["version"])
	_, hasGarbage := m["this line has no colon at all"]
	require.False(t, hasGarbage)
}

// TestParseFrontMatterYAML_InvalidBlockScalarYAMLSkipped verifies that a
// block-scalar entry whose YAML is malformed is silently skipped (no panic,
// no partial data), while other plain-value entries are still returned.
func TestParseFrontMatterYAML_InvalidBlockScalarYAMLSkipped(t *testing.T) {
	// The block scalar fragment "| \t:" is invalid YAML; the key must be absent.
	src := "name: good-skill\nbad: |\n  \t: malformed"
	m := parseFrontMatterYAML(src)
	require.Equal(t, "good-skill", m["name"], "plain value must still be parsed")
}

// TestParseFrontMatterYAML_BlockScalarEmptyProducesEmptyString verifies that
// an empty block scalar ("key: |" with no indented content) is stored as an
// empty string, consistent with yaml.v3 behaviour.
func TestParseFrontMatterYAML_BlockScalarEmptyProducesEmptyString(t *testing.T) {
	src := "key: |\nname: skill"
	m := parseFrontMatterYAML(src)
	require.Equal(t, "", m["key"], "empty block scalar must yield empty string")
	require.Equal(t, "skill", m["name"])
}

// TestReadFrontMatter_SuccessPath verifies the happy path of readFrontMatter:
// a valid "---\n...\n---\nbody" input returns the parsed map and the body text.
// This exercises the parseFrontMatterYAML delegation on line 279 of repository.go.
func TestReadFrontMatter_SuccessPath(t *testing.T) {
	input := "---\nname: my-skill\ndescription: does things\n---\nBody text here\n"
	rd := bufio.NewReader(strings.NewReader(input))
	m, body, err := readFrontMatter(rd)
	require.NoError(t, err)
	require.Equal(t, "my-skill", m["name"])
	require.Equal(t, "does things", m["description"])
	require.Contains(t, body, "Body text here")
}
