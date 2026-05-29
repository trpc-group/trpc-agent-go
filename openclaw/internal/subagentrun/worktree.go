//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	coretaskrun "trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/internal/gitworktree"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

type worktreeManager interface {
	Create(
		ctx context.Context,
		req gitworktree.CreateRequest,
	) (gitworktree.Lease, error)
	Finalize(
		ctx context.Context,
		lease gitworktree.Lease,
	) (gitworktree.FinalizeResult, error)
}

func (s *Service) createWorktree(
	ctx context.Context,
	runID string,
) (gitworktree.Lease, error) {
	if s == nil || s.worktrees == nil {
		return gitworktree.Lease{}, fmt.Errorf(
			"subagent: worktree isolation unavailable",
		)
	}
	workdir, err := worktreeSourceWorkdir(ctx)
	if err != nil {
		return gitworktree.Lease{}, err
	}
	return s.worktrees.Create(ctx, gitworktree.CreateRequest{
		ID:      runID,
		Workdir: workdir,
	})
}

func (s *Service) cleanupUnspawnedWorktree(
	ctx context.Context,
	lease *gitworktree.Lease,
) {
	if s == nil || s.worktrees == nil || lease == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	if _, err := s.worktrees.Finalize(ctx, *lease); err != nil {
		log.Warnf(
			"subagent: cleanup unspawned worktree %s failed: %v",
			lease.Path,
			err,
		)
	}
}

func (s *Service) finalizeWorktree(
	ctx context.Context,
	run coretaskrun.Run,
) map[string]string {
	if s == nil || s.worktrees == nil {
		return nil
	}
	lease, ok := worktreeLeaseFromRun(run)
	if !ok {
		return nil
	}
	result, finalizeErr := s.worktrees.Finalize(ctx, lease)
	metadata := metadataForFinalizeResult(result, finalizeErr)
	if finalizeErr != nil {
		log.Warnf(
			"subagent: finalize worktree for %s failed: %v",
			run.ID,
			finalizeErr,
		)
	}
	return metadata
}

func runOptionsFromContext(
	ctx context.Context,
	lease *gitworktree.Lease,
) []agent.RunOption {
	profile, ok := profileFromContext(ctx, lease)
	if !ok {
		return nil
	}
	return runtimeprofile.RunOptions(profile)
}

func runContextFromContext(
	ctx context.Context,
	lease *gitworktree.Lease,
) func(context.Context) context.Context {
	profile, hasProfile := profileFromContext(ctx, lease)
	req, hasRequest := runtimeprofile.RequestFromContext(ctx)
	if !hasProfile && !hasRequest {
		return nil
	}
	return func(base context.Context) context.Context {
		if base == nil {
			base = context.Background()
		}
		if hasRequest {
			base = runtimeprofile.WithRequest(base, req)
		}
		if hasProfile {
			base = runtimeprofile.WithProfile(base, profile)
		}
		return base
	}
}

func profileFromContext(
	ctx context.Context,
	lease *gitworktree.Lease,
) (runtimeprofile.Profile, bool) {
	profile, ok := runtimeprofile.ProfileFromContext(ctx)
	if lease == nil {
		return profile, ok
	}
	if !ok {
		profile = runtimeprofile.Profile{}
	}
	profile.Workspace = runtimeprofile.WorkspacePolicy{
		Workdir:      lease.Path,
		AllowedRoots: []string{lease.Path},
	}
	return profile, true
}

func worktreeSourceWorkdir(ctx context.Context) (string, error) {
	workdir, err := runtimeprofile.ResolveWorkdir(ctx, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(workdir) != "" {
		return workdir, nil
	}
	return "", fmt.Errorf(
		"subagent: worktree isolation requires runtime profile workdir",
	)
}

func worktreeRunPrompt(lease gitworktree.Lease) string {
	return "This subagent is running in a managed Git worktree at " +
		lease.Path + ". Treat that path as the current repository " +
		"for inspection, editing, builds, and tests. Do not modify " +
		"the parent repository directly. If you make changes, leave " +
		"them in this worktree and report the path and branch " +
		lease.Branch + " in your final result."
}

func metadataForFinalizeResult(
	result gitworktree.FinalizeResult,
	err error,
) map[string]string {
	cleanup := worktreeCleanupRemoved
	if err != nil {
		cleanup = worktreeCleanupError
	} else if result.Preserved || result.HasChanges {
		cleanup = worktreeCleanupPreserved
	}
	note := strings.TrimSpace(result.Reason)
	if err != nil {
		errText := strings.TrimSpace(err.Error())
		if note == "" {
			note = errText
		} else if errText != "" {
			note = note + ": " + errText
		}
	}
	metadata := map[string]string{
		metadataWorktreeCleanup:     cleanup,
		metadataWorktreeCleanupNote: note,
	}
	if path := strings.TrimSpace(result.Path); path != "" {
		metadata[metadataWorktreePath] = path
	}
	if branch := strings.TrimSpace(result.Branch); branch != "" {
		metadata[metadataWorktreeBranch] = branch
	}
	return mergeMetadata(metadata)
}

func worktreeNotificationDetail(run coretaskrun.Run) string {
	if strings.TrimSpace(run.Metadata[metadataIsolation]) != isolationWorktree {
		return ""
	}
	cleanup := strings.TrimSpace(run.Metadata[metadataWorktreeCleanup])
	if cleanup != worktreeCleanupPreserved && cleanup != worktreeCleanupError {
		return ""
	}
	path := strings.TrimSpace(run.Metadata[metadataWorktreePath])
	if path == "" {
		return ""
	}
	branch := strings.TrimSpace(run.Metadata[metadataWorktreeBranch])
	if branch == "" {
		if cleanup == worktreeCleanupError {
			return "Worktree cleanup failed: " + path
		}
		return "Worktree preserved: " + path
	}
	if cleanup == worktreeCleanupError {
		return "Worktree cleanup failed: " + path +
			" (branch " + branch + ")"
	}
	return "Worktree preserved: " + path + " (branch " + branch + ")"
}
