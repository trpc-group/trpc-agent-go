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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

func TestIsPolicy(t *testing.T) {
	require.True(t, isPolicy(" AllowList ", "allowlist"))
	require.False(t, isPolicy("open", "allowlist"))
}

func TestCheckTimeout(t *testing.T) {
	require.True(t, checkTimeout(0))
	require.True(t, checkTimeout(telegramLongPollTimeout+telegramTimeoutSlack))
	require.False(t, checkTimeout(time.Second))
}

func TestCheckPolicies(t *testing.T) {
	require.True(t, checkPolicies("", "", "", ""))
	require.False(t, checkPolicies("allowlist", "", "", ""))
	require.True(t, checkPolicies("allowlist", "", "1", ""))
	require.False(t, checkPolicies("", "allowlist", "", ""))
	require.True(t, checkPolicies("", "allowlist", "", "10"))
}

func TestPrintBot(t *testing.T) {
	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	printBot(tgapi.User{ID: 1, Username: "bot"})
	printBot(tgapi.User{ID: 2})

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "Bot: @bot (id 1)")
	require.Contains(t, string(out), "Bot: id 2")
}

func TestCheckPairingStore(t *testing.T) {
	stateDir := t.TempDir()
	me := tgapi.User{ID: 123, Username: "bot"}

	require.True(t, checkPairingStore(
		context.Background(),
		stateDir,
		"open",
		me,
	))

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	require.True(t, checkPairingStore(
		context.Background(),
		stateDir,
		"",
		me,
	))

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "Pairing store:")
}

func TestRunDoctor_TelegramDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	code := runDoctor([]string{"-telegram-token", ""})
	require.Equal(t, 0, code)

	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(out), "Telegram: disabled")
}

func TestRunDoctor_WithTelegram_BaseURLOverride(t *testing.T) {
	token := "token"

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
		case "/bot" + token + "/getWebhookInfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(
				w,
				`{"ok":true,"result":{"url":""}}`,
			)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv(telegramBaseURLEnvName, srv.URL)

	code := runDoctor([]string{
		"-telegram-token", token,
		"-state-dir", t.TempDir(),
	})
	require.Equal(t, 0, code)
}

func TestCheckWebhook_NotSet(t *testing.T) {
	token := "token"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bot"+token+"/getWebhookInfo", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
	}))
	t.Cleanup(srv.Close)

	c, err := tgapi.New(
		token,
		tgapi.WithBaseURL(srv.URL),
		tgapi.WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	require.True(t, checkWebhook(context.Background(), c))
}

func TestCheckWebhook_Set(t *testing.T) {
	token := "token"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/bot"+token+"/getWebhookInfo", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			w,
			`{"ok":true,"result":{"url":"https://x","pending_update_count":1}}`,
		)
	}))
	t.Cleanup(srv.Close)

	c, err := tgapi.New(
		token,
		tgapi.WithBaseURL(srv.URL),
		tgapi.WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	require.False(t, checkWebhook(context.Background(), c))
}

func TestCheckWebhook_Error(t *testing.T) {
	token := "token"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, err := tgapi.New(
		token,
		tgapi.WithBaseURL(srv.URL),
		tgapi.WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	require.False(t, checkWebhook(context.Background(), c))
}
