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

func TestChatCommandSafetyPolicy_BlocksSystemPackageInstallFallback(
	t *testing.T,
) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `yum install -y stockfish 2>/dev/null || ` +
			`dnf install -y stockfish 2>/dev/null || ` +
			`microdnf install -y stockfish`,
	})
	require.ErrorContains(t, err, reasonSystemPackageInstall)
}

func TestChatCommandSafetyPolicy_BlocksSudoAptPackageInstall(
	t *testing.T,
) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `sudo apt-get -y install tesseract-ocr`,
	})
	require.ErrorContains(t, err, reasonSystemPackageInstall)
}

func TestChatCommandSafetyPolicy_BlocksShellWrappedPackageInstall(
	t *testing.T,
) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `bash -lc 'apk add --no-cache chromium'`,
	})
	require.ErrorContains(t, err, reasonSystemPackageInstall)
}

func TestChatCommandSafetyPolicy_AllowsLanguagePackageInstall(
	t *testing.T,
) {
	t.Parallel()

	policy := NewChatCommandSafetyPolicy()
	for _, command := range []string{
		`pip install python-chess`,
		`python -m pip install python-chess`,
		`go install golang.org/x/tools/gopls@latest`,
	} {
		err := policy(context.Background(), CommandRequest{
			Command: command,
		})
		require.NoError(t, err)
	}
}

func TestChatCommandSafetyPolicy_BlocksSearchResultHTTPClients(
	t *testing.T,
) {
	t.Parallel()

	policy := NewChatCommandSafetyPolicy()
	for _, command := range []string{
		`curl -sL "https://www.google.com/search?q=openclaw"`,
		`wget -qO- https://www.bing.com/search?q=openclaw`,
		`http https://search.yahoo.com/search?p=openclaw`,
		`bash -lc 'curl -s "https://duckduckgo.com/?q=openclaw"'`,
		`python3 -c "import requests; ` +
			`requests.get('https://search.brave.com/search?q=x')"`,
		`node -e "fetch('https://www.google.com/search?q=x')"`,
	} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			err := policy(context.Background(), CommandRequest{
				Command: command,
			})
			require.ErrorContains(t, err, "result pages")
		})
	}
}

func TestChatCommandSafetyPolicy_AllowsNonSearchHTTPCommands(
	t *testing.T,
) {
	t.Parallel()

	policy := NewChatCommandSafetyPolicy()
	for _, command := range []string{
		`curl -sL "https://www.google.com/search/about"`,
		`curl -sL "https://www.boxofficemojo.com/year/world/2020/"`,
		`echo "https://www.google.com/search?q=openclaw"`,
		`python3 -c "print('https://www.google.com/search?q=x')"`,
	} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			err := policy(context.Background(), CommandRequest{
				Command: command,
			})
			require.NoError(t, err)
		})
	}
}

func TestBlocksSystemPackageInstall_EdgeCases(t *testing.T) {
	t.Parallel()

	require.False(t, blocksSystemPackageInstall(""))
	require.False(t, blocksSystemPackageInstallDepth("apt install curl", 3))
	require.True(t, blocksSystemPackageInstall("pacman -S stockfish"))
	require.True(t, blocksSystemPackageInstall("pacman --sync stockfish"))
	require.True(t, blocksSystemPackageInstall("FOO=bar brew install wget"))
	require.True(t, blocksSystemPackageInstall("command:apt install curl"))
	require.True(t, blocksSystemPackageInstall("exec:/usr/bin/apt-get install t"))
	require.False(t, blocksSystemPackageInstall("alias=apt install docs"))
}

func TestShellPackageInstallParsing_EdgeCases(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", policyCommandName(""))
	require.Equal(t, "", policyCommandName("alias=apt"))

	require.Equal(
		t,
		"",
		nextPolicyWord([]string{"", "FOO=bar", "--quiet"}, 0),
	)

	arg, ok := shellCommandStringArg([]string{"", "-lc"})
	require.False(t, ok)
	require.Empty(t, arg)

	arg, ok = shellCommandStringArg([]string{"-x", "python"})
	require.False(t, ok)
	require.Empty(t, arg)
}
