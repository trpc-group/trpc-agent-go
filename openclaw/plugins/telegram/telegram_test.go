//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	testToken    = "token"
	testUsername = "bot"
)

type stubGateway struct{}

func (stubGateway) SendMessage(
	_ context.Context,
	_ gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	return gwclient.MessageResponse{}, nil
}

func (stubGateway) Cancel(
	_ context.Context,
	_ string,
) (bool, error) {
	return false, nil
}

func TestNewChannel_Success_Defaults(t *testing.T) {
	srv := newTelegramServer(t, testToken)
	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
`),
	}

	ch, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, pluginType, ch.ID())
}

func TestNewChannel_Success_WithOptions(t *testing.T) {
	srv := newTelegramServer(t, testToken)
	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
start_from_latest: false
proxy: ""
http_timeout: 1s
max_retries: 2
streaming: block
dm_policy: open
group_policy: allowlist
allow_threads:
  - "x"
pairing_ttl: 30m
max_download_bytes: 123
session_reset_idle: 24h
session_reset_daily: true
on_block: forget
`),
	}

	deps := registry.ChannelDeps{
		Ctx:        context.Background(),
		Gateway:    stubGateway{},
		StateDir:   t.TempDir(),
		AllowUsers: []string{"u1"},
	}

	ch, err := newChannel(deps, spec)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.Equal(t, pluginType, ch.ID())
}

func TestNewChannel_BadSessionResetIdle(t *testing.T) {
	srv := newTelegramServer(t, testToken)
	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
session_reset_idle: bad
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
}

func TestNewChannel_BadOnBlock(t *testing.T) {
	srv := newTelegramServer(t, testToken)
	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
on_block: bad
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
}

func TestNewChannel_NilGateway(t *testing.T) {
	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
`),
	}

	_, err := newChannel(registry.ChannelDeps{}, spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil gateway")
}

func TestNewChannel_DecodeStrictError(t *testing.T) {
	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
unknown: 1
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
}

func TestNewChannel_MissingToken(t *testing.T) {
	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: " "
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing config.token")
}

func TestNewChannel_BadHTTPTimeout(t *testing.T) {
	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
http_timeout: bad
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
}

func TestNewChannel_BadProxyURL(t *testing.T) {
	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
proxy: "://bad"
http_timeout: 1s
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
}

func TestNewChannel_ProbeBotError(t *testing.T) {
	spec := registry.PluginSpec{
		Type: pluginType,
		Config: mustYAMLNode(t, `
token: `+testToken+`
max_retries: -1
`),
	}

	_, err := newChannel(registry.ChannelDeps{
		Gateway:  stubGateway{},
		StateDir: t.TempDir(),
	}, spec)
	require.Error(t, err)
}

func TestResolveStreamingMode(t *testing.T) {
	require.Equal(t, defaultStreamingMode, resolveStreamingMode(""))
	require.Equal(t, "block", resolveStreamingMode(" block "))
}

func TestLogTelegramBot(t *testing.T) {
	logTelegramBot(tgch.BotInfo{Username: testUsername})
	logTelegramBot(tgch.BotInfo{ID: 1})
	logTelegramBot(tgch.BotInfo{})
}

func TestMakeTelegramAPIOptions(t *testing.T) {
	t.Setenv(tgapi.BaseURLEnvName, "")

	opts, err := makeTelegramAPIOptions(channelCfg{})
	require.NoError(t, err)
	require.NotEmpty(t, opts)
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
				`{"ok":true,"result":{"id":1,"username":"`+
					testUsername+`"}}`,
			)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustYAMLNode(t *testing.T, src string) *yaml.Node {
	t.Helper()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(src), &node))
	return &node
}
