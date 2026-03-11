//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareReleaseVersions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		Name     string
		Left     string
		Right    string
		Expected int
	}{
		{
			Name:     "equal versions",
			Left:     "v1.2.3",
			Right:    "v1.2.3",
			Expected: 0,
		},
		{
			Name:     "left is newer",
			Left:     "v1.2.4",
			Right:    "v1.2.3",
			Expected: 1,
		},
		{
			Name:     "right is newer",
			Left:     "v1.2.3",
			Right:    "v1.3.0",
			Expected: -1,
		},
		{
			Name:     "missing prefix still compares",
			Left:     "1.2.3",
			Right:    "v1.2.2",
			Expected: 1,
		},
		{
			Name:     "tag prefix is ignored",
			Left:     "openclaw-v1.2.3",
			Right:    "v1.2.3",
			Expected: 0,
		},
		{
			Name:     "invalid values compare lexically",
			Left:     "dev",
			Right:    "v1.2.3",
			Expected: -1,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			require.Equal(
				t,
				tc.Expected,
				compareReleaseVersions(tc.Left, tc.Right),
			)
		})
	}
}

func TestParseUpgradePaths(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	oldHomeFunc := userHomeDirFunc
	userHomeDirFunc = func() (string, error) {
		return home, nil
	}
	t.Cleanup(func() {
		userHomeDirFunc = oldHomeFunc
	})

	paths, err := parseUpgradePaths([]string{
		"--config=/tmp/openclaw.yaml",
		"--state-dir",
		"/tmp/state",
	})
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Clean("/tmp/openclaw.yaml"),
		filepath.Clean(paths.ConfigPath),
	)
	require.Equal(
		t,
		filepath.Clean("/tmp/state"),
		filepath.Clean(paths.StateDir),
	)

	paths, err = parseUpgradePaths(nil)
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Join(home, ".trpc-agent-go", "openclaw", "openclaw.yaml"),
		paths.ConfigPath,
	)
	require.Equal(
		t,
		filepath.Join(home, ".trpc-agent-go", "openclaw"),
		paths.StateDir,
	)
}

func TestParseUpgradePathsRejectsUnknownArg(t *testing.T) {
	t.Parallel()

	_, err := parseUpgradePaths([]string{"--bad-flag"})
	require.Error(t, err)
}

func TestFetchLatestReleaseVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			require.Equal(
				t,
				"/repos/trpc-group/trpc-agent-go/releases",
				r.URL.Path,
			)
			_, _ = w.Write([]byte(`[
				{"tag_name":"v9.9.9"},
				{"tag_name":"openclaw-v1.3.0"},
				{"tag_name":"openclaw-v1.2.0"}
			]`))
		},
	))
	defer server.Close()

	t.Setenv(releaseAPIBaseURLEnvName, server.URL)
	t.Setenv(releaseRepoEnvName, "trpc-group/trpc-agent-go")

	version, err := fetchLatestReleaseVersion(context.Background())
	require.NoError(t, err)
	require.Equal(t, "v1.3.0", version)
}

func TestUpgradeToLatestNoOpWhenAlreadyCurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[{"tag_name":"openclaw-v1.2.3"}]`))
		},
	))
	defer server.Close()

	t.Setenv(releaseAPIBaseURLEnvName, server.URL)
	t.Setenv(releaseRepoEnvName, "trpc-group/trpc-agent-go")

	oldVersion := releaseVersion
	releaseVersion = "v1.2.3"
	t.Cleanup(func() {
		releaseVersion = oldVersion
	})

	oldInstall := installReleaseFunc
	installReleaseFunc = func(
		_ context.Context,
		_ string,
		_ string,
		_ string,
		_ string,
		_ io.Writer,
		_ io.Writer,
	) error {
		t.Fatal("installRelease should not be called")
		return nil
	}
	t.Cleanup(func() {
		installReleaseFunc = oldInstall
	})

	var stdout bytes.Buffer
	err := upgradeToLatest(
		context.Background(),
		&stdout,
		&bytes.Buffer{},
		upgradePaths{
			ConfigPath: "/tmp/openclaw.yaml",
			StateDir:   "/tmp/state",
		},
	)
	require.NoError(t, err)
	require.Contains(t, stdout.String(), "already up to date")
}

func TestUpgradeToLatestInstallsLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[{"tag_name":"openclaw-v9.9.9"}]`))
		},
	))
	defer server.Close()

	t.Setenv(releaseAPIBaseURLEnvName, server.URL)
	t.Setenv(releaseRepoEnvName, "trpc-group/trpc-agent-go")

	oldVersion := releaseVersion
	releaseVersion = "v1.0.0"
	t.Cleanup(func() {
		releaseVersion = oldVersion
	})

	exeDir := t.TempDir()
	oldExecutable := executablePathFunc
	executablePathFunc = func() (string, error) {
		return filepath.Join(exeDir, "openclaw"), nil
	}
	t.Cleanup(func() {
		executablePathFunc = oldExecutable
	})

	var gotVersion string
	var gotBinDir string
	var gotConfigDir string
	var gotStateDir string

	oldInstall := installReleaseFunc
	installReleaseFunc = func(
		_ context.Context,
		version string,
		binDir string,
		configDir string,
		stateDir string,
		_ io.Writer,
		_ io.Writer,
	) error {
		gotVersion = version
		gotBinDir = binDir
		gotConfigDir = configDir
		gotStateDir = stateDir
		return nil
	}
	t.Cleanup(func() {
		installReleaseFunc = oldInstall
	})

	err := upgradeToLatest(
		context.Background(),
		&bytes.Buffer{},
		&bytes.Buffer{},
		upgradePaths{
			ConfigPath: "/tmp/configs/openclaw.yaml",
			StateDir:   "/tmp/state",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "v9.9.9", gotVersion)
	require.Equal(t, exeDir, gotBinDir)
	require.Equal(t, "/tmp/configs", gotConfigDir)
	require.Equal(t, "/tmp/state", gotStateDir)
}

func TestDownloadInstallScript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("#!/usr/bin/env bash\n"))
		},
	))
	defer server.Close()

	t.Setenv(installScriptEnvName, server.URL)

	path, err := downloadInstallScript(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "#!/usr/bin/env bash\n", string(body))
}
