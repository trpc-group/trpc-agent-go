//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package governance binds command plans to framework permission decisions.
package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/digest"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// RuntimeFake is the deterministic test runtime.
	RuntimeFake = "fake"
	// RuntimeContainer is the default isolated runtime.
	RuntimeContainer = "container"
	// CommandGoTest runs bundled go test checks.
	CommandGoTest = "go-test"
	// CommandGoVet runs bundled go vet checks.
	CommandGoVet = "go-vet"
	// CommandStaticcheck runs bundled staticcheck checks when available.
	CommandStaticcheck = "staticcheck"
)

// Plan is the immutable permission-bound execution plan.
type Plan struct {
	Runtime            string            `json:"runtime"`
	SkillDigest        string            `json:"skill_digest"`
	SnapshotDigest     string            `json:"snapshot_digest"`
	CommandID          string            `json:"command_id"`
	ScriptDigest       string            `json:"script_digest"`
	Args               []string          `json:"args"`
	Cwd                string            `json:"cwd"`
	Env                map[string]string `json:"env"`
	CommandTimeoutMS   int64             `json:"command_timeout_ms"`
	TaskTimeoutMS      int64             `json:"task_timeout_ms"`
	StdoutLimitBytes   int               `json:"stdout_limit_bytes"`
	StderrLimitBytes   int               `json:"stderr_limit_bytes"`
	ArtifactMaxFiles   int               `json:"artifact_max_files"`
	ArtifactFileBytes  int64             `json:"artifact_file_bytes"`
	ArtifactTotalBytes int64             `json:"artifact_total_bytes"`
}

// StaticPolicy is an explicit example permission policy.
type StaticPolicy struct {
	Enabled            bool
	Action             tool.PermissionAction
	Reason             string
	ExpectedPlanDigest string
}

// DecisionRecord is the durable permission result for a bound execution plan.
type DecisionRecord struct {
	Action     tool.PermissionAction
	Reason     string
	PlanDigest string
}

// DigestString returns a stable SHA-256 hex digest of s.
func DigestString(s string) string {
	return digest.String(s)
}

// DigestFile returns the SHA-256 hex digest of a regular file.
func DigestFile(path string) (string, error) {
	return digest.File(path)
}

// DigestTree returns a stable SHA-256 hex digest for all regular files under root.
func DigestTree(root string) (string, error) {
	var files []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	h := digest.New()
	for _, rel := range files {
		file, err := digest.OpenFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		if err := digest.WriteOpenedFile(h, rel, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
	}
	return digest.Sum(h), nil
}

// Digest returns the plan digest with stable map ordering.
func (p Plan) Digest() string {
	type alias Plan
	env := make(map[string]string, len(p.Env))
	keys := make([]string, 0, len(p.Env))
	for k := range p.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env[k] = p.Env[k]
	}
	p.Env = env
	b, _ := json.Marshal(alias(p))
	return DigestString(string(b))
}

// Gate records a permission decision and runs stage only after an allow decision.
func Gate(ctx context.Context, policy StaticPolicy, plan Plan, record func(DecisionRecord) error, verify func() error, stage func(context.Context) error) (tool.PermissionDecision, error) {
	if !policy.Enabled {
		return tool.DenyPermission("permission policy is not configured"), fmt.Errorf("permission policy is not configured")
	}
	if err := validatePlan(plan); err != nil {
		return tool.DenyPermission(err.Error()), err
	}
	if policy.ExpectedPlanDigest != "" && policy.ExpectedPlanDigest != plan.Digest() {
		err := fmt.Errorf("plan digest mismatch")
		return tool.DenyPermission(err.Error()), err
	}
	decision, err := tool.NormalizePermissionDecision(tool.PermissionDecision{Action: policy.Action, Reason: policy.Reason})
	if err != nil {
		return tool.DenyPermission(err.Error()), err
	}
	if record != nil {
		if err := record(DecisionRecord{Action: decision.Action, Reason: decision.Reason, PlanDigest: plan.Digest()}); err != nil {
			return decision, err
		}
	}
	if decision.Action != tool.PermissionActionAllow {
		return decision, nil
	}
	if verify != nil {
		if err := verify(); err != nil {
			return decision, err
		}
	}
	if stage != nil {
		if err := stage(ctx); err != nil {
			return decision, err
		}
	}
	return decision, nil
}

func validatePlan(plan Plan) error {
	if plan.Runtime == "" || plan.SkillDigest == "" || plan.SnapshotDigest == "" || plan.ScriptDigest == "" || plan.Cwd == "" {
		return fmt.Errorf("incomplete execution plan")
	}
	if plan.CommandTimeoutMS <= 0 || plan.TaskTimeoutMS <= 0 ||
		plan.StdoutLimitBytes <= 0 || plan.StderrLimitBytes <= 0 ||
		plan.ArtifactMaxFiles <= 0 || plan.ArtifactFileBytes <= 0 ||
		plan.ArtifactTotalBytes <= 0 || plan.ArtifactFileBytes > plan.ArtifactTotalBytes {
		return fmt.Errorf("invalid execution limits")
	}
	switch plan.CommandID {
	case CommandGoTest, CommandGoVet, CommandStaticcheck:
	default:
		return fmt.Errorf("unsupported command id %q", plan.CommandID)
	}
	if len(plan.Args) == 0 || plan.Args[0] != commandMode(plan.CommandID) {
		return fmt.Errorf("unsupported script argument")
	}
	for _, arg := range plan.Args {
		if strings.ContainsAny(arg, ";&|`$") || strings.Contains(arg, " -c") || strings.Contains(arg, "sh -c") {
			return fmt.Errorf("unsafe command argument")
		}
	}
	for k := range plan.Env {
		switch k {
		case "PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOFLAGS":
		default:
			return fmt.Errorf("environment variable %s is not allowlisted", k)
		}
	}
	return nil
}

// VerifySources rechecks the actual approved skill, script, and optional
// snapshot immediately before a staging or execution boundary.
func VerifySources(plan Plan, skillPath, snapshotPath string) error {
	skillDigest, err := DigestTree(skillPath)
	if err != nil {
		return fmt.Errorf("digest skill: %w", err)
	}
	if skillDigest != plan.SkillDigest {
		return fmt.Errorf("skill digest mismatch")
	}
	scriptDigest, err := DigestFile(filepath.Join(skillPath, "scripts", "run_checks.sh"))
	if err != nil {
		return fmt.Errorf("digest script: %w", err)
	}
	if scriptDigest != plan.ScriptDigest {
		return fmt.Errorf("script digest mismatch")
	}
	if snapshotPath != "" {
		snapshotDigest, err := DigestTree(snapshotPath)
		if err != nil {
			return fmt.Errorf("digest snapshot: %w", err)
		}
		if snapshotDigest != plan.SnapshotDigest {
			return fmt.Errorf("snapshot digest mismatch")
		}
	}
	return nil
}

func commandMode(commandID string) string {
	switch commandID {
	case CommandGoTest:
		return "test"
	case CommandGoVet:
		return "vet"
	case CommandStaticcheck:
		return "staticcheck"
	default:
		return ""
	}
}
