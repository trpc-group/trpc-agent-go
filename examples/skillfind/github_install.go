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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	installGitHubToolName = "skill_install_github"

	githubHost    = "github.com"
	rawGitHubHost = "raw.githubusercontent.com"
	githubAPIHost = "api.github.com"

	githubTreeKind = "tree"
	githubBlobKind = "blob"

	githubAPIBaseURL = "https://api.github.com"
	githubWebBaseURL = "https://github.com"

	githubUserAgent = "trpc-agent-go-skillfind/1.0"

	skillFileName = "SKILL.md"

	installHTTPTimeout = 30 * time.Second

	maxInstallFiles    = 64
	maxInstallBytes    = 1 << 20
	maxSingleFileBytes = 256 << 10
	maxArchiveBytes    = 8 << 20
)

const (
	defaultInstalledFileMode os.FileMode = 0o644
	executableFileMode       os.FileMode = 0o755
)

const (
	yamlFence          = "---"
	yamlNamePrefix     = "name:"
	pathSeparatorSlash = "/"
	dirNameSeparator   = "-"
)

type gitHubInstallRequest struct {
	URL string `json:"url" jsonschema:"description=GitHub skill URL"`
}

type gitHubInstallResponse struct {
	SkillName      string   `json:"skill_name"`
	InstallDir     string   `json:"install_dir"`
	SourceURL      string   `json:"source_url"`
	FileCount      int      `json:"file_count"`
	InstalledFiles []string `json:"installed_files,omitempty"`
	TotalBytes     int64    `json:"total_bytes"`
	Refreshed      bool     `json:"refreshed"`
	Description    string   `json:"description,omitempty"`
	Message        string   `json:"message"`
}

type gitHubInstaller struct {
	userSkillsRoot string
	repo           *skill.FSRepository
	client         *http.Client
	apiBaseURL     string
	webBaseURL     string
}

type gitHubLocation struct {
	Owner   string
	Repo    string
	Ref     string
	DirPath string
}

type gitHubContentItem struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

type installStats struct {
	fileCount  int
	files      []string
	totalBytes int64
}

func newGitHubInstallTool(
	userSkillsRoot string,
	repo *skill.FSRepository,
) tool.Tool {
	installer := &gitHubInstaller{
		userSkillsRoot: userSkillsRoot,
		repo:           repo,
		client: &http.Client{
			Timeout: installHTTPTimeout,
		},
		apiBaseURL: githubAPIBaseURL,
		webBaseURL: githubWebBaseURL,
	}
	return function.NewFunctionTool(
		installer.install,
		function.WithName(installGitHubToolName),
		function.WithDescription(
			"Install a public Agent Skill from a GitHub tree, "+
				"blob, or raw SKILL.md URL into the current "+
				"user skill directory, then refresh the skill "+
				"repository so the skill can be loaded in the "+
				"same conversation.",
		),
	)
}

func (i *gitHubInstaller) install(
	ctx context.Context,
	req gitHubInstallRequest,
) (gitHubInstallResponse, error) {
	location, err := parseGitHubLocation(req.URL)
	if err != nil {
		return gitHubInstallResponse{}, err
	}

	if err := os.MkdirAll(i.userSkillsRoot, 0o755); err != nil {
		return gitHubInstallResponse{}, fmt.Errorf(
			"create user skill root: %w",
			err,
		)
	}

	tempDir, err := os.MkdirTemp(i.userSkillsRoot, "skill-install-")
	if err != nil {
		return gitHubInstallResponse{}, fmt.Errorf(
			"create temp dir: %w",
			err,
		)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	stats, err := i.downloadSkillDir(ctx, location, tempDir)
	if err != nil {
		return gitHubInstallResponse{}, err
	}

	skillPath := filepath.Join(tempDir, skillFileName)
	skillName, desc, err := readInstalledSkillMeta(
		skillPath,
		path.Base(location.DirPath),
	)
	if err != nil {
		return gitHubInstallResponse{}, err
	}

	finalDir := filepath.Join(
		i.userSkillsRoot,
		sanitizeDirName(skillName),
	)
	if err := os.RemoveAll(finalDir); err != nil {
		return gitHubInstallResponse{}, fmt.Errorf(
			"remove existing skill dir: %w",
			err,
		)
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		return gitHubInstallResponse{}, fmt.Errorf(
			"move installed skill: %w",
			err,
		)
	}

	refreshed := false
	if i.repo != nil {
		if err := i.repo.Refresh(); err != nil {
			return gitHubInstallResponse{}, fmt.Errorf(
				"refresh repository: %w",
				err,
			)
		}
		refreshed = true
	}

	return gitHubInstallResponse{
		SkillName:      skillName,
		InstallDir:     finalDir,
		SourceURL:      strings.TrimSpace(req.URL),
		FileCount:      stats.fileCount,
		InstalledFiles: stats.files,
		TotalBytes:     stats.totalBytes,
		Refreshed:      refreshed,
		Description:    desc,
		Message: fmt.Sprintf(
			"installed %q from GitHub; call skill_load with "+
				"skill=%q next",
			skillName,
			skillName,
		),
	}, nil
}

func parseGitHubLocation(raw string) (gitHubLocation, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return gitHubLocation{}, errors.New("url is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return gitHubLocation{}, fmt.Errorf("parse url: %w", err)
	}

	switch strings.ToLower(parsed.Host) {
	case githubHost:
		return parseGitHubPageURL(parsed)
	case rawGitHubHost:
		return parseGitHubRawURL(parsed)
	default:
		return gitHubLocation{}, fmt.Errorf(
			"unsupported host %q: only public GitHub URLs are "+
				"supported",
			parsed.Host,
		)
	}
}

func parseGitHubPageURL(
	parsed *url.URL,
) (gitHubLocation, error) {
	parts := splitURLPath(parsed.Path)
	if len(parts) < 5 {
		return gitHubLocation{}, errors.New(
			"GitHub URL must point to a tree or SKILL.md blob",
		)
	}

	location := gitHubLocation{
		Owner: parts[0],
		Repo:  parts[1],
		Ref:   parts[3],
	}
	switch parts[2] {
	case githubTreeKind:
		location.DirPath = path.Join(parts[4:]...)
	case githubBlobKind:
		filePath := path.Join(parts[4:]...)
		location.DirPath = path.Dir(filePath)
		if path.Base(filePath) != skillFileName {
			return gitHubLocation{}, errors.New(
				"GitHub blob URL must point to SKILL.md",
			)
		}
	default:
		return gitHubLocation{}, errors.New(
			"GitHub URL must use /tree/ or /blob/",
		)
	}

	cleaned, err := validateGitHubLocation(location)
	if err != nil {
		return gitHubLocation{}, err
	}
	return cleaned, nil
}

func parseGitHubRawURL(
	parsed *url.URL,
) (gitHubLocation, error) {
	parts := splitURLPath(parsed.Path)
	if len(parts) < 4 {
		return gitHubLocation{}, errors.New(
			"raw GitHub URL must point to SKILL.md",
		)
	}

	filePath := path.Join(parts[3:]...)
	if path.Base(filePath) != skillFileName {
		return gitHubLocation{}, errors.New(
			"raw GitHub URL must point to SKILL.md",
		)
	}

	location := gitHubLocation{
		Owner:   parts[0],
		Repo:    parts[1],
		Ref:     parts[2],
		DirPath: path.Dir(filePath),
	}
	cleaned, err := validateGitHubLocation(location)
	if err != nil {
		return gitHubLocation{}, err
	}
	return cleaned, nil
}

func validateGitHubLocation(
	location gitHubLocation,
) (gitHubLocation, error) {
	if strings.TrimSpace(location.Owner) == "" ||
		strings.TrimSpace(location.Repo) == "" ||
		strings.TrimSpace(location.Ref) == "" {
		return gitHubLocation{}, errors.New(
			"GitHub URL is missing owner, repo, or ref",
		)
	}
	cleaned := path.Clean(strings.TrimSpace(location.DirPath))
	if cleaned == "." || cleaned == "" {
		return gitHubLocation{}, errors.New(
			"skill directory must not be repository root",
		)
	}
	location.DirPath = cleaned
	return location, nil
}

func splitURLPath(raw string) []string {
	cleaned := strings.Trim(raw, pathSeparatorSlash)
	if cleaned == "" {
		return nil
	}
	return strings.Split(cleaned, pathSeparatorSlash)
}

func (i *gitHubInstaller) downloadSkillDir(
	ctx context.Context,
	location gitHubLocation,
	destDir string,
) (installStats, error) {
	stats, err := i.downloadSkillDirViaAPI(ctx, location, destDir)
	if err == nil {
		return stats, nil
	}

	archiveStats, archiveErr := i.downloadSkillDirViaArchive(
		ctx,
		location,
		destDir,
	)
	if archiveErr == nil {
		return archiveStats, nil
	}

	return installStats{}, fmt.Errorf(
		"install via GitHub API failed: %v; archive fallback failed: %v",
		err,
		archiveErr,
	)
}

func (i *gitHubInstaller) downloadSkillDirViaAPI(
	ctx context.Context,
	location gitHubLocation,
	destDir string,
) (installStats, error) {
	stats := installStats{}
	if err := i.downloadGitHubPath(
		ctx,
		location,
		location.DirPath,
		destDir,
		&stats,
	); err != nil {
		return installStats{}, err
	}
	if stats.fileCount == 0 {
		return installStats{}, errors.New("downloaded skill is empty")
	}
	if _, err := os.Stat(filepath.Join(destDir, skillFileName)); err != nil {
		return installStats{}, errors.New(
			"downloaded directory does not contain SKILL.md",
		)
	}
	return stats, nil
}

func (i *gitHubInstaller) downloadSkillDirViaArchive(
	ctx context.Context,
	location gitHubLocation,
	destDir string,
) (installStats, error) {
	body, err := i.downloadArchive(ctx, location)
	if err != nil {
		return installStats{}, err
	}

	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return installStats{}, fmt.Errorf("open archive: %w", err)
	}

	prefix := archiveSkillPrefix(location)
	stats := installStats{}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(file.Name, prefix) {
			continue
		}

		relPath := strings.TrimPrefix(file.Name, prefix)
		relPath = path.Clean(relPath)
		if relPath == "." || strings.HasPrefix(relPath, "..") {
			continue
		}
		if err := extractArchiveFile(
			file,
			destDir,
			relPath,
			&stats,
		); err != nil {
			return installStats{}, err
		}
	}

	if stats.fileCount == 0 {
		return installStats{}, errors.New(
			"archive did not contain the requested skill path",
		)
	}
	if _, err := os.Stat(filepath.Join(destDir, skillFileName)); err != nil {
		return installStats{}, errors.New(
			"archive did not contain SKILL.md",
		)
	}
	return stats, nil
}

func (i *gitHubInstaller) downloadArchive(
	ctx context.Context,
	location gitHubLocation,
) ([]byte, error) {
	webBase := strings.TrimRight(i.webBaseURL, pathSeparatorSlash)
	candidates := []string{
		fmt.Sprintf(
			"%s/%s/%s/archive/refs/heads/%s.zip",
			webBase,
			url.PathEscape(location.Owner),
			url.PathEscape(location.Repo),
			url.PathEscape(location.Ref),
		),
		fmt.Sprintf(
			"%s/%s/%s/archive/refs/tags/%s.zip",
			webBase,
			url.PathEscape(location.Owner),
			url.PathEscape(location.Repo),
			url.PathEscape(location.Ref),
		),
		fmt.Sprintf(
			"%s/%s/%s/archive/%s.zip",
			webBase,
			url.PathEscape(location.Owner),
			url.PathEscape(location.Repo),
			url.PathEscape(location.Ref),
		),
	}

	var lastErr error
	for _, archiveURL := range candidates {
		data, err := i.fetchArchiveURL(ctx, archiveURL)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no archive URLs attempted")
	}
	return nil, lastErr
}

func (i *gitHubInstaller) fetchArchiveURL(
	ctx context.Context,
	archiveURL string,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		archiveURL,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create archive request: %w", err)
	}
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"download archive failed: %s",
			resp.Status,
		)
	}

	limited := io.LimitReader(resp.Body, maxArchiveBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	if len(body) > maxArchiveBytes {
		return nil, fmt.Errorf(
			"archive exceeds size limit of %d bytes",
			maxArchiveBytes,
		)
	}
	return body, nil
}

func (i *gitHubInstaller) downloadGitHubPath(
	ctx context.Context,
	location gitHubLocation,
	currentPath string,
	destDir string,
	stats *installStats,
) error {
	items, err := i.fetchContents(ctx, location, currentPath)
	if err != nil {
		return err
	}

	for _, item := range items {
		switch item.Type {
		case "dir":
			if err := i.downloadGitHubPath(
				ctx,
				location,
				item.Path,
				destDir,
				stats,
			); err != nil {
				return err
			}
		case "file":
			relPath, err := relativeSkillPath(
				location.DirPath,
				item.Path,
			)
			if err != nil {
				return err
			}
			if err := i.downloadFile(
				ctx,
				item.DownloadURL,
				destDir,
				relPath,
				stats,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i *gitHubInstaller) fetchContents(
	ctx context.Context,
	location gitHubLocation,
	currentPath string,
) ([]gitHubContentItem, error) {
	apiURL := fmt.Sprintf(
		"%s/repos/%s/%s/contents/%s?ref=%s",
		strings.TrimRight(i.apiBaseURL, pathSeparatorSlash),
		url.PathEscape(location.Owner),
		url.PathEscape(location.Repo),
		escapeGitHubPath(currentPath),
		url.QueryEscape(location.Ref),
	)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		apiURL,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contents request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf(
			"contents request failed: %s: %s",
			resp.Status,
			strings.TrimSpace(string(bodyBytes)),
		)
	}

	var items []gitHubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode contents response: %w", err)
	}
	return items, nil
}

func relativeSkillPath(
	rootPath string,
	repoPath string,
) (string, error) {
	trimmedRoot := strings.TrimSuffix(rootPath, pathSeparatorSlash)
	trimmedRepoPath := strings.TrimSpace(repoPath)

	if trimmedRepoPath == trimmedRoot {
		return "", errors.New("unexpected file path equal to skill root")
	}

	prefix := trimmedRoot + pathSeparatorSlash
	if !strings.HasPrefix(trimmedRepoPath, prefix) {
		return "", fmt.Errorf(
			"path %q is outside skill root %q",
			repoPath,
			rootPath,
		)
	}

	relPath := strings.TrimPrefix(trimmedRepoPath, prefix)
	cleaned := path.Clean(relPath)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("invalid relative path %q", relPath)
	}
	return cleaned, nil
}

func (i *gitHubInstaller) downloadFile(
	ctx context.Context,
	downloadURL string,
	destDir string,
	relPath string,
	stats *installStats,
) error {
	if stats.fileCount >= maxInstallFiles {
		return fmt.Errorf(
			"skill exceeds file limit of %d files",
			maxInstallFiles,
		)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		downloadURL,
		nil,
	)
	if err != nil {
		return fmt.Errorf("create file request: %w", err)
	}
	req.Header.Set("User-Agent", githubUserAgent)

	resp, err := i.client.Do(req)
	if err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"download file failed: %s",
			resp.Status,
		)
	}

	destPath := filepath.Join(destDir, filepath.FromSlash(relPath))
	if err := ensurePathInside(destDir, destPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create file dir: %w", err)
	}

	file, err := os.OpenFile(
		destPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		defaultInstalledFileMode,
	)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	limited := io.LimitReader(resp.Body, maxSingleFileBytes+1)
	written, err := io.Copy(file, limited)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if written > maxSingleFileBytes {
		return fmt.Errorf(
			"file %q exceeds size limit of %d bytes",
			relPath,
			maxSingleFileBytes,
		)
	}
	if stats.totalBytes+written > maxInstallBytes {
		return fmt.Errorf(
			"skill exceeds total size limit of %d bytes",
			maxInstallBytes,
		)
	}

	recordInstalledFile(stats, relPath, written)
	if err := applyInstalledFileMode(destPath, relPath, 0); err != nil {
		return err
	}
	return nil
}

func archiveSkillPrefix(location gitHubLocation) string {
	return location.Repo + "-" + location.Ref + pathSeparatorSlash +
		location.DirPath + pathSeparatorSlash
}

func extractArchiveFile(
	file *zip.File,
	destDir string,
	relPath string,
	stats *installStats,
) error {
	if stats.fileCount >= maxInstallFiles {
		return fmt.Errorf(
			"skill exceeds file limit of %d files",
			maxInstallFiles,
		)
	}
	if file.UncompressedSize64 > maxSingleFileBytes {
		return fmt.Errorf(
			"file %q exceeds size limit of %d bytes",
			relPath,
			maxSingleFileBytes,
		)
	}
	remainingBytes := maxInstallBytes - stats.totalBytes
	if remainingBytes <= 0 {
		return fmt.Errorf(
			"skill exceeds total size limit of %d bytes",
			maxInstallBytes,
		)
	}

	destPath := filepath.Join(destDir, filepath.FromSlash(relPath))
	if err := ensurePathInside(destDir, destPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create file dir: %w", err)
	}

	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("open archive file: %w", err)
	}
	defer reader.Close()

	out, err := os.OpenFile(
		destPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		defaultInstalledFileMode,
	)
	if err != nil {
		return fmt.Errorf("create extracted file: %w", err)
	}
	defer out.Close()

	copyLimit := int64(maxSingleFileBytes)
	if remainingBytes < copyLimit {
		copyLimit = remainingBytes
	}
	limitedReader := io.LimitReader(reader, copyLimit+1)
	written, err := io.Copy(out, limitedReader)
	if err != nil {
		return fmt.Errorf("extract file: %w", err)
	}
	if written > maxSingleFileBytes {
		return fmt.Errorf(
			"file %q exceeds size limit of %d bytes",
			relPath,
			maxSingleFileBytes,
		)
	}
	if stats.totalBytes+written > maxInstallBytes {
		return fmt.Errorf(
			"skill exceeds total size limit of %d bytes",
			maxInstallBytes,
		)
	}

	recordInstalledFile(stats, relPath, written)
	if err := applyInstalledFileMode(
		destPath,
		relPath,
		file.Mode(),
	); err != nil {
		return err
	}
	return nil
}

func ensurePathInside(root string, target string) error {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)

	prefix := cleanRoot + string(filepath.Separator)
	if cleanTarget == cleanRoot || strings.HasPrefix(cleanTarget, prefix) {
		return nil
	}
	return fmt.Errorf("path %q escapes install root", target)
}

func recordInstalledFile(
	stats *installStats,
	relPath string,
	written int64,
) {
	stats.fileCount++
	stats.totalBytes += written
	stats.files = append(stats.files, filepath.ToSlash(relPath))
}

func applyInstalledFileMode(
	destPath string,
	relPath string,
	sourceMode os.FileMode,
) error {
	mode := defaultInstalledFileMode
	if sourceMode != 0 {
		mode = sourceMode.Perm()
	}
	if shouldMakeExecutable(relPath) {
		mode = executableFileMode
	}
	if err := os.Chmod(destPath, mode); err != nil {
		return fmt.Errorf("chmod %q: %w", relPath, err)
	}
	return nil
}

func shouldMakeExecutable(relPath string) bool {
	parts := strings.Split(filepath.ToSlash(relPath), pathSeparatorSlash)
	return len(parts) > 1 && parts[0] == "scripts"
}

func readInstalledSkillMeta(
	skillPath string,
	fallbackName string,
) (string, string, error) {
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return "", "", fmt.Errorf("read SKILL.md: %w", err)
	}

	name := ""
	description := ""
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == yamlFence {
		for _, line := range lines[1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == yamlFence {
				break
			}
			switch {
			case strings.HasPrefix(trimmed, yamlNamePrefix):
				name = trimYAMLValue(
					strings.TrimPrefix(trimmed, yamlNamePrefix),
				)
			case strings.HasPrefix(trimmed, "description:"):
				description = trimYAMLValue(
					strings.TrimPrefix(trimmed, "description:"),
				)
			}
		}
	}

	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	if name == "" {
		return "", "", errors.New(
			"SKILL.md is missing a skill name and directory fallback",
		)
	}
	return name, description, nil
}

func trimYAMLValue(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "\"")
	trimmed = strings.Trim(trimmed, "'")
	return strings.TrimSpace(trimmed)
}

func sanitizeDirName(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.ReplaceAll(
		trimmed,
		pathSeparatorSlash,
		dirNameSeparator,
	)
	trimmed = strings.ReplaceAll(trimmed, "\\", dirNameSeparator)
	trimmed = strings.ReplaceAll(trimmed, " ", dirNameSeparator)
	trimmed = strings.Trim(trimmed, dirNameSeparator)
	if trimmed == "" {
		return "installed-skill"
	}
	return trimmed
}

func escapeGitHubPath(raw string) string {
	parts := splitURLPath(raw)
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, pathSeparatorSlash)
}
