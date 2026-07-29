//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/container"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
)

func TestContainerRuntimeSmoke(t *testing.T) {
	if os.Getenv("TRPC_REVIEW_CONTAINER_TEST") != "1" {
		t.Skip("set TRPC_REVIEW_CONTAINER_TEST=1 to run the isolated container smoke test")
	}
	executor, err := container.New(container.WithDockerFilePath("docker"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, executor.Close()) })
	coordinator, err := sandbox.New(executor.Engine(), sandbox.Config{
		Checks:         []sandbox.Check{sandbox.CheckGoTest},
		Timeout:        60 * time.Second,
		MaxOutputBytes: 8 << 10,
	})
	require.NoError(t, err)
	diff, err := input.Parse(strings.NewReader(
		"diff --git a/review_test.go b/review_test.go\n" +
			"new file mode 100644\n--- /dev/null\n+++ b/review_test.go\n" +
			"@@ -0,0 +1,8 @@\n+package smoke\n+\n+import (\"os\"; \"testing\")\n+\n+func TestCleanEnvironment(t *testing.T) {\n+ if os.Getenv(\"TRPC_REVIEW_HOST_SECRET\") != \"\" { t.Fatal(\"host environment leaked\") }\n+}\n+\n",
	))
	require.NoError(t, err)
	result, err := coordinator.Run(context.Background(), sandbox.Request{
		TaskID: "container-smoke",
		Diff:   diff,
		Files: []codeexecutor.PutFile{
			{Path: "go.mod", Content: []byte("module smoke\n\ngo 1.23\n")},
			{Path: "review_test.go", Content: []byte(`package smoke

import (
	"os"
	"testing"
)

func TestCleanEnvironment(t *testing.T) {
	if os.Getenv("TRPC_REVIEW_HOST_SECRET") != "" {
		t.Fatal("host environment leaked")
	}
}
`)},
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Runs, 1)
	require.Equal(t, 0, *result.Runs[0].ExitCode)
}
