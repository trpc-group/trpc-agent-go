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
	"os"
	"path"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// FileSpec describes a single file-shaped workspace requirement.
//
// Exactly one source strategy is honored, in the following precedence:
//
//  1. Content: inline bytes
//  2. Input: a codeexecutor.InputSpec (artifact://, host://,
//     workspace://, skill://)
//
// The Target is required and must be a workspace-relative path.
// Fingerprint combines the source identity (or content hash) with the
// target path so that moving the same content to a new location is
// treated as a separate requirement.
type FileSpec struct {
	// Key is the stable Requirement key. When empty a deterministic
	// key is derived from Target.
	Key string
	// Target is the workspace-relative destination path.
	Target string
	// Content is inline bytes. When set, Input is ignored.
	Content []byte
	// Mode is the POSIX mode for Content writes. Defaults to 0o644.
	Mode uint32
	// Input is a richer source spec reusing codeexecutor.InputSpec.
	Input *codeexecutor.InputSpec
	// Optional marks this requirement as non-blocking.
	Optional bool
}

// NewFileRequirement builds a Requirement from FileSpec after
// validating the basic invariants (target present, at least one
// source).
func NewFileRequirement(spec FileSpec) (Requirement, error) {
	target := cleanRel(spec.Target)
	if target == "" {
		return nil, fmt.Errorf(
			"workspaceprep: FileSpec.Target is required",
		)
	}
	if len(spec.Content) == 0 && spec.Input == nil {
		return nil, fmt.Errorf(
			"workspaceprep: FileSpec needs Content or Input",
		)
	}
	if spec.Input != nil {
		input := *spec.Input
		if strings.TrimSpace(input.To) == "" {
			input.To = target
		}
		spec.Input = &input
	}
	spec.Target = target
	if strings.TrimSpace(spec.Key) == "" {
		spec.Key = "file:" + target
	}
	return &fileRequirement{spec: spec}, nil
}

type fileRequirement struct {
	spec FileSpec
}

func (r *fileRequirement) Key() string    { return r.spec.Key }
func (r *fileRequirement) Kind() Kind     { return KindFile }
func (r *fileRequirement) Phase() Phase   { return PhaseFile }
func (r *fileRequirement) Required() bool { return !r.spec.Optional }
func (r *fileRequirement) Target() string { return r.spec.Target }

func (r *fileRequirement) Fingerprint(
	ctx context.Context, rctx ApplyContext,
) (string, error) {
	h := sha256.New()
	h.Write([]byte("file|"))
	h.Write([]byte(r.spec.Target))
	h.Write([]byte{0})
	if len(r.spec.Content) > 0 {
		sum := sha256.Sum256(r.spec.Content)
		h.Write([]byte("inline|"))
		h.Write(sum[:])
	}
	if r.spec.Input != nil {
		h.Write([]byte("input|"))
		h.Write([]byte(r.spec.Input.From))
		h.Write([]byte{0})
		h.Write([]byte(r.spec.Input.Mode))
		h.Write([]byte{0})
		if r.spec.Input.Pin {
			h.Write([]byte("pin"))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SentinelExists checks whether the target path still exists on the
// local filesystem. For container-backed engines the sentinel is the
// same path inside the workspace; we fall back to FS().Collect when
// the path is not reachable from the host.
func (r *fileRequirement) SentinelExists(
	ctx context.Context, rctx ApplyContext,
) (bool, error) {
	if rctx.Workspace.Path != "" {
		p := path.Join(rctx.Workspace.Path, r.spec.Target)
		if _, err := os.Stat(p); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			// Non-local engines can return a permission error here;
			// in that case we fall through to FS().Collect.
			_ = err
		}
	}
	if rctx.Engine == nil || rctx.Engine.FS() == nil {
		return false, nil
	}
	files, err := rctx.Engine.FS().Collect(
		ctx, rctx.Workspace, []string{r.spec.Target},
	)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

// Apply writes the file into the workspace. Inline content is routed
// through FS().PutFiles; InputSpec-based sources use the engine's
// StageInputs so that symlink/copy semantics, host:// mounts and
// artifact fetches all reuse existing codeexecutor plumbing.
func (r *fileRequirement) Apply(
	ctx context.Context, rctx ApplyContext,
) error {
	if rctx.Engine == nil || rctx.Engine.FS() == nil {
		return fmt.Errorf("workspace fs is not configured")
	}
	if len(r.spec.Content) > 0 {
		mode := r.spec.Mode
		if mode == 0 {
			mode = codeexecutor.DefaultScriptFileMode
		}
		return rctx.Engine.FS().PutFiles(
			ctx, rctx.Workspace,
			[]codeexecutor.PutFile{{
				Path:    r.spec.Target,
				Content: r.spec.Content,
				Mode:    mode,
			}},
		)
	}
	if r.spec.Input == nil {
		return fmt.Errorf("no source for file %q", r.spec.Target)
	}
	return rctx.Engine.FS().StageInputs(
		ctx, rctx.Workspace,
		[]codeexecutor.InputSpec{*r.spec.Input},
	)
}

func cleanRel(p string) string {
	s := strings.TrimSpace(p)
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return ""
	}
	return path.Clean(s)
}
