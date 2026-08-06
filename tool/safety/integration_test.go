//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"strings"
	"testing"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

// TestHostexecNamedToolSetGuarded builds a real hostexec.NewToolSet, wraps it
// the way the framework does (NewNamedToolSet prefixes each tool with the
// toolset name -> "hostexec_exec_command"), and confirms the guard recognises
// that prefixed name and blocks a dangerous command — the exact bypass a
// prefix-unaware backend map would allow through unscanned.
func TestHostexecNamedToolSetGuarded(t *testing.T) {
	ts, err := hostexec.NewToolSet(hostexec.WithBaseDir(t.TempDir()))
	if err != nil {
		t.Fatalf("hostexec.NewToolSet: %v", err)
	}
	named := itool.NewNamedToolSet(ts)
	defer named.Close()

	pol := NewPermissionPolicy(NewScanner(nil))
	var execName string
	for _, tl := range named.Tools(context.Background()) {
		if name := tl.Declaration().Name; strings.HasSuffix(name, "exec_command") {
			execName = name
		}
	}
	if execName == "" {
		t.Fatal("hostexec toolset did not expose an exec_command tool")
	}
	if execName == "exec_command" {
		t.Fatalf("expected a prefixed name, got bare %q", execName)
	}
	if b := pol.backendFor(execName); b != BackendHostExec {
		t.Fatalf("backendFor(%q)=%q, want hostexec", execName, b)
	}
	d, err := pol.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  execName,
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if d.Action != tool.PermissionActionDeny {
		t.Errorf("prefixed hostexec tool %q should be denied, got %s", execName, d.Action)
	}
}
