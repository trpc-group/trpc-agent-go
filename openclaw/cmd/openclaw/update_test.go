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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTopLevelRequestHelpers(t *testing.T) {
	t.Parallel()

	require.True(t, isTopLevelVersionRequest([]string{"version"}))
	require.True(t, isTopLevelVersionRequest([]string{"--version"}))
	require.False(t, isTopLevelVersionRequest(nil))

	require.True(t, isTopLevelUpgradeRequest([]string{"upgrade"}))
	require.False(t, isTopLevelUpgradeRequest(nil))
	require.False(t, isTopLevelUpgradeRequest([]string{"doctor"}))

	require.True(t, isHelpRequest(" --help "))
	require.False(t, isHelpRequest("version"))
}

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
		{
			Name:     "valid beats invalid",
			Left:     "v1.2.3",
			Right:    "dev",
			Expected: 1,
		},
		{
			Name:     "both invalid compare lexically",
			Left:     "beta",
			Right:    "alpha",
			Expected: 1,
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

func TestVersionPart(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, versionPart([]int{1, 2, 3}, 1))
	require.Equal(t, 0, versionPart([]int{1, 2, 3}, 8))
}

func TestParseUpgradePaths(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	oldHomeFunc := userHomeDirFunc
	userHomeDirFunc = func() (string, error) {
		return home, nil
	}
	t.Cleanup(func() {
		userHomeDirFunc = oldHomeFunc
	})
	oldExecutable := executablePathFunc
	executablePathFunc = func() (string, error) {
		return filepath.Join(binDir, "openclaw"), nil
	}
	t.Cleanup(func() {
		executablePathFunc = oldExecutable
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

func TestParseUpgradePathsUsesInstallMetadata(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	metadata := "bin_dir=" + binDir + "\n" +
		"config_dir=/tmp/custom-config\n" +
		"state_dir=/tmp/custom-state\n"
	err := os.WriteFile(
		filepath.Join(binDir, installMetadataFileName),
		[]byte(metadata),
		0o644,
	)
	require.NoError(t, err)

	oldHomeFunc := userHomeDirFunc
	userHomeDirFunc = func() (string, error) {
		return home, nil
	}
	t.Cleanup(func() {
		userHomeDirFunc = oldHomeFunc
	})
	oldExecutable := executablePathFunc
	executablePathFunc = func() (string, error) {
		return filepath.Join(binDir, "openclaw"), nil
	}
	t.Cleanup(func() {
		executablePathFunc = oldExecutable
	})

	paths, err := parseUpgradePaths(nil)
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Clean("/tmp/custom-config/openclaw.yaml"),
		filepath.Clean(paths.ConfigPath),
	)
	require.Equal(
		t,
		filepath.Clean("/tmp/custom-state"),
		filepath.Clean(paths.StateDir),
	)
}

func TestParseUpgradePathsExplicitFlagsOverrideMetadata(t *testing.T) {
	binDir := t.TempDir()
	metadata := "config_dir=/tmp/custom-config\n" +
		"state_dir=/tmp/custom-state\n"
	err := os.WriteFile(
		filepath.Join(binDir, installMetadataFileName),
		[]byte(metadata),
		0o644,
	)
	require.NoError(t, err)

	oldExecutable := executablePathFunc
	executablePathFunc = func() (string, error) {
		return filepath.Join(binDir, "openclaw"), nil
	}
	t.Cleanup(func() {
		executablePathFunc = oldExecutable
	})

	paths, err := parseUpgradePaths([]string{
		"--config", "/tmp/override.yaml",
		"--state-dir", "/tmp/override-state",
	})
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Clean("/tmp/override.yaml"),
		filepath.Clean(paths.ConfigPath),
	)
	require.Equal(
		t,
		filepath.Clean("/tmp/override-state"),
		filepath.Clean(paths.StateDir),
	)
}

func TestParseUpgradePathsRejectsUnknownArg(t *testing.T) {
	t.Parallel()

	_, err := parseUpgradePaths([]string{"--bad-flag"})
	require.Error(t, err)
}

func TestParseUpgradePathsRejectsMissingValue(t *testing.T) {
	t.Parallel()

	_, err := parseUpgradePaths([]string{"--config"})
	require.Error(t, err)

	_, err = parseUpgradePaths([]string{"--state-dir"})
	require.Error(t, err)
}

func TestResolveUpgradeConfigPathUsesEnv(t *testing.T) {
	t.Setenv(openClawConfigEnvName, "./custom.yaml")

	got, err := resolveUpgradeConfigPath("", installMetadata{})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("custom.yaml"), filepath.Base(got))
}

func TestResolveUpgradeConfigPathHomeError(t *testing.T) {
	old := userHomeDirFunc
	userHomeDirFunc = func() (string, error) {
		return "", errors.New("boom")
	}
	t.Cleanup(func() {
		userHomeDirFunc = old
	})

	_, err := resolveUpgradeConfigPath("", installMetadata{})
	require.Error(t, err)
}

func TestResolveUpgradeStateDirHomeError(t *testing.T) {
	old := userHomeDirFunc
	userHomeDirFunc = func() (string, error) {
		return "", errors.New("boom")
	}
	t.Cleanup(func() {
		userHomeDirFunc = old
	})

	_, err := resolveUpgradeStateDir(
		"",
		"",
		installMetadata{},
	)
	require.Error(t, err)
}

func TestResolveUpgradeStateDirUsesConfigStateDir(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "openclaw.yaml")
	err := os.WriteFile(
		configPath,
		[]byte("state_dir: /tmp/from-config\n"),
		0o644,
	)
	require.NoError(t, err)

	stateDir, err := resolveUpgradeStateDir(
		"",
		configPath,
		installMetadata{},
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("/tmp/from-config"), stateDir)
}

func TestResolveUpgradeStateDirIgnoresRelativeConfigStateDir(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "openclaw.yaml")
	err := os.WriteFile(
		configPath,
		[]byte("state_dir: ./.openclaw-state\n"),
		0o644,
	)
	require.NoError(t, err)

	oldHomeFunc := userHomeDirFunc
	userHomeDirFunc = func() (string, error) {
		return home, nil
	}
	t.Cleanup(func() {
		userHomeDirFunc = oldHomeFunc
	})

	stateDir, err := resolveUpgradeStateDir(
		"",
		configPath,
		installMetadata{},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		filepath.Join(home, ".trpc-agent-go", "openclaw"),
		stateDir,
	)
}

func TestReadInstallMetadataRejectsInvalidLine(t *testing.T) {
	t.Parallel()

	binDir := t.TempDir()
	err := os.WriteFile(
		filepath.Join(binDir, installMetadataFileName),
		[]byte("bad-line\n"),
		0o644,
	)
	require.NoError(t, err)

	_, err = readInstallMetadata(binDir)
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

func TestFetchLatestReleaseVersionErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Query().Get("mode") {
			case "bad-json":
				_, _ = w.Write([]byte("{"))
			default:
				_, _ = w.Write([]byte(`[{"tag_name":"v1.2.3"}]`))
			}
		},
	))
	defer server.Close()

	t.Setenv(releaseRepoEnvName, "trpc-group/trpc-agent-go")

	t.Setenv(releaseAPIBaseURLEnvName, server.URL+"?mode=bad-json")
	_, err := fetchLatestReleaseVersion(context.Background())
	require.Error(t, err)

	t.Setenv(releaseAPIBaseURLEnvName, server.URL)
	_, err = fetchLatestReleaseVersion(context.Background())
	require.Error(t, err)
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

func TestUpgradeToLatestReturnsBinaryDirError(t *testing.T) {
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

	oldExecutable := executablePathFunc
	executablePathFunc = func() (string, error) {
		return "", errors.New("boom")
	}
	t.Cleanup(func() {
		executablePathFunc = oldExecutable
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
	require.Error(t, err)
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

func TestRunUpgradeCommand(t *testing.T) {
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

	oldInstall := installReleaseFunc
	installReleaseFunc = func(
		_ context.Context,
		_ string,
		_ string,
		_ string,
		_ string,
		_, _ io.Writer,
	) error {
		return nil
	}
	t.Cleanup(func() {
		installReleaseFunc = oldInstall
	})

	require.Equal(t, 0, runUpgradeCommand([]string{"--help"}))
	require.Equal(t, 2, runUpgradeCommand([]string{"--bad-flag"}))
	require.Equal(
		t,
		0,
		runUpgradeCommand([]string{"--state-dir", t.TempDir()}),
	)
}

func TestInstallRelease(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	script := "#!/usr/bin/env bash\n" +
		"set -e\n" +
		"printf '%s\\n' \"$@\" > \"$ARGS_FILE\"\n"

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(script))
		},
	))
	defer server.Close()

	t.Setenv(installScriptEnvName, server.URL)
	t.Setenv("ARGS_FILE", argsFile)

	err := installRelease(
		context.Background(),
		"v1.2.3",
		"/tmp/bin",
		"/tmp/config",
		"/tmp/state",
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	require.NoError(t, err)

	body, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(
		t,
		"--version\nv1.2.3\n--bin-dir\n/tmp/bin\n--config-dir\n"+
			"/tmp/config\n--state-dir\n/tmp/state\n",
		string(body),
	)
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

func TestDownloadInstallScriptError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
	))
	defer server.Close()

	t.Setenv(installScriptEnvName, server.URL)
	_, err := downloadInstallScript(context.Background())
	require.Error(t, err)
}

func TestFetchReleaseAssetErrors(t *testing.T) {
	t.Parallel()

	_, err := fetchReleaseAsset(context.Background(), "://bad")
	require.Error(t, err)

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
	))
	defer server.Close()

	_, err = fetchReleaseAsset(context.Background(), server.URL)
	require.Error(t, err)
}

func TestOverrideHelpers(t *testing.T) {
	t.Setenv(installScriptEnvName, "https://example.com/install.sh")
	t.Setenv(releaseAPIBaseURLEnvName, "https://api.example.com/")
	t.Setenv(releaseRepoEnvName, "example/openclaw")

	require.Equal(
		t,
		"https://example.com/install.sh",
		installScriptURL(),
	)
	require.Equal(t, "https://api.example.com", releaseAPIBaseURL())
	require.Equal(t, "example/openclaw", releaseRepo())
	require.Contains(t, latestReleaseAPIURL(), "/repos/example/openclaw/")
}

func TestOverrideHelpersDefaults(t *testing.T) {
	t.Setenv(installScriptEnvName, "")
	t.Setenv(releaseAPIBaseURLEnvName, "")
	t.Setenv(releaseRepoEnvName, "")

	require.Equal(t, defaultInstallScriptURL, installScriptURL())
	require.Equal(t, defaultGitHubAPIBaseURL, releaseAPIBaseURL())
	require.Equal(t, defaultReleaseRepo, releaseRepo())
}

func TestCurrentBinaryDir(t *testing.T) {
	oldExecutable := executablePathFunc
	executablePathFunc = func() (string, error) {
		return "/tmp/bin/openclaw", nil
	}
	t.Cleanup(func() {
		executablePathFunc = oldExecutable
	})

	dir, err := currentBinaryDir()
	require.NoError(t, err)
	require.Equal(t, "/tmp/bin", dir)

	executablePathFunc = func() (string, error) {
		return "", errors.New("boom")
	}
	_, err = currentBinaryDir()
	require.Error(t, err)
}
