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
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	subcmdUpgrade = "upgrade"

	flagConfig   = "config"
	flagStateDir = "state-dir"

	openClawConfigEnvName = "OPENCLAW_CONFIG"

	defaultConfigRootDir = ".trpc-agent-go-github"
	defaultConfigAppDir  = "openclaw"
	defaultConfigFile    = "openclaw.yaml"

	installMetadataFileName = ".openclaw-install.env"
	installMetadataKeyBin   = "bin_dir"
	installMetadataKeyCfg   = "config_dir"
	installMetadataKeyState = "state_dir"

	defaultGitHubAPIBaseURL      = "https://api.github.com"
	defaultGitHubDownloadBaseURL = "https://github.com"
	defaultReleaseRepo           = "trpc-group/trpc-agent-go"
	defaultInstallScriptURL      = "https://github.com/" +
		"trpc-group/trpc-agent-go/releases/latest/" +
		"download/openclaw-install.sh"

	releaseAPIBaseURLEnvName      = "OPENCLAW_RELEASE_API_BASE_URL"
	releaseDownloadBaseURLEnvName = "OPENCLAW_RELEASE_DOWNLOAD_BASE_URL"
	releaseRepoEnvName            = "OPENCLAW_RELEASE_REPO"
	installScriptEnvName          = "OPENCLAW_INSTALL_SCRIPT_URL"

	upgradeCommandTimeout = 10 * time.Minute
)

var (
	httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}

	executablePathFunc = os.Executable
	userHomeDirFunc    = os.UserHomeDir
	installReleaseFunc = installRelease
)

type upgradePaths struct {
	ConfigPath string
	StateDir   string
}

type upgradePathInputs struct {
	ConfigPath string
	StateDir   string
}

type installMetadata struct {
	BinDir    string
	ConfigDir string
	StateDir  string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

type githubReleaseFeed struct {
	Entries []githubReleaseFeedEntry `xml:"entry"`
}

type githubReleaseFeedEntry struct {
	Title string `xml:"title"`
}

type upgradeConfigFile struct {
	StateDir *string `yaml:"state_dir,omitempty"`
}

func isTopLevelVersionRequest(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch strings.TrimSpace(args[0]) {
	case "version", "-version", "--version":
		return true
	default:
		return false
	}
}

func isTopLevelUpgradeRequest(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return strings.TrimSpace(args[0]) == subcmdUpgrade
}

func runUpgradeCommand(args []string) int {
	if len(args) > 0 && isHelpRequest(args[0]) {
		printUpgradeUsage(os.Stdout)
		return 0
	}

	paths, err := parseUpgradePaths(args)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 2
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		upgradeCommandTimeout,
	)
	defer cancel()

	if err := upgradeToLatest(ctx, os.Stdout, os.Stderr, paths); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func printUpgradeUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  openclaw upgrade [--config PATH]")
	_, _ = fmt.Fprintln(w, "                   [--state-dir PATH]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(
		w,
		"Upgrade the current OpenClaw binary to the latest release.",
	)
}

func parseUpgradePaths(args []string) (upgradePaths, error) {
	inputs, err := parseUpgradePathInputs(args)
	if err != nil {
		return upgradePaths{}, err
	}

	binDir, err := currentBinaryDir()
	if err != nil {
		return upgradePaths{}, err
	}
	return resolveUpgradePaths(binDir, inputs)
}

func parseUpgradePathInputs(args []string) (upgradePathInputs, error) {
	inputs := upgradePathInputs{}

	for i := 0; i < len(args); i++ {
		raw := strings.TrimSpace(args[i])
		if raw == "" {
			continue
		}

		if value, ok := matchFlagValue(raw, flagConfig); ok {
			if value == "" && isSeparateFlagToken(raw, flagConfig) {
				if i+1 >= len(args) {
					return upgradePathInputs{}, fmt.Errorf(
						"flag %q requires a value",
						flagConfig,
					)
				}
				i++
				value = strings.TrimSpace(args[i])
			}
			inputs.ConfigPath = value
			continue
		}

		if value, ok := matchFlagValue(raw, flagStateDir); ok {
			if value == "" && isSeparateFlagToken(raw, flagStateDir) {
				if i+1 >= len(args) {
					return upgradePathInputs{}, fmt.Errorf(
						"flag %q requires a value",
						flagStateDir,
					)
				}
				i++
				value = strings.TrimSpace(args[i])
			}
			inputs.StateDir = value
			continue
		}

		return upgradePathInputs{}, fmt.Errorf(
			"upgrade does not support argument %q",
			raw,
		)
	}

	return inputs, nil
}

func resolveUpgradePaths(
	binDir string,
	inputs upgradePathInputs,
) (upgradePaths, error) {
	metadata, err := readInstallMetadata(binDir)
	if err != nil {
		return upgradePaths{}, err
	}

	configPath, err := resolveUpgradeConfigPath(
		inputs.ConfigPath,
		metadata,
	)
	if err != nil {
		return upgradePaths{}, err
	}
	stateDir, err := resolveUpgradeStateDir(
		inputs.StateDir,
		configPath,
		metadata,
	)
	if err != nil {
		return upgradePaths{}, err
	}

	return upgradePaths{
		ConfigPath: configPath,
		StateDir:   stateDir,
	}, nil
}

func isSeparateFlagToken(raw string, name string) bool {
	raw = strings.TrimSpace(raw)
	return raw == "-"+name || raw == "--"+name
}

func matchFlagValue(raw string, name string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	if isSeparateFlagToken(raw, name) {
		return "", true
	}

	prefixes := []string{
		"-" + name + "=",
		"--" + name + "=",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(raw, prefix)),
				true
		}
	}
	return "", false
}

func isHelpRequest(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func resolveUpgradeConfigPath(
	raw string,
	metadata installMetadata,
) (string, error) {
	configPath := strings.TrimSpace(raw)
	if configPath == "" {
		configPath = strings.TrimSpace(
			os.Getenv(openClawConfigEnvName),
		)
	}
	if configPath != "" {
		return filepath.Abs(configPath)
	}

	configDir := strings.TrimSpace(metadata.ConfigDir)
	if configDir != "" {
		return filepath.Abs(
			filepath.Join(configDir, defaultConfigFile),
		)
	}

	home, err := userHomeDirFunc()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(
		home,
		defaultConfigRootDir,
		defaultConfigAppDir,
		defaultConfigFile,
	), nil
}

func resolveUpgradeStateDir(
	raw string,
	configPath string,
	metadata installMetadata,
) (string, error) {
	stateDir := strings.TrimSpace(raw)
	if stateDir != "" {
		return filepath.Abs(stateDir)
	}

	stateDir = strings.TrimSpace(metadata.StateDir)
	if stateDir != "" {
		return filepath.Abs(stateDir)
	}

	stateDir = configuredUpgradeStateDir(configPath)
	if stateDir != "" {
		return filepath.Abs(stateDir)
	}

	home, err := userHomeDirFunc()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(
		home,
		defaultConfigRootDir,
		defaultConfigAppDir,
	), nil
}

func configuredUpgradeStateDir(configPath string) string {
	path := strings.TrimSpace(configPath)
	if path == "" {
		return ""
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var cfg upgradeConfigFile
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return ""
	}
	if cfg.StateDir == nil {
		return ""
	}

	stateDir := strings.TrimSpace(*cfg.StateDir)
	if !filepath.IsAbs(stateDir) {
		return ""
	}
	return stateDir
}

func readInstallMetadata(binDir string) (installMetadata, error) {
	path := filepath.Join(binDir, installMetadataFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return installMetadata{}, nil
		}
		return installMetadata{}, fmt.Errorf(
			"read install metadata: %w",
			err,
		)
	}

	var metadata installMetadata
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return installMetadata{}, fmt.Errorf(
				"parse install metadata line %d",
				lineNumber,
			)
		}

		switch strings.TrimSpace(key) {
		case installMetadataKeyBin:
			metadata.BinDir = strings.TrimSpace(value)
		case installMetadataKeyCfg:
			metadata.ConfigDir = strings.TrimSpace(value)
		case installMetadataKeyState:
			metadata.StateDir = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return installMetadata{}, fmt.Errorf(
			"scan install metadata: %w",
			err,
		)
	}
	return metadata, nil
}

func upgradeToLatest(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	paths upgradePaths,
) error {
	latest, err := fetchLatestReleaseVersion(ctx)
	if err != nil {
		return fmt.Errorf("get latest release version: %w", err)
	}

	current := currentVersion()
	if !hasNewerRelease(latest, current) {
		_, _ = fmt.Fprintf(
			stdout,
			"openclaw is already up to date (%s)\n",
			current,
		)
		return nil
	}

	binDir, err := currentBinaryDir()
	if err != nil {
		return err
	}

	configDir := filepath.Dir(paths.ConfigPath)
	_, _ = fmt.Fprintf(
		stdout,
		"Upgrading openclaw from %s to %s\n",
		current,
		latest,
	)

	if err := installReleaseFunc(
		ctx,
		latest,
		binDir,
		configDir,
		paths.StateDir,
		stdout,
		stderr,
	); err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}
	return nil
}

func fetchLatestReleaseVersion(ctx context.Context) (string, error) {
	version, err := fetchLatestReleaseVersionFromAPI(ctx)
	if err == nil {
		return version, nil
	}

	version, feedErr := fetchLatestReleaseVersionFromFeed(ctx)
	if feedErr == nil {
		return version, nil
	}

	return "", fmt.Errorf(
		"resolve latest release: api failed: %v; "+
			"feed failed: %w",
		err,
		feedErr,
	)
}

func fetchLatestReleaseVersionFromAPI(
	ctx context.Context,
) (string, error) {
	body, err := fetchReleaseAsset(ctx, latestReleaseAPIURL())
	if err != nil {
		return "", err
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", fmt.Errorf("decode releases: %w", err)
	}

	tags := make([]string, 0, len(releases))
	for _, release := range releases {
		tags = append(tags, release.TagName)
	}
	return latestReleaseVersionFromTags(tags)
}

func fetchLatestReleaseVersionFromFeed(
	ctx context.Context,
) (string, error) {
	body, err := fetchReleaseAsset(ctx, latestReleaseFeedURL())
	if err != nil {
		return "", err
	}

	var feed githubReleaseFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("decode releases feed: %w", err)
	}

	tags := make([]string, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		tags = append(tags, entry.Title)
	}
	return latestReleaseVersionFromTags(tags)
}

func latestReleaseVersionFromTags(tags []string) (string, error) {
	for _, rawTag := range tags {
		tag := strings.TrimSpace(rawTag)
		version := normalizeReleaseVersion(tag)
		if version == "" {
			continue
		}
		if releaseTagForVersion(version) != tag {
			continue
		}
		return version, nil
	}
	return "", fmt.Errorf("no openclaw release found")
}

func installRelease(
	ctx context.Context,
	version string,
	binDir string,
	configDir string,
	stateDir string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	scriptPath, err := downloadInstallScript(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(scriptPath) }()

	cmd := exec.CommandContext(
		ctx,
		"bash",
		scriptPath,
		"--version", version,
		"--bin-dir", binDir,
		"--config-dir", configDir,
		"--state-dir", stateDir,
	)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func downloadInstallScript(ctx context.Context) (string, error) {
	body, err := fetchReleaseAsset(ctx, installScriptURL())
	if err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp("", "openclaw-install-*.sh")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	path := tmpFile.Name()
	if _, err := tmpFile.Write(body); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("write install script: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("close install script: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return "", fmt.Errorf("chmod install script: %w", err)
	}
	return path, nil
}

func fetchReleaseAsset(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"fetch %q: unexpected status %s",
			url,
			resp.Status,
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return body, nil
}

func latestReleaseAPIURL() string {
	return strings.TrimRight(releaseAPIBaseURL(), "/") +
		"/repos/" + releaseRepo() + "/releases?per_page=100"
}

func latestReleaseFeedURL() string {
	return strings.TrimRight(releaseDownloadBaseURL(), "/") +
		"/" + releaseRepo() + "/releases.atom"
}

func installScriptURL() string {
	override := strings.TrimSpace(os.Getenv(installScriptEnvName))
	if override != "" {
		return override
	}
	return defaultInstallScriptURL
}

func releaseAPIBaseURL() string {
	override := strings.TrimSpace(os.Getenv(releaseAPIBaseURLEnvName))
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return defaultGitHubAPIBaseURL
}

func releaseDownloadBaseURL() string {
	override := strings.TrimSpace(
		os.Getenv(releaseDownloadBaseURLEnvName),
	)
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return defaultGitHubDownloadBaseURL
}

func releaseRepo() string {
	override := strings.TrimSpace(os.Getenv(releaseRepoEnvName))
	if override != "" {
		return override
	}
	return defaultReleaseRepo
}

func currentBinaryDir() (string, error) {
	path, err := executablePathFunc()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Dir(path), nil
}

func hasNewerRelease(latest string, current string) bool {
	return compareReleaseVersions(latest, current) > 0
}

func compareReleaseVersions(left string, right string) int {
	leftParts, leftOK := parseReleaseVersion(left)
	rightParts, rightOK := parseReleaseVersion(right)

	switch {
	case leftOK && rightOK:
	case leftOK:
		return 1
	case rightOK:
		return -1
	default:
		return strings.Compare(
			normalizeReleaseVersion(left),
			normalizeReleaseVersion(right),
		)
	}

	maxLen := len(leftParts)
	if len(rightParts) > maxLen {
		maxLen = len(rightParts)
	}

	for i := 0; i < maxLen; i++ {
		leftValue := versionPart(leftParts, i)
		rightValue := versionPart(rightParts, i)
		switch {
		case leftValue > rightValue:
			return 1
		case leftValue < rightValue:
			return -1
		}
	}
	return 0
}

func versionPart(parts []int, index int) int {
	if index >= len(parts) {
		return 0
	}
	return parts[index]
}

func parseReleaseVersion(raw string) ([]int, bool) {
	version := normalizeReleaseVersion(raw)
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return nil, false
	}

	fields := strings.Split(version, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, false
		}
		value, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		parts = append(parts, value)
	}
	return parts, len(parts) > 0
}
