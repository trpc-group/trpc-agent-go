//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNavigationPolicy_BlocksLoopbackByDefault(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{}.Validate("http://127.0.0.1:8080")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked")
}

func TestNavigationPolicy_AllowlistBlocksOtherDomains(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowedDomains: []string{"example.com"},
	}.Validate("https://google.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not allowed")
}

func TestNavigationPolicy_AllowsSubdomain(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowedDomains: []string{"example.com"},
	}.Validate("https://docs.example.com")
	require.NoError(t, err)
}

func TestNavigationPolicy_BlocksFileURLByDefault(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{}.Validate("file:///tmp/test.html")
	require.Error(t, err)
	require.Contains(t, err.Error(), "file URLs")
}

func TestNavigationPolicy_BlocksPrivateNetworkByDefault(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{}.Validate("http://192.168.1.20")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private network")
}

func TestNavigationPolicy_AllowsFileURLWhenEnabled(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowFileURLs: true,
	}.Validate("file:///tmp/test.html")
	require.NoError(t, err)
}

func TestNavigationPolicy_AllowsFileURLUnderAllowedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "image.png")
	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{root}),
	}.Validate("file://" + filepath.ToSlash(target))
	require.NoError(t, err)
}

func TestNavigationPolicy_AllowsLocalhostFileURLUnderAllowedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "image with space.png")
	u := url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(target)}
	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{root}),
	}.Validate(u.String())
	require.NoError(t, err)
}

func TestNavigationPolicy_AllowsFileURLUnderSymlinkedAllowedRoot(
	t *testing.T,
) {
	t.Parallel()

	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	require.NoError(t, os.Mkdir(realRoot, 0o700))
	linkRoot := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(realRoot, linkRoot))
	target := filepath.Join(linkRoot, "nested", "image.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	require.NoError(t, os.WriteFile(target, []byte("png"), 0o600))

	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{linkRoot}),
	}.Validate("file://" + filepath.ToSlash(target))
	require.NoError(t, err)
}

func TestNavigationPolicy_AllowsMissingFileURLUnderSymlinkedRoot(
	t *testing.T,
) {
	t.Parallel()

	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	require.NoError(t, os.Mkdir(realRoot, 0o700))
	linkRoot := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(realRoot, linkRoot))
	target := filepath.Join(linkRoot, "nested", "missing.png")

	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{linkRoot}),
	}.Validate("file://" + filepath.ToSlash(target))
	require.NoError(t, err)
}

func TestNavigationPolicy_BlocksFileURLTraversalOutOfAllowedRoot(
	t *testing.T,
) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(root, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	raw := "file://" + filepath.ToSlash(root) + "/../outside/image.png"

	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{root}),
	}.Validate(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "file URLs")
}

func TestNavigationPolicy_BlocksFileURLOutsideAllowedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	other := filepath.Join(t.TempDir(), "image.png")
	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{root}),
	}.Validate("file://" + filepath.ToSlash(other))
	require.Error(t, err)
	require.Contains(t, err.Error(), "file URLs")
}

func TestNavigationPolicy_BlocksMalformedFileURLPath(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{t.TempDir()}),
	}.Validate("file:///%zz")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid browser url")
}

func TestNavigationPolicy_BlocksRemoteHostFileURL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "image.png")
	u := url.URL{Scheme: "file", Host: "example.com", Path: filepath.ToSlash(target)}
	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{root}),
	}.Validate(u.String())
	require.Error(t, err)
	require.Contains(t, err.Error(), "file URLs")
}

func TestNavigationPolicy_BlocksEmptyFileURLPath(t *testing.T) {
	t.Parallel()

	err := navigationPolicy{
		AllowedFileRoots: normalizeFileRoots([]string{t.TempDir()}),
	}.Validate("file://localhost")
	require.Error(t, err)
	require.Contains(t, err.Error(), "file URLs")
}

func TestNormalizeDomains_DedupesAndTrims(t *testing.T) {
	t.Parallel()

	got := normalizeDomains([]string{
		" Example.com ",
		"example.com.",
		"",
	})
	require.Equal(t, []string{"example.com"}, got)
	require.True(t, hostMatchesDomain("docs.example.com", "example.com"))
	require.False(t, hostMatchesDomain("google.com", "example.com"))
}

func TestNavigationPolicy_AdditionalBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		policy navigationPolicy
		raw    string
		want   string
	}{
		{
			name: "empty url",
			raw:  "",
		},
		{
			name: "invalid url",
			raw:  "http://[::1",
			want: "invalid browser url",
		},
		{
			name: "about page",
			raw:  "about:blank",
		},
		{
			name: "unsupported scheme",
			raw:  "mailto:test@example.com",
			want: "not allowed",
		},
		{
			name: "empty host",
			raw:  "https:///docs",
		},
		{
			name: "loopback allowed",
			policy: navigationPolicy{
				AllowLoopback: true,
			},
			raw: "http://api.localhost:8080",
		},
		{
			name: "private network allowed",
			policy: navigationPolicy{
				AllowPrivateNet: true,
			},
			raw: "http://10.0.0.8",
		},
		{
			name: "blocked domain",
			policy: navigationPolicy{
				BlockedDomains: []string{"example.com"},
			},
			raw:  "https://docs.example.com",
			want: "blocked",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.policy.Validate(tc.raw)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}

	require.True(t, isLoopbackHost("api.localhost"))
}

func TestCleanFilePathRejectsRelativePath(t *testing.T) {
	t.Parallel()

	_, err := cleanFilePath("relative/file.html")
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute")
}

func TestFilePathWithinRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.False(t, filePathWithinRoot(filepath.Join(root, "file.txt"), ""))
	require.True(t, filePathWithinRoot(root, root))
	require.True(t, filePathWithinRoot(filepath.Join(root, "nested", "file.txt"), root))
	require.False(t, filePathWithinRoot(filepath.Join(t.TempDir(), "file.txt"), root))
}

func TestNormalizeFileRoots(t *testing.T) {
	t.Parallel()

	require.Nil(t, normalizeFileRoots(nil))
	require.Nil(t, normalizeFileRoots([]string{"", " \t"}))

	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	require.NoError(t, os.Mkdir(realRoot, 0o700))
	linkRoot := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(realRoot, linkRoot))
	resolvedRoot, err := filepath.EvalSymlinks(realRoot)
	require.NoError(t, err)

	roots := normalizeFileRoots([]string{realRoot, linkRoot, realRoot})
	require.Equal(t, []string{resolvedRoot}, roots)
}
