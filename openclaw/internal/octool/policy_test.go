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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlocksSensitivePath_AllowsDotEnvAccess(t *testing.T) {
	t.Parallel()

	require.False(t, blocksSensitivePath(`python - <<'PY'
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
	require.True(t, hasSensitivePathBoundaryAfter(
		`cat ~/.bashrc`,
		13,
		`.bashrc`,
	))
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

func TestChatCommandSafetyPolicy_AllowsPythonSensitiveEnvRead(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `python - <<'PY'
import os
print(os.environ.get("OPENAI_API_KEY"))
PY`,
	})
	require.NoError(t, err)
}

func TestChatCommandSafetyPolicy_BlocksStateRuntimeEnvPath(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw-state")

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: "cat " + filepath.Join(
			stateDir,
			protectedRuntimeEnvRelPath,
		),
		Env: map[string]string{
			envTRPCClawStateDir: stateDir,
		},
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}

func TestChatCommandSafetyPolicy_BlocksEnvFileHandleReference(
	t *testing.T,
) {
	t.Parallel()

	envFile := filepath.Join("/tmp", "openclaw", "runtime", "env.sh")

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `cat "$TRPC_CLAW_ENV_FILE"`,
		Env: map[string]string{
			envTRPCClawEnvFile: envFile,
		},
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}

func TestChatCommandSafetyPolicy_BlocksQuotedStateDirRuntimePath(
	t *testing.T,
) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw-state")

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `cat "$TRPC_CLAW_STATE_DIR"/runtime/env.sh`,
		Env: map[string]string{
			envTRPCClawStateDir: stateDir,
		},
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}

func TestChatCommandSafetyPolicy_BlocksStateDirCredentialHandle(
	t *testing.T,
) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw-state")

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `cat ${TRPC_CLAW_STATE_DIR}/git-credentials`,
		Env: map[string]string{
			envTRPCClawStateDir: stateDir,
		},
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}

func TestChatCommandSafetyPolicy_BlocksProtectedWorkdir(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: "cat config",
		Workdir: filepath.Join("/tmp", ".ssh"),
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}

func TestChatCommandSafetyPolicy_BlocksStateRuntimeWorkdir(
	t *testing.T,
) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw-state")

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: "cat env.sh",
		Workdir: filepath.Join(
			stateDir,
			filepath.Dir(protectedRuntimeEnvRelPath),
		),
		Env: map[string]string{
			envTRPCClawStateDir: stateDir,
		},
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}

func TestChatCommandSafetyPolicy_BlocksEnvFileParentWorkdir(
	t *testing.T,
) {
	t.Parallel()

	envFile := filepath.Join("/tmp", "openclaw", "runtime", "env.sh")

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: "cat env.sh",
		Workdir: filepath.Dir(envFile),
		Env: map[string]string{
			envTRPCClawEnvFile: envFile,
		},
	})
	require.ErrorContains(t, err, reasonSensitivePath)
}
