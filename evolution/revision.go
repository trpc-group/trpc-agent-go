//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RevisionStatus classifies where a revision sits in its lifecycle.
//
// A revision progresses through:
//
//	pending → [gates run] → active / rejected / pending_eval / pending_approval
//	active  → archived (when a newer revision is promoted)
//
// The status set is intentionally flat rather than hierarchical.
// Adding new terminal or intermediate states is additive and does not
// break callers that only inspect the existing values.
type RevisionStatus string

// Revision statuses.
const (
	// RevisionPending means the revision was built but gates have not
	// run yet. Used briefly inside the worker and never persisted with
	// this value in the current implementation; reserved so future
	// async gate pipelines can park revisions in this state.
	RevisionPending RevisionStatus = "pending"

	// RevisionActive marks the revision currently pointed to by
	// ActivePointer for its SkillID. Exactly zero or one revision per
	// SkillID is Active at any time.
	RevisionActive RevisionStatus = "active"

	// RevisionRejected means one or more gates rejected the revision
	// and it will never be promoted. The reject reasons live on the
	// gate reports embedded in the Revision.
	RevisionRejected RevisionStatus = "rejected"

	// RevisionArchived means the revision was once Active but has since
	// been superseded by a newer Active revision. Archived revisions
	// stay on disk so they can be promoted back by a rollback.
	RevisionArchived RevisionStatus = "archived"

	// RevisionPendingEval means the revision passed SpecGate + SafetyGate
	// but the EffectivenessGate has not yet evaluated it. The revision
	// is written to disk but the ActivePointer is not moved.
	RevisionPendingEval RevisionStatus = "pending_eval"

	// RevisionShadow means the revision is being evaluated by the
	// EffectivenessGate in a non-live context. It sits alongside
	// the current Active revision and can be promoted to Active or
	// rejected based on evaluation results.
	RevisionShadow RevisionStatus = "shadow"

	// RevisionPendingApproval means the revision passed all automatic
	// gates (spec, safety, effectiveness) but is awaiting human approval
	// before promotion. The worker does not block; an external system
	// (CLI, API, webhook) drives the approval decision that either
	// promotes or rejects.
	RevisionPendingApproval RevisionStatus = "pending_approval"
)

// Revision is an immutable snapshot of a SkillSpec plus the metadata
// that makes it safe to ship, audit, and roll back.
//
// SkillID is the stable logical identity of a skill across versions
// (matches the on-disk directory name). RevisionID is the content-
// addressable identity of this particular candidate body. An
// ActivePointer keyed by SkillID decides which RevisionID an agent
// actually sees at runtime.
type Revision struct {
	SkillID    string         `json:"skill_id"`
	RevisionID string         `json:"revision_id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Source     string         `json:"source"` // e.g. "reviewer", "benchmark-seed".
	Action     string         `json:"action"` // "create" | "update" | "delete".
	Spec       *SkillSpec     `json:"spec,omitempty"`
	Status     RevisionStatus `json:"status"`
	CreatedAt  time.Time      `json:"created_at"`
	PromotedAt *time.Time     `json:"promoted_at,omitempty"`

	// Gate reports populated by SpecGate / SafetyGate. Nil when the
	// corresponding gate was not run (for example when approval gate
	// is disabled at the worker level).
	SpecReport          *SpecReport          `json:"spec_report,omitempty"`
	SafetyReport        *SafetyReport        `json:"safety_report,omitempty"`
	EffectivenessReport *EffectivenessReport `json:"effectiveness_report,omitempty"`
	HumanReport         *HumanReport         `json:"human_report,omitempty"`
}

// SpecReport is the deterministic SpecGate verdict.
type SpecReport struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons,omitempty"`
}

// SafetyReport is the deterministic SafetyGate verdict.
type SafetyReport struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons,omitempty"`
}

// EffectivenessReport is the effectiveness gate verdict.
type EffectivenessReport struct {
	Passed  bool     `json:"passed"`
	Reasons []string `json:"reasons,omitempty"`
}

// HumanReport is the human gate verdict.
type HumanReport struct {
	Held    bool     `json:"held"`
	Reasons []string `json:"reasons,omitempty"`
}

// AuditEvent is one entry in the append-only audit log. Each
// CandidateStore operation that mutates on-disk state appends one
// AuditEvent to a JSON-lines file so operators can reconstruct "what
// the worker did and why" without replaying the reviewer.
type AuditEvent struct {
	At         time.Time `json:"at"`
	Action     string    `json:"action"` // "write_revision" | "reject" | "promote" | "archive" | "rollback".
	SkillID    string    `json:"skill_id"`
	RevisionID string    `json:"revision_id,omitempty"`
	Status     string    `json:"status,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// CandidateStore persists immutable revisions plus an append-only
// audit log. It is intentionally narrow: the store only records what
// happened, it does not decide anything. The worker decides which
// revisions to write, the gates decide whether to accept or reject,
// and the ActivePointer decides which revision is currently visible.
type CandidateStore interface {
	// WriteRevision persists a revision. Callers must set SkillID,
	// RevisionID and Status; the store overwrites CreatedAt if zero.
	// Returns an error only if the underlying backend fails — it does
	// not judge the revision's contents.
	WriteRevision(ctx context.Context, rev *Revision) error

	// ReadRevision loads a previously written revision by SkillID +
	// RevisionID. Returns (nil, os.ErrNotExist) when absent.
	ReadRevision(ctx context.Context, skillID, revisionID string) (*Revision, error)

	// ListRevisions lists the RevisionIDs known for a SkillID in
	// creation order (oldest first). Empty slice + nil error when the
	// SkillID has no revisions yet.
	ListRevisions(ctx context.Context, skillID string) ([]string, error)

	// ListSkills returns all SkillIDs that have at least one revision.
	ListSkills(ctx context.Context) ([]string, error)

	// AppendAudit records one AuditEvent for the given SkillID.
	AppendAudit(ctx context.Context, ev AuditEvent) error
}

// ActivePointer tracks which RevisionID is currently active for each
// SkillID. It is the only place an agent-read path needs to consult
// to know "which revision do I load for this skill".
//
// Implementations MUST be safe for concurrent Set / Get on different
// SkillIDs; concurrent writes to the same SkillID MUST be serialized
// so the pointer never returns a truncated file.
type ActivePointer interface {
	Get(ctx context.Context, skillID string) (revisionID string, err error)
	Set(ctx context.Context, skillID, revisionID string) error
	// Clear removes the pointer (used for deletions). A subsequent Get
	// MUST return "" + nil (or os.ErrNotExist — both are accepted by
	// worker callers).
	Clear(ctx context.Context, skillID string) error
}

// -----------------------------------------------------------------------------
// Filesystem backed implementation.
// -----------------------------------------------------------------------------

// FileCandidateStore stores revisions under <root>/<skill-id>/revisions/<revision-id>/
// and an append-only audit log under <root>/<skill-id>/audit.log. It
// is deliberately boring: plain files, no database, no locking file
// layout, so it works inside the existing filesystem-only
// `managed_skills/` world.
type FileCandidateStore struct {
	root string
	mu   sync.Mutex // serializes audit-log appends per process.
}

// NewFileCandidateStore creates a FileCandidateStore rooted at root.
// The directory is created lazily on first write.
func NewFileCandidateStore(root string) *FileCandidateStore {
	return &FileCandidateStore{root: root}
}

// skillDir returns the directory that holds all revisions and the
// audit log for a single SkillID. It sanitizes the SkillID the same
// way on-disk skill directories are sanitized so callers can pass the
// reviewer-returned name verbatim.
func (s *FileCandidateStore) skillDir(skillID string) string {
	return filepath.Join(s.root, sanitizeSkillName(skillID))
}

// WriteRevision implements CandidateStore.
func (s *FileCandidateStore) WriteRevision(_ context.Context, rev *Revision) error {
	if rev == nil {
		return errors.New("evolution: write revision: nil revision")
	}
	if strings.TrimSpace(rev.SkillID) == "" {
		return errors.New("evolution: write revision: empty skill id")
	}
	if strings.TrimSpace(rev.RevisionID) == "" {
		return errors.New("evolution: write revision: empty revision id")
	}
	if err := validateRevisionID(rev.RevisionID); err != nil {
		return err
	}
	if rev.CreatedAt.IsZero() {
		rev.CreatedAt = time.Now().UTC()
	}
	dir := filepath.Join(s.skillDir(rev.SkillID), "revisions", rev.RevisionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("evolution: write revision: mkdir %q: %w", dir, err)
	}
	metaPath := filepath.Join(dir, "meta.json")
	meta, err := json.MarshalIndent(rev, "", "  ")
	if err != nil {
		return fmt.Errorf("evolution: write revision: marshal meta: %w", err)
	}
	if err := writeFileAtomically(metaPath, meta, 0o644); err != nil {
		return err
	}
	// Always write the SKILL.md next to the meta so operators can
	// read the revision content directly. Deletion revisions have no
	// spec; their meta.json is sufficient.
	if rev.Spec != nil {
		body := RenderSkillMarkdown(rev.Spec)
		if err := writeFileAtomically(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ReadRevision implements CandidateStore.
func (s *FileCandidateStore) ReadRevision(_ context.Context, skillID, revisionID string) (*Revision, error) {
	if err := validateRevisionID(revisionID); err != nil {
		return nil, err
	}
	metaPath := filepath.Join(s.skillDir(skillID), "revisions", revisionID, "meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var rev Revision
	if err := json.Unmarshal(raw, &rev); err != nil {
		return nil, fmt.Errorf("evolution: read revision %q/%q: %w", skillID, revisionID, err)
	}
	return &rev, nil
}

// ListRevisions implements CandidateStore. The returned slice is
// sorted oldest-first so callers can pick the previous active
// revision for rollback by walking it in reverse.
func (s *FileCandidateStore) ListRevisions(_ context.Context, skillID string) ([]string, error) {
	revDir := filepath.Join(s.skillDir(skillID), "revisions")
	entries, err := os.ReadDir(revDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type stamped struct {
		id string
		at time.Time
	}
	items := make([]stamped, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, stamped{id: e.Name(), at: info.ModTime()})
	}
	// ModTime-based sort is close enough for audit purposes; the
	// RevisionID itself also contains a millisecond timestamp prefix
	// so ties are broken in the right direction.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].at.Before(items[j-1].at); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out, nil
}

// ListSkills implements CandidateStore. Returns all SkillIDs that have
// at least one revision directory on disk.
func (s *FileCandidateStore) ListSkills(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Only include directories that have a revisions/ subdirectory.
		revDir := filepath.Join(s.root, e.Name(), "revisions")
		if info, statErr := os.Stat(revDir); statErr == nil && info.IsDir() {
			skills = append(skills, e.Name())
		}
	}
	return skills, nil
}

// AppendAudit implements CandidateStore. Writes are serialized by a
// process-wide mutex so concurrent workers do not interleave partial
// JSON lines; this is fine for the single-binary benchmark and for
// adopters that run one worker per process.
func (s *FileCandidateStore) AppendAudit(_ context.Context, ev AuditEvent) error {
	if strings.TrimSpace(ev.SkillID) == "" {
		return errors.New("evolution: append audit: empty skill id")
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.skillDir(ev.SkillID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "audit.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// FileActivePointer stores the active revision id for each SkillID in
// a single-line file named `active.txt` under the skill's directory.
// A missing file means "no active revision" (skill deleted or never
// promoted).
type FileActivePointer struct {
	root string
	mu   sync.Mutex
}

// NewFileActivePointer creates a FileActivePointer rooted at root.
// root SHOULD be the same directory used by the accompanying
// FileCandidateStore so the layout stays coherent.
func NewFileActivePointer(root string) *FileActivePointer {
	return &FileActivePointer{root: root}
}

// Get implements ActivePointer. Returns ("", nil) when the skill has
// no active pointer (missing file). Other I/O errors bubble up.
func (p *FileActivePointer) Get(_ context.Context, skillID string) (string, error) {
	path := filepath.Join(p.root, sanitizeSkillName(skillID), "active.txt")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// Set implements ActivePointer.
func (p *FileActivePointer) Set(_ context.Context, skillID, revisionID string) error {
	if strings.TrimSpace(skillID) == "" {
		return errors.New("evolution: active pointer: empty skill id")
	}
	if strings.TrimSpace(revisionID) == "" {
		return errors.New("evolution: active pointer: empty revision id")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	dir := filepath.Join(p.root, sanitizeSkillName(skillID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "active.txt")
	return writeFileAtomically(path, []byte(revisionID+"\n"), 0o644)
}

// Clear implements ActivePointer.
func (p *FileActivePointer) Clear(_ context.Context, skillID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	path := filepath.Join(p.root, sanitizeSkillName(skillID), "active.txt")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// validateRevisionID rejects revision IDs that contain path separators
// or parent-directory traversal sequences. Without this check, a
// malicious or buggy caller could craft a revisionID like
// "../../etc/passwd" and escape the intended directory subtree when
// the ID is joined into a filepath.
func validateRevisionID(id string) error {
	if strings.Contains(id, "..") {
		return fmt.Errorf("evolution: invalid revision id %q: contains path traversal", id)
	}
	if strings.Contains(id, "/") || strings.Contains(id, string(os.PathSeparator)) {
		return fmt.Errorf("evolution: invalid revision id %q: contains path separator", id)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Helpers used by the worker when building revisions.
// -----------------------------------------------------------------------------

// newRevisionID returns a time-ordered id that is unique with very
// high probability. The leading timestamp keeps lexicographic sort
// roughly aligned with creation order, which makes listing and
// rollback intuitive. The random suffix prevents collisions when two
// workers extract the same skill at the same time.
func newRevisionID() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("%s-%s",
		time.Now().UTC().Format("20060102T150405.000"),
		hex.EncodeToString(buf[:]))
}

// skillIDFromName returns the canonical SkillID for a reviewer-given
// skill name. We reuse the on-disk sanitizer so the SkillID matches
// the directory name that filePublisher already writes to — the
// revision store and the publisher therefore stay consistent when
// both are enabled.
func skillIDFromName(name string) string {
	return sanitizeSkillName(name)
}
