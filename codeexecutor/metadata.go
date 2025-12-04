//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeexecutor holds workspace metadata helpers and constants.
package codeexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Well-known subdirectories in a workspace.
const (
	// DirSkills contains read-only staged skill trees.
	DirSkills = "skills"
	// DirWork contains writable shared intermediates.
	DirWork = "work"
	// DirRuns contains per-run working directories.
	DirRuns = "runs"
	// DirOut contains collected outputs for artifacting.
	DirOut = "out"
	// MetaFileName is the metadata file name at workspace root.
	MetaFileName = "metadata.json"
)

// Additional environment variable keys injected at runtime.
const (
	EnvSkillsDir = "SKILLS_DIR"
	EnvWorkDir   = "WORK_DIR"
	EnvOutputDir = "OUTPUT_DIR"
	EnvRunDir    = "RUN_DIR"
	EnvSkillName = "SKILL_NAME"
)

// WorkspaceMetadata describes staged skills and recent activity.
type WorkspaceMetadata struct {
	Version    int                  `json:"version"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
	LastAccess time.Time            `json:"last_access"`
	Skills     map[string]SkillMeta `json:"skills"`
	Inputs     []InputRecord        `json:"inputs,omitempty"`
	Outputs    []OutputRecord       `json:"outputs,omitempty"`
}

// SkillMeta records a staged skill snapshot.
type SkillMeta struct {
	Name     string    `json:"name"`
	RelPath  string    `json:"rel_path"`
	Digest   string    `json:"digest"`
	Mounted  bool      `json:"mounted"`
	StagedAt time.Time `json:"staged_at"`
}

// InputRecord tracks a staged input resolution.
type InputRecord struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Resolved  string    `json:"resolved,omitempty"`
	Version   *int      `json:"version,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	Timestamp time.Time `json:"ts"`
}

// OutputRecord tracks an output collection run.
type OutputRecord struct {
	Globs     []string  `json:"globs"`
	SavedAs   []string  `json:"saved_as,omitempty"`
	Versions  []int     `json:"versions,omitempty"`
	LimitsHit bool      `json:"limits_hit"`
	Timestamp time.Time `json:"ts"`
}

// EnsureLayout creates standard workspace subdirectories and a
// metadata file when absent. It returns full paths for convenience.
func EnsureLayout(root string) (map[string]string, error) {
	paths := map[string]string{
		DirSkills: filepath.Join(root, DirSkills),
		DirWork:   filepath.Join(root, DirWork),
		DirRuns:   filepath.Join(root, DirRuns),
		DirOut:    filepath.Join(root, DirOut),
	}
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
	}
	// Initialize metadata if missing.
	mf := filepath.Join(root, MetaFileName)
	if _, err := os.Stat(mf); err != nil {
		if os.IsNotExist(err) {
			md := WorkspaceMetadata{
				Version:    1,
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
				LastAccess: time.Now(),
				Skills:     map[string]SkillMeta{},
			}
			if err := SaveMetadata(root, md); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return paths, nil
}

// LoadMetadata loads metadata.json from workspace root. When missing,
// an empty metadata with defaults is returned without error.
func LoadMetadata(root string) (WorkspaceMetadata, error) {
	mf := filepath.Join(root, MetaFileName)
	b, err := os.ReadFile(mf)
	if err != nil {
		if os.IsNotExist(err) {
			return WorkspaceMetadata{
				Version:    1,
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
				LastAccess: time.Now(),
				Skills:     map[string]SkillMeta{},
			}, nil
		}
		return WorkspaceMetadata{}, err
	}
	var md WorkspaceMetadata
	if err := json.Unmarshal(b, &md); err != nil {
		return WorkspaceMetadata{}, err
	}
	return md, nil
}

// SaveMetadata writes metadata.json to the workspace root.
func SaveMetadata(root string, md WorkspaceMetadata) error {
	md.UpdatedAt = time.Now()
	buf, err := json.MarshalIndent(md, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(root, ".metadata.tmp")
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, MetaFileName))
}

// DirDigest computes a stable digest of a directory tree. It walks
// the tree, sorts entries, and hashes relative path and contents.
func DirDigest(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(
		root,
		func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			files = append(files, rel)
			return nil
		},
	)
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		// Normalize to slash for stability.
		k := strings.ReplaceAll(rel, string(os.PathSeparator), "/")
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		_, _ = h.Write(b)
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum), nil
}
