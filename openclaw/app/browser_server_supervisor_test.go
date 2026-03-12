//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const browserServerHelperEnv = "OPENCLAW_TEST_BROWSER_SERVER_HELPER"

func TestDetectManagedBrowserServerPlan(t *testing.T) {
	t.Parallel()

	plan, ok := detectManagedBrowserServerPlan([]pluginSpec{{
		Type: toolProviderBrowser,
		Name: "browser-runtime",
		Config: yamlNode(t, `
default_profile: "openclaw"
server_url: "http://127.0.0.1:19790"
auth_token: "secret"
profiles:
  - name: "openclaw"
`),
	}})
	require.True(t, ok)
	require.Equal(t, "browser-runtime", plan.ProviderName)
	require.Equal(t, "http://127.0.0.1:19790", plan.ServerURL)
	require.Equal(t, "127.0.0.1:19790", plan.Addr)
	require.Equal(t, "secret", plan.AuthToken)

	_, ok = detectManagedBrowserServerPlan([]pluginSpec{{
		Type: toolProviderBrowser,
		Config: yamlNode(t, `
server_url: "http://browser.example:19790"
profiles:
  - name: "openclaw"
`),
	}})
	require.False(t, ok)
}

func TestBrowserServerSupervisorUsesExistingServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, "/profiles", r.URL.Path)
		_, _ = io.WriteString(
			w,
			`{"profiles":[{"name":"openclaw"}]}`,
		)
	}))
	t.Cleanup(server.Close)

	sup := newBrowserServerSupervisor(
		browserServerPlan{
			ServerURL: server.URL,
			Addr:      strings.TrimPrefix(server.URL, "http://"),
		},
		t.TempDir(),
	)

	require.NoError(t, sup.Start(context.Background()))

	status := sup.BrowserManagedStatus()
	require.True(t, status.Enabled)
	require.False(t, status.Managed)
	require.Equal(t, browserServerStateExternal, status.State)
	require.Equal(t, server.URL, status.URL)
}

func TestBrowserServerSupervisorStartsAndStopsManagedProcess(
	t *testing.T,
) {
	addr := freeLoopbackAddr(t)
	debugDir := t.TempDir()

	restore := stubBrowserServerLauncher(t, addr)
	defer restore()

	sup := newBrowserServerSupervisor(
		browserServerPlan{
			ServerURL: "http://" + addr,
			Addr:      addr,
		},
		debugDir,
	)

	require.NoError(t, sup.Start(context.Background()))

	status := sup.BrowserManagedStatus()
	require.True(t, status.Enabled)
	require.True(t, status.Managed)
	require.Equal(t, browserServerStateRunning, status.State)
	require.NotZero(t, status.PID)
	require.NotEmpty(t, status.LogPath)
	require.NotEmpty(t, status.WorkDir)
	require.Equal(t, "helper-browser-server", status.Command)
	require.Contains(
		t,
		strings.Join(status.RecentLogs, "\n"),
		"helper browser server listening",
	)

	data, err := os.ReadFile(status.LogPath)
	require.NoError(t, err)
	require.Contains(
		t,
		string(data),
		"helper browser server listening",
	)

	require.NoError(t, sup.Close())
	require.Eventually(t, func() bool {
		return sup.BrowserManagedStatus().State ==
			browserServerStateStopped
	}, 5*time.Second, 50*time.Millisecond)
}

func TestBrowserServerSupervisorRecordsStartFailure(t *testing.T) {
	original := browserServerWorkDir
	browserServerWorkDir = func() (string, error) {
		return "", fmt.Errorf("missing browser-server dir")
	}
	t.Cleanup(func() {
		browserServerWorkDir = original
	})

	sup := newBrowserServerSupervisor(
		browserServerPlan{
			ServerURL: "http://127.0.0.1:19790",
			Addr:      "127.0.0.1:19790",
		},
		t.TempDir(),
	)

	err := sup.Start(context.Background())
	require.Error(t, err)

	status := sup.BrowserManagedStatus()
	require.True(t, status.Enabled)
	require.False(t, status.Managed)
	require.Equal(t, browserServerStateFailed, status.State)
	require.Contains(t, status.LastError, "missing browser-server dir")
}

func TestBrowserServerSupervisorHelperProcess(t *testing.T) {
	if os.Getenv(browserServerHelperEnv) == "" {
		return
	}

	addr := strings.TrimSpace(os.Getenv(browserServerAddrEnv))
	if addr == "" {
		_, _ = fmt.Fprintln(os.Stderr, "missing browser server addr")
		os.Exit(2)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/profiles", func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = io.WriteString(
			w,
			`{"profiles":[{"name":"openclaw","state":"ready"}]}`,
		)
	})

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(3)
	}

	srv := &http.Server{Handler: mux}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	_, _ = fmt.Fprintln(os.Stdout, "helper browser server listening")

	err = srv.Serve(listener)
	signal.Stop(signals)
	if err != nil && err != http.ErrServerClosed {
		_, _ = fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		os.Exit(4)
	}
	os.Exit(0)
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

func stubBrowserServerLauncher(
	t *testing.T,
	addr string,
) func() {
	t.Helper()

	originalWorkDir := browserServerWorkDir
	originalCommand := browserServerCommand

	workDir := t.TempDir()
	browserServerWorkDir = func() (string, error) {
		return workDir, nil
	}
	browserServerCommand = func(
		_ string,
		env []string,
		stdout io.Writer,
		stderr io.Writer,
	) (*exec.Cmd, string, error) {
		cmd := exec.Command(
			os.Args[0],
			"-test.run=TestBrowserServerSupervisorHelperProcess",
		)
		cmd.Env = append(
			env,
			browserServerHelperEnv+"=1",
			browserServerAddrEnv+"="+addr,
		)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		return cmd, "helper-browser-server", nil
	}

	return func() {
		browserServerWorkDir = originalWorkDir
		browserServerCommand = originalCommand
	}
}
