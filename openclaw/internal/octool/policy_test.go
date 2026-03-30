//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlocksSensitivePath_BlocksDotEnvAccess(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitivePath(`python - <<'PY'
from pathlib import Path
print(Path("/tmp/.env.local").read_text())
PY`))
}

func TestBlocksSensitivePath_IgnoresPythonOsEnviron(t *testing.T) {
	t.Parallel()

	require.False(t, blocksSensitivePath(`python - <<'PY'
import os
print(os.environ.get("OPENCLAW_MEMORY_FILE"))
PY`))
}

func TestBlocksSensitivePath_BlocksSSHDirectoryAccess(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitivePath(`ls ~/.ssh/config`))
}

func TestBlocksSensitivePath_IgnoresEmbeddedDotEnvName(t *testing.T) {
	t.Parallel()

	require.False(t, blocksSensitivePath(`cat /tmp/project.envfile`))
}

func TestContainsSensitivePathFragment_RequiresBoundaryBefore(t *testing.T) {
	t.Parallel()

	require.False(
		t,
		containsSensitivePathFragment(`cat prefix.bashrc`, `.bashrc`),
	)
}

func TestSensitivePathBoundaryHelpers(t *testing.T) {
	t.Parallel()

	require.True(t, hasSensitivePathBoundaryBefore(`.bashrc`, 0))
	require.False(t, hasSensitivePathBoundaryBefore(`x.bashrc`, 1))

	require.True(t, hasSensitivePathBoundaryAfter(`~/.ssh/config`, 6, `.ssh/`))
	require.True(t, hasSensitivePathBoundaryAfter(`cat ~/.bashrc`, 13, `.bashrc`))
	require.True(t, hasSensitivePathBoundaryAfter(`cat /tmp/.env.local`, 13, `.env`))
	require.False(t, hasSensitivePathBoundaryAfter(`cat /tmp/.envfile`, 13, `.env`))
}

func TestBlocksSensitiveEnv_BlocksPythonSensitiveVarRead(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitiveEnv(`python - <<'PY'
import os
print(os.environ.get("OPENAI_API_KEY"))
PY`))
}

func TestBlocksSensitiveEnv_BlocksNodeSensitiveVarRead(t *testing.T) {
	t.Parallel()

	require.True(
		t,
		blocksSensitiveEnv(
			`node -e 'console.log(process.env.OPENAI_API_KEY)'`,
		),
	)
}

func TestBlocksSensitiveEnv_BlocksNodeBracketSensitiveVarRead(t *testing.T) {
	t.Parallel()

	require.True(
		t,
		blocksSensitiveEnv(
			`node -e 'console.log(process.env["OPENAI_API_KEY"])'`,
		),
	)
}

func TestBlocksSensitiveEnv_BlocksGoLookupEnvSensitiveVarRead(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitiveEnv(`go run <<'EOF'
package main

import "os"

func main() {
	_, _ = os.LookupEnv("OPENAI_API_KEY")
}
EOF`))
}

func TestBlocksSensitiveEnv_AllowsSafeRuntimeReadWithSensitiveLocalNames(
	t *testing.T,
) {
	t.Parallel()

	require.False(t, blocksSensitiveEnv(`python - <<'PY'
import os
token = "placeholder"
api_key = "placeholder"
print(os.environ.get("OPENCLAW_MEMORY_FILE"))
PY`))
}

func TestBlocksSensitiveEnv_BlocksShellSensitiveExpansion(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitiveEnv(`echo $OPENAI_API_KEY`))
}

func TestChatCommandSafetyPolicy_AllowsPythonOsEnviron(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `python - <<'PY'
import os
print(os.environ.get("OPENCLAW_MEMORY_FILE"))
PY`,
	})
	require.NoError(t, err)
}

func TestChatCommandSafetyPolicy_BlocksPythonSensitiveEnvRead(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `python - <<'PY'
import os
print(os.environ.get("OPENAI_API_KEY"))
PY`,
	})
	require.ErrorContains(t, err, reasonSensitiveEnv)
}

func TestChatCommandSafetyPolicy_BlocksGoLookupEnvSensitiveEnvRead(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `go run <<'EOF'
package main

import "os"

func main() {
	_, _ = os.LookupEnv("OPENAI_API_KEY")
}
EOF`,
	})
	require.ErrorContains(t, err, reasonSensitiveEnv)
}
