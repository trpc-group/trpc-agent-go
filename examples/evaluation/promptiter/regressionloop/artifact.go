//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

const (
	reportJSONName       = "optimization_report.json"
	reportMarkdownName   = "optimization_report.md"
	candidateProfileName = "candidate_profile.json"
)

func publishBundle(root string, report regressionReport, profile *promptiter.Profile) (returnErr error) {
	if err := validateArtifactPath(root, report.RunID); err != nil {
		return err
	}
	accepted := report.Accepted && report.Status == "succeeded"
	if accepted && (profile == nil || profile.StructureID == "") {
		return errors.New("accepted succeeded report requires a complete candidate profile")
	}
	if accepted {
		if err := validateProfileSafe(profile); err != nil {
			return err
		}
	}
	if !accepted && profile != nil {
		return errors.New("rejected or failed report must not publish a candidate profile")
	}
	jsonReport, err := renderJSON(report)
	if err != nil {
		return err
	}
	markdownReport, err := renderMarkdown(report)
	if err != nil {
		return err
	}
	files := map[string][]byte{reportJSONName: jsonReport, reportMarkdownName: markdownReport}
	if accepted {
		candidate, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal candidate profile: %w", err)
		}
		files[candidateProfileName] = append(candidate, '\n')
	}
	for name, contents := range files {
		if !json.Valid(contents) && (name == reportJSONName || name == candidateProfileName) {
			return fmt.Errorf("rendered %s is invalid JSON", name)
		}
	}

	if err := ensurePrivateRoot(root); err != nil {
		return err
	}
	destination := filepath.Join(root, report.RunID)
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("artifact bundle %q already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect artifact destination: %w", err)
	}
	staging, err := os.MkdirTemp(root, "."+report.RunID+".staging-")
	if err != nil {
		return fmt.Errorf("create artifact staging directory: %w", err)
	}
	defer func() {
		if returnErr != nil {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return fmt.Errorf("secure staging directory: %w", err)
	}
	for _, name := range []string{reportJSONName, reportMarkdownName, candidateProfileName} {
		contents, ok := files[name]
		if !ok {
			continue
		}
		if err := writeSyncedFile(filepath.Join(staging, name), contents); err != nil {
			return err
		}
	}
	if err := syncDirectory(staging); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("publish artifact bundle: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		removeErr := os.RemoveAll(destination)
		return errors.Join(err, removeErr)
	}
	return nil
}

func validateProfileSafe(profile *promptiter.Profile) error {
	for _, override := range profile.Overrides {
		model := override.Value.Model
		if model == nil {
			continue
		}
		if model.APIKey != "" {
			return fmt.Errorf("candidate profile surface %q contains a credential", override.SurfaceID)
		}
		for name := range model.Headers {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "authorization") || strings.Contains(lower, "api-key") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "credential") {
				return fmt.Errorf("candidate profile surface %q contains a credential header", override.SurfaceID)
			}
		}
	}
	return nil
}

func validateArtifactPath(root, runID string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("artifact root is empty")
	}
	if runID == "" || filepath.IsAbs(runID) || filepath.Base(runID) != runID || runID == "." || runID == ".." {
		return fmt.Errorf("unsafe run ID %q", runID)
	}
	if reportJSONName == reportMarkdownName || reportJSONName == candidateProfileName || reportMarkdownName == candidateProfileName {
		return errors.New("artifact file paths collide")
	}
	return nil
}

func ensurePrivateRoot(root string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fmt.Errorf("create artifact root: %w", err)
		}
		return os.Chmod(root, 0o700)
	}
	if err != nil {
		return fmt.Errorf("inspect artifact root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact root must not be a symbolic link")
	}
	if !info.IsDir() {
		return errors.New("artifact root is not a directory")
	}
	return nil
}

func writeSyncedFile(path string, contents []byte) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact %q: %w", filepath.Base(path), err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write artifact %q: %w", filepath.Base(path), err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync artifact %q: %w", filepath.Base(path), err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}
