//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestWorkspaceExecSafetyArgumentsUseSecondsAndEffectiveDefaults(t *testing.T) {
	profile := safety.ToolProfile{
		MaxTimeout:     safety.Duration(time.Minute),
		MaxOutputBytes: 128,
	}
	var normalized map[string]any
	if err := json.Unmarshal(workspaceExecSafetyArguments(execInput{
		Command: "go test ./...", Timeout: 90,
	}, profile), &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["timeout_sec"] != float64(90) {
		t.Fatalf("explicit timeout = %#v", normalized["timeout_sec"])
	}
	if normalized["max_output_bytes"] != float64(128) {
		t.Fatalf("max output = %#v", normalized["max_output_bytes"])
	}

	if err := json.Unmarshal(workspaceExecSafetyArguments(execInput{
		Command: "go test ./...",
	}, profile), &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["timeout_sec"] != float64(60) {
		t.Fatalf("effective default timeout = %#v", normalized["timeout_sec"])
	}
}

func TestEffectiveExecTimeoutCapsBackend(t *testing.T) {
	profile := safety.ToolProfile{MaxTimeout: safety.Duration(time.Minute)}
	if got := effectiveExecTimeout(execInput{}, profile); got != time.Minute {
		t.Fatalf("default timeout = %v", got)
	}
	if got := effectiveExecTimeout(execInput{Timeout: 90}, profile); got != time.Minute {
		t.Fatalf("capped timeout = %v", got)
	}
	if got := effectiveExecTimeout(execInput{Timeout: 30}, profile); got != 30*time.Second {
		t.Fatalf("explicit timeout = %v", got)
	}
}

func TestRuntimeSafetyProfileUsesMostRestrictiveGuard(t *testing.T) {
	const toolName = "workspace_exec"
	directPolicy := safety.DefaultPolicy()
	directPolicy.Profiles = map[string]safety.ToolProfile{toolName: {
		MaxTimeout: safety.Duration(2 * time.Minute), MaxOutputBytes: 1 << 20,
	}}
	direct, err := safety.NewGuard(directPolicy)
	if err != nil {
		t.Fatal(err)
	}
	invocationPolicy := safety.DefaultPolicy()
	invocationPolicy.Profiles = map[string]safety.ToolProfile{toolName: {
		MaxTimeout: safety.Duration(30 * time.Second), MaxOutputBytes: 1024,
	}}
	invocation, err := safety.NewGuard(invocationPolicy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tool.WithPermissionPolicyContext(context.Background(), invocation)
	profile := effectiveRuntimeSafetyProfile(ctx, direct, toolName)
	if profile.MaxTimeout != safety.Duration(30*time.Second) || profile.MaxOutputBytes != 1024 {
		t.Fatalf("effective runtime profile = %+v", profile)
	}
}

func TestCheckRunnerSupportsOutputLimit(t *testing.T) {
	plain := codeexecutor.NewEngine(nil, nil, nil)
	if err := checkRunnerSupportsOutputLimit(plain, 1); err == nil {
		t.Fatal("runtime without hard output limit support was accepted")
	}
	supported := codeexecutor.NewEngineWithCapabilities(nil, nil, nil,
		codeexecutor.Capabilities{SupportsMaxOutputBytes: true})
	if err := checkRunnerSupportsOutputLimit(supported, 1); err != nil {
		t.Fatal(err)
	}
	if err := checkRunnerSupportsOutputLimit(plain, 0); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyGuardEnablesHardenedExecutionMode(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	exec := &ExecTool{safetyGuard: guard}
	hardened := exec.commandPolicy().Active() || exec.safetyGuard != nil
	if !hardened {
		t.Fatal("safety guard did not enable clean-environment execution")
	}
	if got := shellArgsForPolicy(hardened, "echo ok"); len(got) == 0 || got[0] != "-c" {
		t.Fatalf("guarded shell args = %#v", got)
	}
	got := envForPolicyOnGOOS(hardened, map[string]string{
		"BASH_ENV": "evil", "CI": "1",
	}, "linux")
	if got["BASH_ENV"] != "" || got["CI"] != "1" {
		t.Fatalf("guarded environment = %#v", got)
	}
}

func TestExecToolSafetyGuardBlocksBeforeWorkspaceResolution(t *testing.T) {
	guard, err := safety.NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	exec := &ExecTool{safetyGuard: guard}
	result, err := exec.Call(context.Background(), []byte(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatal(err)
	}
	permission, ok := result.(tool.PermissionResult)
	if !ok || permission.Status != tool.PermissionResultStatusDenied {
		t.Fatalf("result = %#v", result)
	}
}

func TestWriteStdinSafetyGuardBlocksBeforeSessionLookup(t *testing.T) {
	guard, _ := safety.NewDefaultGuard()
	exec := &ExecTool{safetyGuard: guard}
	write := NewWriteStdinTool(exec)
	result, err := write.Call(context.Background(), []byte(`{"session_id":"missing","chars":"rm -rf /\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	permission, ok := result.(tool.PermissionResult)
	if !ok || permission.Status != tool.PermissionResultStatusDenied {
		t.Fatalf("result = %#v", result)
	}
}

func TestWriteStdinSafetyGuardAllowsEmptyPollToReachSessionLookup(t *testing.T) {
	guard, _ := safety.NewDefaultGuard()
	exec := &ExecTool{safetyGuard: guard, sessions: map[string]*execSession{}}
	write := NewWriteStdinTool(exec)
	_, err := write.Call(context.Background(), []byte(`{"session_id":"missing","chars":""}`))
	if err == nil {
		t.Fatal("empty poll did not reach the normal session lookup")
	}
}

func TestWriteStdinSafetyGuardRequiresApprovalForEmptySubmit(t *testing.T) {
	guard, _ := safety.NewDefaultGuard()
	exec := &ExecTool{safetyGuard: guard}
	write := NewWriteStdinTool(exec)
	result, err := write.Call(context.Background(), []byte(`{"session_id":"missing","chars":"","submit":true}`))
	if err != nil {
		t.Fatal(err)
	}
	permission, ok := result.(tool.PermissionResult)
	if !ok || permission.Status != tool.PermissionResultStatusApprovalRequired {
		t.Fatalf("result = %#v", result)
	}
}
