//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package gitworktree manages short-lived Git worktree leases.
package gitworktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultDirPerm        = 0o755
	defaultCommandTimeout = 30 * time.Second
	defaultBranchPrefix   = "managed-worktree-"
	maxSlugLength         = 64
	shortHashLength       = 12

	fallbackSlug         = "run"
	slugHashSeparator    = "-"
	gitTerminalPromptEnv = "GIT_TERMINAL_PROMPT=0"
	gitDirName           = ".git"

	changeReasonClean       = "clean"
	changeReasonStatus      = "status"
	changeReasonCommit      = "commit"
	changeReasonStatusError = "status_error"
	changeReasonCommitError = "commit_error"
	changeReasonRemoved     = "removed"
	changeReasonRemoveError = "remove_error"
)

// ErrDirtySource reports that the source repository has uncommitted changes.
var ErrDirtySource = errors.New("git worktree: source repository is dirty")

type commandRunner func(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error)

// Manager creates and finalizes managed Git worktree leases.
type Manager struct {
	Root           string
	BranchPrefix   string
	CommandTimeout time.Duration

	runGit commandRunner
	clock  func() time.Time

	mu          sync.Mutex
	createLocks map[string]*createLock
}

type createLock struct {
	mu   sync.Mutex
	refs int
}

// Option customizes a Manager.
type Option func(*Manager)

// WithCommandTimeout sets the per-Git-command timeout.
func WithCommandTimeout(timeout time.Duration) Option {
	return func(m *Manager) {
		if timeout > 0 {
			m.CommandTimeout = timeout
		}
	}
}

// WithBranchPrefix sets the branch prefix for managed worktree leases.
func WithBranchPrefix(prefix string) Option {
	return func(m *Manager) {
		if strings.TrimSpace(prefix) != "" {
			m.BranchPrefix = strings.TrimSpace(prefix)
		}
	}
}

// NewManager creates a Git worktree manager rooted under root.
func NewManager(root string, opts ...Option) *Manager {
	m := &Manager{
		Root:           strings.TrimSpace(root),
		BranchPrefix:   defaultBranchPrefix,
		CommandTimeout: defaultCommandTimeout,
		runGit:         runGitCommand,
		clock:          time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// CreateRequest describes one worktree lease request.
type CreateRequest struct {
	ID         string
	Workdir    string
	AllowDirty bool
}

// Lease describes a managed Git worktree.
type Lease struct {
	ID         string    `json:"id,omitempty"`
	RepoRoot   string    `json:"repo_root,omitempty"`
	Path       string    `json:"path,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	BaseCommit string    `json:"base_commit,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// FinalizeResult describes what happened when a lease was finalized.
type FinalizeResult struct {
	Path       string
	Branch     string
	Removed    bool
	Preserved  bool
	HasChanges bool
	Reason     string
}

// Create creates a Git worktree lease from req.Workdir's repository HEAD.
func (m *Manager) Create(
	ctx context.Context,
	req CreateRequest,
) (Lease, error) {
	if m == nil {
		return Lease{}, fmt.Errorf("git worktree: nil manager")
	}
	root, err := normalizeRoot(m.Root)
	if err != nil {
		return Lease{}, err
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return Lease{}, fmt.Errorf("git worktree: empty id")
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		return Lease{}, fmt.Errorf("git worktree: empty workdir")
	}

	repoRoot, err := m.repoRoot(ctx, workdir)
	if err != nil {
		return Lease{}, err
	}
	if !req.AllowDirty {
		dirty, err := m.hasDirtySource(ctx, repoRoot)
		if err != nil {
			return Lease{}, err
		}
		if dirty {
			return Lease{}, fmt.Errorf("%w: %s", ErrDirtySource, repoRoot)
		}
	}
	baseCommit, err := m.git(ctx, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return Lease{}, fmt.Errorf("git worktree: resolve HEAD: %w", err)
	}
	baseCommit = strings.TrimSpace(baseCommit)
	if baseCommit == "" {
		return Lease{}, fmt.Errorf("git worktree: empty HEAD for %s", repoRoot)
	}

	slug := leaseSlug(id)
	unlockCreate := m.lockCreate(repoRoot, slug)
	defer unlockCreate()

	branch := m.branchPrefix() + slug
	path := filepath.Join(root, repoHash(repoRoot), slug)
	branchExists, err := m.branchExists(ctx, repoRoot, branch)
	if err != nil {
		return Lease{}, err
	}
	if branchExists {
		return Lease{}, fmt.Errorf("git worktree: branch already exists: %s", branch)
	}
	if _, err := os.Stat(path); err == nil {
		return Lease{}, fmt.Errorf("git worktree: path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return Lease{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), defaultDirPerm); err != nil {
		return Lease{}, fmt.Errorf("git worktree: prepare root: %w", err)
	}
	if _, err := m.git(
		ctx,
		repoRoot,
		"worktree",
		"add",
		"-b",
		branch,
		path,
		baseCommit,
	); err != nil {
		if cleanupErr := m.cleanupCreateFailure(ctx, repoRoot, path, branch); cleanupErr != nil {
			return Lease{}, fmt.Errorf(
				"git worktree: create: %w; cleanup: %v",
				err,
				cleanupErr,
			)
		}
		return Lease{}, fmt.Errorf("git worktree: create: %w", err)
	}
	return Lease{
		ID:         id,
		RepoRoot:   repoRoot,
		Path:       path,
		Branch:     branch,
		BaseCommit: baseCommit,
		CreatedAt:  m.now(),
	}, nil
}

func (m *Manager) lockCreate(repoRoot string, slug string) func() {
	key := filepath.Join(repoHash(repoRoot), slug)
	m.mu.Lock()
	if m.createLocks == nil {
		m.createLocks = make(map[string]*createLock)
	}
	lock := m.createLocks[key]
	if lock == nil {
		lock = &createLock{}
		m.createLocks[key] = lock
	}
	lock.refs++
	m.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		m.mu.Lock()
		defer m.mu.Unlock()

		lock.refs--
		if lock.refs == 0 {
			delete(m.createLocks, key)
		}
	}
}

func (m *Manager) validateManagedLease(root string, lease Lease) error {
	id := strings.TrimSpace(lease.ID)
	if id == "" {
		return fmt.Errorf("git worktree: empty lease id")
	}
	slug := leaseSlug(id)
	expectedBranch := m.branchPrefix() + slug
	if strings.TrimSpace(lease.Branch) != expectedBranch {
		return fmt.Errorf(
			"git worktree: lease branch mismatch: %s",
			lease.Branch,
		)
	}
	expectedPath := filepath.Join(
		root,
		repoHash(lease.RepoRoot),
		slug,
	)
	if filepath.Clean(lease.Path) != expectedPath {
		return fmt.Errorf(
			"git worktree: lease path mismatch: %s",
			lease.Path,
		)
	}
	return nil
}

// Finalize removes a clean worktree lease and preserves a changed one.
func (m *Manager) Finalize(
	ctx context.Context,
	lease Lease,
) (FinalizeResult, error) {
	result := FinalizeResult{
		Path:   strings.TrimSpace(lease.Path),
		Branch: strings.TrimSpace(lease.Branch),
		Reason: changeReasonClean,
	}
	if m == nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, fmt.Errorf("git worktree: nil manager")
	}
	if err := validateLease(lease, m.branchPrefix()); err != nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, err
	}
	root, err := normalizeRoot(m.Root)
	if err != nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, err
	}
	if err := m.validateManagedLease(root, lease); err != nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, err
	}
	if _, err := os.Stat(lease.Path); err != nil {
		if os.IsNotExist(err) {
			return m.finalizeMissingPath(ctx, lease, result)
		}
		result.Preserved = true
		result.Reason = changeReasonStatusError
		return result, fmt.Errorf("git worktree: inspect path: %w", err)
	}
	currentBranch, err := m.currentBranch(ctx, lease.Path)
	if err != nil {
		result.Preserved = true
		result.HasChanges = true
		result.Reason = changeReasonStatusError
		return result, err
	}
	if currentBranch != "" {
		result.Branch = currentBranch
	}
	switchedBranch := currentBranch != "" && currentBranch != lease.Branch
	if switchedBranch {
		branchChanged, branchReason, err := m.branchHasChanges(ctx, lease)
		if err != nil {
			result.Preserved = true
			result.HasChanges = true
			result.Reason = branchReason
			return result, err
		}
		if branchChanged {
			result.Preserved = true
			result.HasChanges = true
			result.Reason = branchReason
			return result, nil
		}
	}
	changed, reason, err := m.hasChanges(ctx, lease)
	if err != nil {
		result.Preserved = true
		result.HasChanges = true
		result.Reason = reason
		return result, err
	}
	if changed {
		result.Preserved = true
		result.HasChanges = true
		result.Reason = reason
		if switchedBranch {
			if err := m.deleteManagedBranch(ctx, lease.RepoRoot, lease.Branch); err != nil {
				result.Reason = changeReasonRemoveError
				return result, err
			}
		}
		return result, nil
	}
	if _, err := m.git(
		ctx,
		lease.RepoRoot,
		"worktree",
		"remove",
		"--force",
		lease.Path,
	); err != nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, fmt.Errorf("git worktree: remove: %w", err)
	}
	if err := m.deleteManagedBranch(ctx, lease.RepoRoot, lease.Branch); err != nil {
		result.Removed = true
		result.Reason = changeReasonRemoveError
		return result, err
	}
	result.Removed = true
	result.Reason = changeReasonRemoved
	return result, nil
}

func (m *Manager) cleanupCreateFailure(
	ctx context.Context,
	repoRoot string,
	path string,
	branch string,
) error {
	cleanupCtx := context.WithoutCancel(ctx)
	var cleanupErrs []error
	if _, err := os.Stat(path); err == nil {
		if _, err := m.git(
			cleanupCtx,
			repoRoot,
			"worktree",
			"remove",
			"--force",
			path,
		); err != nil {
			if removeErr := os.RemoveAll(path); removeErr != nil {
				cleanupErrs = append(
					cleanupErrs,
					fmt.Errorf("git worktree: remove partial path: %w", removeErr),
				)
			}
		}
	} else if !os.IsNotExist(err) {
		cleanupErrs = append(
			cleanupErrs,
			fmt.Errorf("git worktree: inspect partial path: %w", err),
		)
	}
	if _, err := m.git(cleanupCtx, repoRoot, "worktree", "prune"); err != nil {
		cleanupErrs = append(
			cleanupErrs,
			fmt.Errorf("git worktree: prune partial worktree: %w", err),
		)
	}
	if err := m.deleteManagedBranch(cleanupCtx, repoRoot, branch); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	return errors.Join(cleanupErrs...)
}

func (m *Manager) finalizeMissingPath(
	ctx context.Context,
	lease Lease,
	result FinalizeResult,
) (FinalizeResult, error) {
	if _, err := m.git(ctx, lease.RepoRoot, "worktree", "prune"); err != nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, fmt.Errorf("git worktree: prune missing worktree: %w", err)
	}
	changed, reason, err := m.branchHasChanges(ctx, lease)
	if err != nil {
		result.Preserved = true
		result.HasChanges = true
		result.Reason = reason
		return result, err
	}
	if changed {
		result.Preserved = true
		result.HasChanges = true
		result.Reason = reason
		return result, nil
	}
	if err := m.deleteManagedBranch(ctx, lease.RepoRoot, lease.Branch); err != nil {
		result.Preserved = true
		result.Reason = changeReasonRemoveError
		return result, err
	}
	result.Removed = true
	result.Reason = changeReasonRemoved
	return result, nil
}

func (m *Manager) repoRoot(ctx context.Context, workdir string) (string, error) {
	out, err := m.git(ctx, workdir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git worktree: resolve repo root: %w", err)
	}
	root := filepath.Clean(strings.TrimSpace(out))
	if root == "" || root == "." {
		return "", fmt.Errorf("git worktree: empty repo root")
	}
	if _, err := os.Stat(filepath.Join(root, gitDirName)); err != nil {
		return "", fmt.Errorf("git worktree: invalid repo root %s: %w", root, err)
	}
	return root, nil
}

func (m *Manager) hasDirtySource(
	ctx context.Context,
	repoRoot string,
) (bool, error) {
	out, err := m.git(ctx, repoRoot, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git worktree: inspect source status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) hasChanges(
	ctx context.Context,
	lease Lease,
) (bool, string, error) {
	out, err := m.git(ctx, lease.Path, "status", "--porcelain")
	if err != nil {
		return true, changeReasonStatusError,
			fmt.Errorf("git worktree: inspect status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return true, changeReasonStatus, nil
	}
	countRaw, err := m.git(
		ctx,
		lease.Path,
		"rev-list",
		"--count",
		lease.BaseCommit+"..HEAD",
	)
	if err != nil {
		return true, changeReasonCommitError,
			fmt.Errorf("git worktree: inspect commits: %w", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(countRaw))
	if err != nil {
		return true, changeReasonCommitError,
			fmt.Errorf("git worktree: parse commit count: %w", err)
	}
	if count > 0 {
		return true, changeReasonCommit, nil
	}
	return false, changeReasonClean, nil
}

func (m *Manager) branchHasChanges(
	ctx context.Context,
	lease Lease,
) (bool, string, error) {
	exists, err := m.branchExists(ctx, lease.RepoRoot, lease.Branch)
	if err != nil {
		return true, changeReasonCommitError, err
	}
	if !exists {
		return false, changeReasonClean, nil
	}
	countRaw, err := m.git(
		ctx,
		lease.RepoRoot,
		"rev-list",
		"--count",
		lease.BaseCommit+".."+lease.Branch,
	)
	if err != nil {
		return true, changeReasonCommitError,
			fmt.Errorf("git worktree: inspect branch commits: %w", err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(countRaw))
	if err != nil {
		return true, changeReasonCommitError,
			fmt.Errorf("git worktree: parse branch commit count: %w", err)
	}
	if count > 0 {
		return true, changeReasonCommit, nil
	}
	return false, changeReasonClean, nil
}

func (m *Manager) branchExists(
	ctx context.Context,
	repoRoot string,
	branch string,
) (bool, error) {
	out, err := m.git(ctx, repoRoot, "branch", "--list", branch)
	if err != nil {
		return false, fmt.Errorf("git worktree: inspect branch: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

func (m *Manager) currentBranch(
	ctx context.Context,
	workdir string,
) (string, error) {
	out, err := m.git(ctx, workdir, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("git worktree: inspect current branch: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func (m *Manager) deleteManagedBranch(
	ctx context.Context,
	repoRoot string,
	branch string,
) error {
	exists, err := m.branchExists(ctx, repoRoot, branch)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := m.git(ctx, repoRoot, "branch", "-D", branch); err != nil {
		return fmt.Errorf("git worktree: delete branch: %w", err)
	}
	return nil
}

func (m *Manager) git(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	if m.runGit == nil {
		return "", fmt.Errorf("git worktree: nil command runner")
	}
	if timeout := m.CommandTimeout; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return m.runGit(ctx, dir, args...)
}

func (m *Manager) now() time.Time {
	if m.clock == nil {
		return time.Now()
	}
	return m.clock()
}

func (m *Manager) branchPrefix() string {
	if m == nil || strings.TrimSpace(m.BranchPrefix) == "" {
		return defaultBranchPrefix
	}
	return strings.TrimSpace(m.BranchPrefix)
}

func runGitCommand(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitTerminalPromptEnv)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func normalizeRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" {
		return "", fmt.Errorf("git worktree: empty root")
	}
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		root = abs
	}
	return filepath.Clean(root), nil
}

func validateLease(lease Lease, branchPrefix string) error {
	if strings.TrimSpace(lease.Path) == "" {
		return fmt.Errorf("git worktree: empty lease path")
	}
	if strings.TrimSpace(lease.RepoRoot) == "" {
		return fmt.Errorf("git worktree: empty lease repo root")
	}
	if strings.TrimSpace(lease.BaseCommit) == "" {
		return fmt.Errorf("git worktree: empty lease base commit")
	}
	branch := strings.TrimSpace(lease.Branch)
	if !strings.HasPrefix(branch, strings.TrimSpace(branchPrefix)) {
		return fmt.Errorf("git worktree: unmanaged branch %q", branch)
	}
	return nil
}

func safeSlug(raw string) string {
	raw = strings.TrimSpace(raw)
	var builder strings.Builder
	lastDash := false
	for _, r := range raw {
		if builder.Len() >= maxSlugLength {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
			continue
		}
		if r == '_' || r == '-' {
			builder.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if lastDash {
			continue
		}
		builder.WriteByte('-')
		lastDash = true
	}
	slug := strings.Trim(builder.String(), "-_")
	if slug == "" {
		return fallbackSlug
	}
	return slug
}

func leaseSlug(raw string) string {
	slug := safeSlug(raw)
	suffix := shortHash(strings.TrimSpace(raw))
	maxBaseLength := maxSlugLength -
		len(slugHashSeparator) -
		shortHashLength
	slug = truncateSlug(slug, maxBaseLength)
	if slug == "" {
		slug = fallbackSlug
	}
	return slug + slugHashSeparator + suffix
}

func truncateSlug(slug string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	if len(slug) <= maxLength {
		return slug
	}
	var builder strings.Builder
	for _, r := range slug {
		width := utf8.RuneLen(r)
		if width <= 0 || builder.Len()+width > maxLength {
			break
		}
		builder.WriteRune(r)
	}
	return strings.Trim(builder.String(), "-_")
}

func repoHash(path string) string {
	return shortHash(filepath.Clean(path))
}

func shortHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	encoded := hex.EncodeToString(sum[:])
	return encoded[:shortHashLength]
}
