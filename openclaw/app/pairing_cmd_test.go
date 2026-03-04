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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

func TestRunPairing_ParseError(t *testing.T) {
	require.Equal(t, 2, runPairing([]string{"-unknown-flag"}))
}

func TestRunPairing_NoAction(t *testing.T) {
	require.Equal(t, 2, runPairing(nil))
}

func TestRunPairing_UnknownAction(t *testing.T) {
	require.Equal(
		t,
		2,
		runPairing([]string{"nope"}),
	)
}

func TestRunPairingApprove_MissingCode(t *testing.T) {
	require.Equal(
		t,
		2,
		runPairing([]string{"approve"}),
	)
}

func TestRunPairingList_TelegramNotConfigured(t *testing.T) {
	t.Setenv(openClawConfigEnvName, "")

	require.Equal(t, 1, runPairing([]string{"list"}))
}

func TestRunPairingListAndApprove_WithStubProbe(t *testing.T) {
	old := probeBotInfo
	t.Cleanup(func() { probeBotInfo = old })
	probeBotInfo = func(
		_ context.Context,
		_ string,
		_ ...tgapi.Option,
	) (tgch.BotInfo, error) {
		return tgch.BotInfo{ID: 123, Username: "bot"}, nil
	}

	stateDir := t.TempDir()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("token: x"), &node))

	store, err := openPairingStore(context.Background(), runOptions{
		StateDir: stateDir,
		Channels: []pluginSpec{{
			Type:   telegramChannelType,
			Config: &node,
		}},
	}, "")
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
		runPairingList(context.Background(), store),
	)
	require.Equal(
		t,
		0,
		runPairingApprove(context.Background(), store, code),
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
		_ ...tgapi.Option,
	) (tgch.BotInfo, error) {
		return tgch.BotInfo{ID: 123, Username: "bot"}, nil
	}

	stateDir := t.TempDir()

	cfgData := []byte(`state_dir: "` + stateDir + `"
channels:
  - type: telegram
    config:
      token: x
`)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	runOpts, err := parseRunOptions([]string{
		"-config", cfgPath,
		"-state-dir", stateDir,
	})
	require.NoError(t, err)

	store, err := openPairingStore(context.Background(), runOpts, "")
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
			"-config", cfgPath,
			"-state-dir", stateDir,
		}),
	)
	require.Equal(
		t,
		0,
		runPairing([]string{
			"approve", code,
			"-config", cfgPath,
			"-state-dir", stateDir,
		}),
	)

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "CODE")
	require.Contains(t, string(out), "approved user: u1")
}

func TestRunPairing_LoadConfigError(t *testing.T) {
	stderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = stderr })

	cfgPath := filepath.Join(t.TempDir(), "missing.yaml")
	require.Equal(t, 1, runPairing([]string{
		"list",
		"-config", cfgPath,
	}))

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "load config failed")
}

func TestOpenPairingStore_UsesProbeBotInfo(t *testing.T) {
	const token = "token"

	srv := newTelegramServer(t, token)
	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("token: "+token), &node))

	store, err := openPairingStore(context.Background(), runOptions{
		StateDir: t.TempDir(),
		Channels: []pluginSpec{{
			Type:   telegramChannelType,
			Config: &node,
		}},
	}, "")
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestOpenPairingStore_DecodeStrictError(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
token: x
unknown: 1
`), &node))

	_, err := openPairingStore(context.Background(), runOptions{
		StateDir: t.TempDir(),
		Channels: []pluginSpec{{
			Type:   telegramChannelType,
			Config: &node,
		}},
	}, "")
	require.Error(t, err)
}

func TestOpenPairingStore_MissingToken(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`token: " "`), &node))

	_, err := openPairingStore(context.Background(), runOptions{
		StateDir: t.TempDir(),
		Channels: []pluginSpec{{
			Type:   telegramChannelType,
			Config: &node,
		}},
	}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing telegram channel token")
}

func TestOpenPairingStore_ClientNetOptionsError(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
token: x
http_timeout: bad
`), &node))

	_, err := openPairingStore(context.Background(), runOptions{
		StateDir: t.TempDir(),
		Channels: []pluginSpec{{
			Type:   telegramChannelType,
			Config: &node,
		}},
	}, "")
	require.Error(t, err)
}

func TestOpenPairingStore_PairingStoreOptionsError(t *testing.T) {
	const token = "x"

	srv := newTelegramServer(t, token)
	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
token: `+token+`
pairing_ttl: -1s
`), &node))

	_, err := openPairingStore(context.Background(), runOptions{
		StateDir: t.TempDir(),
		Channels: []pluginSpec{{
			Type:   telegramChannelType,
			Config: &node,
		}},
	}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-positive ttl")
}

func TestResolveTelegramPairingChannel_MultipleChannels(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("token: x"), &node))

	_, err := resolveTelegramPairingChannel(runOptions{
		Channels: []pluginSpec{
			{
				Type:   telegramChannelType,
				Name:   "a",
				Config: &node,
			},
			{
				Type:   telegramChannelType,
				Name:   "b",
				Config: &node,
			},
		},
	}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "use -channel")
}

func TestResolveTelegramPairingChannel_SelectByName(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("token: x"), &node))

	spec, err := resolveTelegramPairingChannel(runOptions{
		Channels: []pluginSpec{
			{
				Type:   telegramChannelType,
				Name:   "a",
				Config: &node,
			},
			{
				Type:   telegramChannelType,
				Name:   "B",
				Config: &node,
			},
		},
	}, " b ")
	require.NoError(t, err)
	require.Equal(t, "B", spec.Name)
}

func TestResolveTelegramPairingChannel_NotFound(t *testing.T) {
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("token: x"), &node))

	_, err := resolveTelegramPairingChannel(runOptions{
		Channels: []pluginSpec{
			{
				Type:   telegramChannelType,
				Name:   "a",
				Config: &node,
			},
		},
	}, "nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel not found")
}

func newTelegramServer(t *testing.T, token string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch r.URL.Path {
		case "/bot" + token + "/getMe":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(
				w,
				`{"ok":true,"result":{"id":1,"username":"bot"}}`,
			)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
