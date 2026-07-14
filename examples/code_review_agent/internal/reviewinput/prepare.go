// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewinput

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
)

// Config controls caller-owned locations and review-context budgets.
type Config struct {
	FixtureRoot string
	TempRoot    string
	Limits      Limits
}

// Preparer owns the complete conversion from CLI-shaped input to review facts,
// durable masked input, and framework-native workspace bootstrap state.
type Preparer struct {
	artifacts ArtifactStore
	sanitizer *redact.Sanitizer
	config    Config
}

// NewPreparer creates the review input module.
func NewPreparer(artifacts ArtifactStore, sanitizer *redact.Sanitizer, config Config) (preparer *Preparer, err error) {
	if artifacts == nil {
		return nil, errors.New("review input preparer requires an artifact store")
	}
	if sanitizer == nil {
		return nil, errors.New("review input preparer requires a sanitizer")
	}
	config.Limits = config.Limits.withDefaults()
	return &Preparer{artifacts: artifacts, sanitizer: sanitizer, config: config}, nil
}

// InputKind validates the external shape and derives its task-level kind
// without reading user files. Reviewer uses it when creating the running task;
// any later I/O or parse failure is then recorded against that task.
func (p *Preparer) InputKind(spec Spec) (inputKind string, err error) {
	return deriveInputKind(spec)
}

// Prepare builds one internally consistent view of the change. Every failure
// after a snapshot is created cleans only task-owned temporary files.
func (p *Preparer) Prepare(ctx context.Context, scope TaskScope, spec Spec) (prepared *PreparedInput, retErr error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	resolved, err := resolveSpec(spec, p.config.FixtureRoot)
	if err != nil {
		return nil, err
	}
	client := gitClient{timeout: p.config.Limits.GitTimeout}
	rawDiff, snapshot, cleanup, err := p.loadSources(ctx, resolved, client)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer func() {
			if retErr != nil {
				_ = cleanup()
			}
		}()
	}
	if len(rawDiff) == 0 {
		return nil, errors.New("review input did not produce a diff")
	}

	repoBacked := snapshot != ""
	parsed, maskedDiff, scopedDiff, err := parseReviewDiff(rawDiff, resolved.paths, repoBacked, p.sanitizer)
	if err != nil {
		return nil, err
	}
	if snapshot != "" && resolved.diffFile != "" {
		if err := client.ensureDiffApplied(ctx, snapshot, scopedDiff); err != nil {
			return nil, err
		}
	}
	parsed.GoPackages, err = discoverGoPackages(snapshot, parsed)
	if err != nil {
		return nil, err
	}
	mode := ReviewModePatchOnly
	if repoBacked {
		mode = ReviewModeRepoBacked
	}

	info := artifact.SessionInfo{AppName: scope.AppName, UserID: scope.UserID, SessionID: scope.TaskID}
	version, err := p.artifacts.SaveArtifact(ctx, info, inputArtifactName, &artifact.Artifact{
		Data:     maskedDiff,
		MimeType: "text/x-diff",
		Name:     inputArtifactName,
	})
	if err != nil {
		return nil, fmt.Errorf("save masked review input artifact: %w", err)
	}

	summary := buildInputSummary(resolved.inputKind, mode, resolved.paths, parsed, p.config.Limits)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("marshal input summary: %w", err)
	}
	bootstrap := buildWorkspaceBootstrap(version, snapshot)
	return &PreparedInput{
		InputKind:       resolved.inputKind,
		ReviewMode:      mode,
		Message:         buildReviewMessage(resolved.inputKind, mode, resolved.paths, parsed, p.config.Limits),
		SummaryJSON:     string(summaryJSON),
		ArtifactName:    inputArtifactName,
		ArtifactVersion: version,
		Bootstrap:       bootstrap,
		parsed:          parsed,
		cleanup:         cleanup,
	}, nil
}

// loadSources reads a caller-supplied diff and creates a task-owned repository
// snapshot when repo context is available. Git fixtures use the normal
// tracked/untracked loader; simple public fixtures can use an ordinary repo/
// directory without manufacturing Git metadata.
func (p *Preparer) loadSources(
	ctx context.Context,
	resolved resolvedSpec,
	client gitClient,
) (rawDiff []byte, snapshot string, cleanup func() error, err error) {
	if resolved.diffFile != "" {
		rawDiff, err = os.ReadFile(resolved.diffFile)
		if err != nil {
			return nil, "", nil, fmt.Errorf("read diff file: %w", err)
		}
	}
	if resolved.repoPath == "" {
		return rawDiff, "", nil, nil
	}
	if resolved.inputKind == InputKindFixture && !resolved.fixtureRepoIsGit {
		root, absErr := filepath.Abs(resolved.repoPath)
		if absErr != nil {
			return nil, "", nil, fmt.Errorf("resolve fixture repo: %w", absErr)
		}
		snapshot, cleanup, err = createDirectorySnapshot(root, p.config.TempRoot)
		return rawDiff, snapshot, cleanup, err
	}

	root, err := client.resolveRoot(ctx, resolved.repoPath)
	if err != nil {
		return nil, "", nil, err
	}
	if len(rawDiff) == 0 {
		rawDiff, err = client.collectDiff(ctx, root, resolved.paths)
		if err != nil {
			return nil, "", nil, err
		}
	}
	snapshot, cleanup, err = createGitSnapshot(ctx, client, root, p.config.TempRoot)
	return rawDiff, snapshot, cleanup, err
}

// buildWorkspaceBootstrap uses framework-native sources for both inputs: the
// pinned Artifact version supplies the masked diff, while host:// points only
// at the task-owned snapshot and is copied into the invocation workspace.
func buildWorkspaceBootstrap(version int, snapshot string) codeexecutor.WorkspaceBootstrapSpec {
	files := []codeexecutor.WorkspaceFile{{
		Key:    "review-input-diff",
		Target: "work/inputs/change.diff",
		Input: &codeexecutor.InputSpec{
			From: fmt.Sprintf("artifact://%s@%d", inputArtifactName, version),
			Mode: "copy",
			Pin:  true,
		},
	}}
	if snapshot != "" {
		files = append(files, codeexecutor.WorkspaceFile{
			Key:    "review-input-repo",
			Target: "work/inputs/repo",
			Input: &codeexecutor.InputSpec{
				From: "host://" + snapshot,
				// Copy backends stage a host directory's contents into
				// filepath.Dir(To). The trailing dot therefore preserves the
				// declared repo root on local, container, and remote runtimes.
				To:   "work/inputs/repo/.",
				Mode: "copy",
			},
		})
	}
	return codeexecutor.WorkspaceBootstrapSpec{Files: files}
}
