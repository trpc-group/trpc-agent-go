//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
)

func TestRunPairing_ParseError(t *testing.T) {
	require.Equal(t, 2, runPairing([]string{"-unknown-flag"}))
}

func TestRunPairing_NoAction(t *testing.T) {
	require.Equal(t, 2, runPairing([]string{"-telegram-token", "x"}))
}

func TestRunPairing_UnknownAction(t *testing.T) {
	require.Equal(
		t,
		2,
		runPairing([]string{"-telegram-token", "x", "nope"}),
	)
}

func TestRunPairingApprove_MissingCode(t *testing.T) {
	require.Equal(
		t,
		2,
		runPairing([]string{
			"-telegram-token", "x",
			"approve",
		}),
	)
}

func TestRunPairingList_TokenRequired(t *testing.T) {
	require.Equal(
		t,
		1,
		runPairingList(context.Background(), "", t.TempDir()),
	)
}

func TestRunPairingListAndApprove_WithStubProbe(t *testing.T) {
	old := probeBotInfo
	t.Cleanup(func() { probeBotInfo = old })
	probeBotInfo = func(
		_ context.Context,
		_ string,
	) (tgch.BotInfo, error) {
		return tgch.BotInfo{ID: 123, Username: "bot"}, nil
	}

	stateDir := t.TempDir()
	store, err := openPairingStore(context.Background(), "x", stateDir)
	require.NoError(t, err)

	code, approved, err := store.Request(context.Background(), "u1")
	require.NoError(t, err)
	require.False(t, approved)
	require.NotEmpty(t, code)

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	require.Equal(
		t,
		0,
		runPairingList(context.Background(), "x", stateDir),
	)
	require.Equal(
		t,
		0,
		runPairingApprove(context.Background(), "x", stateDir, code),
	)

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "CODE")
	require.Contains(t, string(out), "approved user: u1")
}

func TestRunPairing_FlagsAfterAction(t *testing.T) {
	old := probeBotInfo
	t.Cleanup(func() { probeBotInfo = old })
	probeBotInfo = func(
		_ context.Context,
		_ string,
	) (tgch.BotInfo, error) {
		return tgch.BotInfo{ID: 123, Username: "bot"}, nil
	}

	stateDir := t.TempDir()
	store, err := openPairingStore(context.Background(), "x", stateDir)
	require.NoError(t, err)

	code, approved, err := store.Request(context.Background(), "u1")
	require.NoError(t, err)
	require.False(t, approved)

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	require.Equal(
		t,
		0,
		runPairing([]string{
			"list",
			"-telegram-token", "x",
			"-state-dir", stateDir,
		}),
	)
	require.Equal(
		t,
		0,
		runPairing([]string{
			"approve", code,
			"-telegram-token", "x",
			"-state-dir", stateDir,
		}),
	)

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "CODE")
	require.Contains(t, string(out), "approved user: u1")
}
