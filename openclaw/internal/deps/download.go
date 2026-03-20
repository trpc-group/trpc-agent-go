//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deps

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const downloadHTTPTimeout = 30 * time.Minute

var downloadHTTPClient = &http.Client{
	Timeout: downloadHTTPTimeout,
}

func downloadInstallStep(
	toolchain Toolchain,
	sourceName string,
	action InstallAction,
) (Step, error) {
	rawURL := strings.TrimSpace(action.URL)
	if rawURL == "" {
		return Step{}, fmt.Errorf(
			"download install action %q is missing url",
			action.Label,
		)
	}

	targetPath, err := downloadTargetPath(
		toolchain.StateDir,
		sourceName,
		action,
	)
	if err != nil {
		return Step{}, err
	}

	return Step{
		Label:           actionLabel(action, "Download tool assets"),
		Kind:            stepKindDownload,
		CommandLine:     downloadCommandLine(rawURL, targetPath),
		URL:             rawURL,
		TargetPath:      targetPath,
		Archive:         action.Archive,
		Extract:         action.Extract,
		StripComponents: action.StripComponents,
	}, nil
}

func executeDownloadStep(
	ctx context.Context,
	step Step,
) (string, error) {
	reader, cleanup, err := openDownloadReader(ctx, step.URL)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if step.Extract {
		if err := os.MkdirAll(step.TargetPath, 0o755); err != nil {
			return "", err
		}
		if err := extractArchive(
			reader,
			step.Archive,
			step.TargetPath,
			step.StripComponents,
		); err != nil {
			return "", err
		}
		return "downloaded to " + step.TargetPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(step.TargetPath), 0o755); err != nil {
		return "", err
	}
	if err := writeDownloadFile(step.TargetPath, reader); err != nil {
		return "", err
	}
	return "downloaded to " + step.TargetPath, nil
}

func writeDownloadFile(
	targetPath string,
	reader io.Reader,
) error {
	file, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		_ = os.Remove(targetPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(targetPath)
		return err
	}
	return nil
}

func downloadTargetPath(
	stateDir string,
	sourceName string,
	action InstallAction,
) (string, error) {
	root, err := skillToolsRoot(stateDir, sourceName)
	if err != nil {
		return "", err
	}

	targetPath := root
	if strings.TrimSpace(action.TargetDir) != "" {
		targetPath, err = safeJoin(root, action.TargetDir)
		if err != nil {
			return "", err
		}
	}
	if action.Extract {
		return targetPath, nil
	}

	name := downloadFileName(action.URL)
	if name == "" {
		return "", fmt.Errorf("cannot infer download filename from url")
	}
	return safeJoin(targetPath, name)
}

func skillToolsRoot(
	stateDir string,
	sourceName string,
) (string, error) {
	cleanName := strings.TrimSpace(sourceName)
	if cleanName == "" {
		return "", fmt.Errorf("empty source name for tools root")
	}
	root := filepath.Join(
		strings.TrimSpace(stateDir),
		defaultToolsDir,
		cleanName,
	)
	return filepath.Clean(root), nil
}

func safeJoin(root string, rel string) (string, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanRoot == "" {
		return "", fmt.Errorf("empty root path")
	}

	cleanRel := filepath.Clean(strings.TrimSpace(rel))
	if cleanRel == "." || cleanRel == "" {
		return cleanRoot, nil
	}
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("absolute target path %q is not allowed", rel)
	}
	if cleanRel == ".." ||
		strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target path %q escapes root", rel)
	}
	return filepath.Join(cleanRoot, cleanRel), nil
}

func downloadCommandLine(
	rawURL string,
	targetPath string,
) string {
	return "download " + shellQuote(rawURL) + " -> " + shellQuote(targetPath)
}

func downloadFileName(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return path.Base(parsed.Path)
}

func openDownloadReader(
	ctx context.Context,
	rawURL string,
) (io.ReadCloser, func(), error) {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil, err
	}

	switch parsed.Scheme {
	case "", "file":
		path := parsed.Path
		if path == "" {
			path = rawURL
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		return file, func() { _ = file.Close() }, nil
	case "http", "https":
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			rawURL,
			nil,
		)
		if err != nil {
			return nil, nil, err
		}
		resp, err := downloadHTTPClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		if resp.StatusCode < http.StatusOK ||
			resp.StatusCode >= http.StatusMultipleChoices {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			return nil, nil, fmt.Errorf(
				"download %q failed: %s %s",
				rawURL,
				resp.Status,
				strings.TrimSpace(string(body)),
			)
		}
		return resp.Body, func() { _ = resp.Body.Close() }, nil
	default:
		return nil, nil, fmt.Errorf(
			"unsupported download scheme %q",
			parsed.Scheme,
		)
	}
}

func extractArchive(
	reader io.Reader,
	archiveKind string,
	targetPath string,
	stripComponents int,
) error {
	switch normalizeArchiveKind(archiveKind) {
	case "tar.gz":
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return err
		}
		defer gz.Close()
		return extractTar(gz, targetPath, stripComponents)
	case "tar.bz2":
		return extractTar(
			bzip2.NewReader(reader),
			targetPath,
			stripComponents,
		)
	case "zip":
		return extractZip(reader, targetPath, stripComponents)
	default:
		return fmt.Errorf("unsupported archive kind %q", archiveKind)
	}
}

func normalizeArchiveKind(raw string) string {
	kind := strings.ToLower(strings.TrimSpace(raw))
	switch kind {
	case "tgz":
		return "tar.gz"
	default:
		return kind
	}
}

func extractTar(
	reader io.Reader,
	targetPath string,
	stripComponents int,
) error {
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name, ok := archiveTargetName(
			header.Name,
			stripComponents,
		)
		if !ok {
			continue
		}
		dst, err := safeJoin(targetPath, name)
		if err != nil {
			return err
		}
		if err := writeArchiveEntry(dst, header.FileInfo().Mode(), tr); err != nil {
			return err
		}
	}
}

func extractZip(
	reader io.Reader,
	targetPath string,
	stripComponents int,
) error {
	tmp, err := os.CreateTemp("", "openclaw-download-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, reader); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	archive, err := zip.OpenReader(tmpPath)
	if err != nil {
		return err
	}
	defer archive.Close()

	for _, file := range archive.File {
		name, ok := archiveTargetName(file.Name, stripComponents)
		if !ok {
			continue
		}
		dst, err := safeJoin(targetPath, name)
		if err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		err = writeArchiveEntry(dst, file.Mode(), rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func archiveTargetName(
	name string,
	stripComponents int,
) (string, bool) {
	cleaned := path.Clean(strings.TrimSpace(name))
	if cleaned == "." || cleaned == "" {
		return "", false
	}
	parts := strings.Split(cleaned, "/")
	if stripComponents >= len(parts) {
		return "", false
	}
	parts = parts[stripComponents:]
	joined := path.Join(parts...)
	if joined == "." || joined == "" {
		return "", false
	}
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}
	return filepath.FromSlash(joined), true
}

func writeArchiveEntry(
	dst string,
	mode os.FileMode,
	reader io.Reader,
) error {
	if mode.IsDir() {
		return os.MkdirAll(dst, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(
		dst,
		os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		archiveEntryPerm(mode),
	)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, reader)
	return err
}

func archiveEntryPerm(mode os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}
