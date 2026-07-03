//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// skipIfNoEndpoint skips the test when OPENSANDBOX_ENDPOINT is not
// set. No build tag, just a runtime skip so `go test ./...` works
// out of the box.
func skipIfNoEndpoint(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENSANDBOX_ENDPOINT") == "" {
		t.Skip("OPENSANDBOX_ENDPOINT not set; skipping integration test")
	}
}

// TestIntegration_CreateAndExecute is an end-to-end smoke test against
// a real OpenSandbox server. It creates a sandbox, runs a trivial
// Python snippet, checks the output, and closes the sandbox.
//
// Configure with:
//
//	OPENSANDBOX_ENDPOINT=localhost:8080 \
//	OPENSANDBOX_PROTOCOL=http \
//	OPENSANDBOX_API_KEY=...
//	go test ./codeexecutor/opensandbox/ -run TestIntegration -v
func TestIntegration_CreateAndExecute(t *testing.T) {
	skipIfNoEndpoint(t)

	endpoint := os.Getenv("OPENSANDBOX_ENDPOINT")
	protocol := os.Getenv("OPENSANDBOX_PROTOCOL")
	if protocol == "" {
		protocol = "http"
	}

	var opts []Option
	opts = append(opts,
		WithDomain(endpoint),
		WithProtocol(protocol),
		WithAPIKey(os.Getenv("OPENSANDBOX_API_KEY")),
	)
	if img := os.Getenv("OPENSANDBOX_IMAGE"); img != "" {
		opts = append(opts, WithImage(img))
	}
	if ep := os.Getenv("OPENSANDBOX_ENTRYPOINT"); ep != "" {
		opts = append(opts, WithEntrypoint(strings.Split(ep, " ")))
	}
	exec, err := New(opts...)
	require.NoError(t, err, "New failed")
	defer exec.Close()

	assert.NotEmpty(t, exec.SandboxID(), "sandbox ID should be non-empty")

	result, err := exec.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print(1+1)"},
		},
	})
	require.NoError(t, err, "ExecuteCode failed")
	assert.Contains(t, result.Output, "2", "expected output to contain '2'")
}
