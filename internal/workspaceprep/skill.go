//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceprep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillstage"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

// SkillSpec describes a skill working copy that must exist under
// skills/<name> before user commands execute. Source is resolved
// through the provided skill.Repository using the invocation context
// (so per-conversation skill overrides still apply).
type SkillSpec struct {
	// Key is the stable Requirement key. When empty the reconciler
	// uses "skill:<name>".
	Key string
	// Name is the skill name as registered in the Repository.
	Name string
	// Repository resolves skill source paths. It must be non-nil.
	Repository rootskill.Repository
	// ReadOnly requests the legacy read-only staged tree. The
	// default (false) matches the new writable-working-copy
	// contract.
	ReadOnly bool
	// Optional marks this requirement as non-blocking.
	Optional bool
}

// NewSkillRequirement validates SkillSpec and returns a Requirement
// that materializes the skill into skills/<name>. Source resolution
// happens lazily inside Fingerprint/Apply so that context-scoped
// repositories can honor the active invocation.
func NewSkillRequirement(spec SkillSpec) (Requirement, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, fmt.Errorf(
			"workspaceprep: SkillSpec.Name is required",
		)
	}
	// spec.Name flows into skills/<name> and into skillstage cleanup.
	// Model-driven tool invocations, untrusted skill repositories, or
	// a misconfigured caller could otherwise smuggle traversal
	// components (absolute paths, "..", backslash-rooted paths) and
	// escape the workspace. Normalize and reject anything that does
	// not resolve to a single-segment, non-traversing relative name.
	if err := validateSkillName(name); err != nil {
		return nil, err
	}
	if spec.Repository == nil {
		return nil, fmt.Errorf(
			"workspaceprep: SkillSpec.Repository is required",
		)
	}
	if strings.TrimSpace(spec.Key) == "" {
		spec.Key = "skill:" + name
	}
	spec.Name = name
	return &skillRequirement{
		spec:   spec,
		stager: skillstage.New(),
	}, nil
}

// validateSkillName rejects skill names that could escape skills/<name>.
// The check is intentionally strict: we refuse anything that contains
// path separators, parent references, or leading dots. Skill naming in
// the repository layer already follows this convention, so legitimate
// callers are unaffected.
func validateSkillName(name string) error {
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf(
			"workspaceprep: SkillSpec.Name %q must not contain "+
				"path separators",
			name,
		)
	}
	if name == "." || name == ".." {
		return fmt.Errorf(
			"workspaceprep: SkillSpec.Name %q is reserved",
			name,
		)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf(
			"workspaceprep: SkillSpec.Name %q must not start "+
				"with '.'",
			name,
		)
	}
	// path.Clean must be a no-op for a well-formed single-segment
	// name; anything else implies hidden traversal or normalization
	// surprises.
	if path.Clean(name) != name {
		return fmt.Errorf(
			"workspaceprep: SkillSpec.Name %q is not a clean "+
				"relative name",
			name,
		)
	}
	return nil
}

type skillRequirement struct {
	spec   SkillSpec
	stager *skillstage.Stager
}

func (r *skillRequirement) Key() string    { return r.spec.Key }
func (r *skillRequirement) Kind() Kind     { return KindSkill }
func (r *skillRequirement) Phase() Phase   { return PhaseSkill }
func (r *skillRequirement) Required() bool { return !r.spec.Optional }
func (r *skillRequirement) Target() string {
	return path.Join(codeexecutor.DirSkills, r.spec.Name)
}

// Fingerprint captures the skill source digest plus the staging mode
// so switching between read-only and writable modes forces a
// re-stage.
func (r *skillRequirement) Fingerprint(
	ctx context.Context, rctx ApplyContext,
) (string, error) {
	root, err := rootskill.PathForContext(
		ctx, r.spec.Repository, r.spec.Name,
	)
	if err != nil {
		return "", err
	}
	dg, err := codeexecutor.DirDigest(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("skill|"))
	h.Write([]byte(r.spec.Name))
	h.Write([]byte{0})
	h.Write([]byte(dg))
	if r.spec.ReadOnly {
		h.Write([]byte("|readonly"))
	} else {
		h.Write([]byte("|writable"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SentinelExists reports whether the materialized skill tree is still
// structurally intact. We reuse the existing SkillLinksPresent helper
// which validates the out/work/inputs symlinks, and we additionally
// check that SKILL.md exists under skills/<name>.
func (r *skillRequirement) SentinelExists(
	ctx context.Context, rctx ApplyContext,
) (bool, error) {
	ok, err := r.stager.SkillLinksPresent(
		ctx, rctx.Engine, rctx.Workspace, r.spec.Name,
	)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if rctx.Engine == nil || rctx.Engine.FS() == nil {
		return true, nil
	}
	files, err := rctx.Engine.FS().Collect(
		ctx, rctx.Workspace,
		[]string{path.Join(
			codeexecutor.DirSkills, r.spec.Name, "SKILL.md",
		)},
	)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

// Apply resolves the skill source path through the repository and
// delegates to skillstage, which already handles idempotent
// restaging, symlink management, and metadata updates.
func (r *skillRequirement) Apply(
	ctx context.Context, rctx ApplyContext,
) error {
	if rctx.Engine == nil {
		return fmt.Errorf("engine is not configured")
	}
	root, err := rootskill.PathForContext(
		ctx, r.spec.Repository, r.spec.Name,
	)
	if err != nil {
		return err
	}
	return r.stager.StageSkillWithOptions(
		ctx, rctx.Engine, rctx.Workspace,
		root, r.spec.Name,
		skillstage.StageOptions{ReadOnly: r.spec.ReadOnly},
	)
}
