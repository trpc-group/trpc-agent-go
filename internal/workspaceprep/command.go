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
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// CommandSpec describes a one-shot bootstrap command to execute during
// reconcile. Typical uses are "create a virtualenv once per workspace"
// and "pip install -r requirements.txt when requirements change".
//
// Self-healing notes:
//
//   - When MarkerPath is set, the reconciler treats the marker as the
//     sentinel: if a user removes the marker between reconciles, the
//     command re-runs even when the fingerprint is unchanged.
//   - When ObservedPaths is set, the sentinel is considered satisfied
//     only if all observed paths still exist.
//   - When neither is set, the sentinel is always "present"; the
//     command only re-runs when Fingerprint changes. This matches the
//     behavior documented in the architecture plan.
//
// FingerprintInputs lets callers fold the contents of arbitrary
// workspace-relative files into the fingerprint so that edits to, for
// example, requirements.txt naturally force a re-run.
type CommandSpec struct {
	// Key is the stable Requirement key. When empty a deterministic
	// key is derived from Cmd+Args.
	Key string
	// Cmd is the program to execute, exactly as RunProgramSpec.Cmd.
	Cmd string
	// Args are command-line arguments passed verbatim.
	Args []string
	// Env augments the run environment.
	Env map[string]string
	// Cwd is a workspace-relative working directory.
	Cwd string
	// Timeout bounds a single run.
	Timeout time.Duration
	// MarkerPath is a workspace-relative sentinel file. When set and
	// missing, Apply creates it after a successful run.
	MarkerPath string
	// ObservedPaths are additional workspace-relative paths used as
	// sentinels.
	ObservedPaths []string
	// FingerprintInputs are workspace-relative files whose contents
	// are hashed into Fingerprint. Missing files contribute an empty
	// hash segment rather than causing an error.
	FingerprintInputs []string
	// FingerprintSalt is a caller-supplied version string added to
	// the fingerprint, letting business config force a re-run without
	// changing Cmd/Args.
	FingerprintSalt string
	// Optional marks this requirement as non-blocking.
	Optional bool
}

// NewCommandRequirement builds a Requirement from CommandSpec.
func NewCommandRequirement(spec CommandSpec) (Requirement, error) {
	if strings.TrimSpace(spec.Cmd) == "" {
		return nil, fmt.Errorf(
			"workspaceprep: CommandSpec.Cmd is required",
		)
	}
	if strings.TrimSpace(spec.Key) == "" {
		sum := sha256.Sum256([]byte(
			spec.Cmd + "\x00" + strings.Join(spec.Args, "\x01"),
		))
		spec.Key = "cmd:" + hex.EncodeToString(sum[:8])
	}
	if spec.MarkerPath != "" {
		spec.MarkerPath = cleanRel(spec.MarkerPath)
	}
	cleaned := make([]string, 0, len(spec.ObservedPaths))
	for _, p := range spec.ObservedPaths {
		if rel := cleanRel(p); rel != "" {
			cleaned = append(cleaned, rel)
		}
	}
	spec.ObservedPaths = cleaned
	inputs := make([]string, 0, len(spec.FingerprintInputs))
	for _, p := range spec.FingerprintInputs {
		if rel := cleanRel(p); rel != "" {
			inputs = append(inputs, rel)
		}
	}
	sort.Strings(inputs)
	spec.FingerprintInputs = inputs
	return &commandRequirement{spec: spec}, nil
}

type commandRequirement struct {
	spec CommandSpec
}

func (r *commandRequirement) Key() string    { return r.spec.Key }
func (r *commandRequirement) Kind() Kind     { return KindCommand }
func (r *commandRequirement) Phase() Phase   { return PhaseCommand }
func (r *commandRequirement) Required() bool { return !r.spec.Optional }
func (r *commandRequirement) Target() string {
	if r.spec.MarkerPath != "" {
		return r.spec.MarkerPath
	}
	return r.spec.Cmd
}

func (r *commandRequirement) Fingerprint(
	ctx context.Context, rctx ApplyContext,
) (string, error) {
	h := sha256.New()
	h.Write([]byte("cmd|"))
	h.Write([]byte(r.spec.Cmd))
	h.Write([]byte{0})
	for _, a := range r.spec.Args {
		h.Write([]byte(a))
		h.Write([]byte{0})
	}
	keys := make([]string, 0, len(r.spec.Env))
	for k := range r.spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{'='})
		h.Write([]byte(r.spec.Env[k]))
		h.Write([]byte{0})
	}
	h.Write([]byte("cwd|"))
	h.Write([]byte(r.spec.Cwd))
	h.Write([]byte{0})
	h.Write([]byte("salt|"))
	h.Write([]byte(r.spec.FingerprintSalt))
	h.Write([]byte{0})
	for _, rel := range r.spec.FingerprintInputs {
		h.Write([]byte("input|"))
		h.Write([]byte(rel))
		h.Write([]byte{0})
		data, err := r.readFile(ctx, rctx, rel)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SentinelExists checks the MarkerPath (if set) and each ObservedPath.
// When neither is configured, the sentinel is considered present so
// that skip decisions are driven purely by Fingerprint.
func (r *commandRequirement) SentinelExists(
	ctx context.Context, rctx ApplyContext,
) (bool, error) {
	paths := append([]string{}, r.spec.ObservedPaths...)
	if r.spec.MarkerPath != "" {
		paths = append(paths, r.spec.MarkerPath)
	}
	if len(paths) == 0 {
		return true, nil
	}
	for _, rel := range paths {
		ok, err := r.pathExists(ctx, rctx, rel)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// Apply runs the configured command through eng.Runner() and, on
// success, creates the MarkerPath (when configured) so future
// reconciles can use it as a sentinel.
func (r *commandRequirement) Apply(
	ctx context.Context, rctx ApplyContext,
) error {
	if rctx.Engine == nil || rctx.Engine.Runner() == nil {
		return fmt.Errorf("workspace runner is not configured")
	}
	spec := codeexecutor.RunProgramSpec{
		Cmd:     r.spec.Cmd,
		Args:    append([]string{}, r.spec.Args...),
		Env:     cloneEnv(r.spec.Env),
		Cwd:     r.spec.Cwd,
		Timeout: r.spec.Timeout,
	}
	if spec.Cwd == "" {
		spec.Cwd = "."
	}
	res, err := rctx.Engine.Runner().RunProgram(ctx, rctx.Workspace, spec)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf(
			"bootstrap command %q exited %d: %s",
			r.spec.Cmd, res.ExitCode,
			trimForError(res.Stderr, res.Stdout),
		)
	}
	if r.spec.MarkerPath != "" && rctx.Engine.FS() != nil {
		content := fmt.Sprintf(
			"reconciled at %s\n",
			time.Now().UTC().Format(time.RFC3339),
		)
		if err := rctx.Engine.FS().PutFiles(
			ctx, rctx.Workspace,
			[]codeexecutor.PutFile{{
				Path:    r.spec.MarkerPath,
				Content: []byte(content),
				Mode:    codeexecutor.DefaultScriptFileMode,
			}},
		); err != nil {
			return fmt.Errorf("write marker: %w", err)
		}
	}
	return nil
}

func (r *commandRequirement) readFile(
	ctx context.Context, rctx ApplyContext, rel string,
) ([]byte, error) {
	if rctx.Workspace.Path != "" {
		p := path.Join(rctx.Workspace.Path, rel)
		b, err := os.ReadFile(p)
		if err == nil {
			return b, nil
		}
		if !os.IsNotExist(err) {
			// Fall through to FS() for non-local engines.
			_ = err
		}
	}
	if rctx.Engine == nil || rctx.Engine.FS() == nil {
		return nil, nil
	}
	files, err := rctx.Engine.FS().Collect(
		ctx, rctx.Workspace, []string{rel},
	)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	return []byte(files[0].Content), nil
}

func (r *commandRequirement) pathExists(
	ctx context.Context, rctx ApplyContext, rel string,
) (bool, error) {
	if rctx.Workspace.Path != "" {
		p := path.Join(rctx.Workspace.Path, rel)
		if _, err := os.Stat(p); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			_ = err
		}
	}
	if rctx.Engine == nil || rctx.Engine.FS() == nil {
		return false, nil
	}
	files, err := rctx.Engine.FS().Collect(
		ctx, rctx.Workspace, []string{rel},
	)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

func cloneEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func trimForError(stderr, stdout string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		s = strings.TrimSpace(stdout)
	}
	const max = 512
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}
