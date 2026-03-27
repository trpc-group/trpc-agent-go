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
	"path/filepath"
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

func TestDetectManagedBrowserServerPlan_SkipsInvalidSpecs(t *testing.T) {
	t.Parallel()

	plan, ok := detectManagedBrowserServerPlan([]pluginSpec{
		{Type: "search", Name: "web"},
		{
			Type: toolProviderBrowser,
			Name: "broken",
			Config: yamlNode(t, `
unknown_field: true
`),
		},
		{
			Type: toolProviderBrowser,
			Name: "browser-runtime",
			Config: yamlNode(t, `
server_url: "http://127.0.0.1:19790"
auth_token: "secret"
profiles:
  - name: "openclaw"
`),
		},
	})
	require.True(t, ok)
	require.Equal(t, "browser-runtime", plan.ProviderName)
	require.Equal(t, "secret", plan.AuthToken)
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

func TestMaybeStartBrowserServerSupervisorWithoutPlan(t *testing.T) {
	t.Parallel()

	sup, err := maybeStartBrowserServerSupervisor(
		context.Background(),
		nil,
		t.TempDir(),
	)
	require.NoError(t, err)
	require.Nil(t, sup)
}

func TestMaybeStartBrowserServerSupervisorReturnsSupOnError(
	t *testing.T,
) {
	original := browserServerWorkDir
	browserServerWorkDir = func() (string, error) {
		return "", fmt.Errorf("missing browser-server dir")
	}
	t.Cleanup(func() {
		browserServerWorkDir = original
	})

	sup, err := maybeStartBrowserServerSupervisor(
		context.Background(),
		[]pluginSpec{{
			Type: toolProviderBrowser,
			Name: "browser-runtime",
			Config: yamlNode(t, `
server_url: "http://127.0.0.1:19790"
profiles:
  - name: "openclaw"
`),
		}},
		t.TempDir(),
	)
	require.Error(t, err)
	require.NotNil(t, sup)
	require.Equal(
		t,
		browserServerStateFailed,
		sup.BrowserManagedStatus().State,
	)
}

func TestManagedBrowserServerHelpers(t *testing.T) {
	t.Parallel()

	serverURL, err := parseBrowserServerURL("127.0.0.1:19790")
	require.NoError(t, err)
	require.Equal(t, browserServerScheme, serverURL.Scheme)
	require.Equal(t, "127.0.0.1:19790", serverURL.Host)

	_, err = parseBrowserServerURL(" ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")

	addr, ok := managedBrowserServerAddr("localhost:19790")
	require.True(t, ok)
	require.Equal(t, "localhost:19790", addr)

	_, ok = managedBrowserServerAddr("https://127.0.0.1:19790")
	require.False(t, ok)

	_, ok = managedBrowserServerAddr("http://127.0.0.1:17777")
	require.False(t, ok)

	_, ok = managedBrowserServerAddr("http://example.com:19790")
	require.False(t, ok)

	require.True(t, isManagedBrowserServerHost("localhost"))
	require.True(t, isManagedBrowserServerHost("127.0.0.1"))
	require.False(t, isManagedBrowserServerHost("example.com"))
}

func TestBrowserServerWorkDirHelpers(t *testing.T) {
	root := t.TempDir()
	direct := filepath.Join(root, browserServerDirName)
	writeBrowserServerWorkDir(t, direct)

	require.True(t, isBrowserServerWorkDir(direct))
	require.False(
		t,
		isBrowserServerWorkDir(filepath.Join(root, "missing")),
	)

	found, ok := browserServerWorkDirIn(root)
	require.True(t, ok)
	require.Equal(t, direct, found)

	nestedRoot := t.TempDir()
	nested := filepath.Join(
		nestedRoot,
		appName,
		browserServerDirName,
	)
	writeBrowserServerWorkDir(t, nested)

	found, ok = browserServerWorkDirIn(nestedRoot)
	require.True(t, ok)
	require.Equal(t, nested, found)

	t.Run("env override", func(t *testing.T) {
		t.Setenv(browserServerDirEnv, direct)

		workDir, err := defaultBrowserServerWorkDir()
		require.NoError(t, err)
		require.Equal(t, direct, workDir)
	})

	t.Run("bad env override", func(t *testing.T) {
		t.Setenv(
			browserServerDirEnv,
			filepath.Join(root, "bad-browser-server"),
		)

		_, err := defaultBrowserServerWorkDir()
		require.Error(t, err)
		require.Contains(t, err.Error(), browserServerDirEnv)
	})

	t.Run("search from cwd", func(t *testing.T) {
		t.Setenv(browserServerDirEnv, "")

		searchRoot := t.TempDir()
		want := filepath.Join(
			searchRoot,
			appName,
			browserServerDirName,
		)
		writeBrowserServerWorkDir(t, want)

		cwd, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, os.Chdir(cwd))
		})

		workingDir := filepath.Join(searchRoot, appName, "tmp", "cwd")
		require.NoError(t, os.MkdirAll(workingDir, 0o755))
		require.NoError(t, os.Chdir(workingDir))

		workDir, err := defaultBrowserServerWorkDir()
		require.NoError(t, err)
		resolvedWant, err := filepath.EvalSymlinks(want)
		require.NoError(t, err)
		resolvedWorkDir, err := filepath.EvalSymlinks(workDir)
		require.NoError(t, err)
		require.Equal(t, resolvedWant, resolvedWorkDir)
	})
}

func TestDefaultBrowserServerCommand(t *testing.T) {
	workDir := t.TempDir()

	nodePath, err := exec.LookPath(browserServerNodeBin)
	if err != nil {
		t.Skip("node not found")
	}

	cmd, command, err := defaultBrowserServerCommand(
		workDir,
		[]string{"A=B"},
		io.Discard,
		io.Discard,
	)
	require.NoError(t, err)
	require.Equal(t, nodePath, cmd.Path)
	require.Equal(t, workDir, cmd.Dir)
	require.Equal(t, []string{"A=B"}, cmd.Env)
	require.Contains(t, command, browserServerScriptName)
	require.Equal(
		t,
		browserServerScriptName,
		filepath.Base(cmd.Args[1]),
	)

	t.Setenv("PATH", "")
	_, _, err = defaultBrowserServerCommand(
		workDir,
		nil,
		io.Discard,
		io.Discard,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "find node")
}

func TestBrowserServerSupervisorStartupLines(t *testing.T) {
	t.Parallel()

	var nilSup *browserServerSup
	require.Nil(t, nilSup.startupLines())

	external := newBrowserServerSupervisor(
		browserServerPlan{ServerURL: "http://127.0.0.1:19790"},
		t.TempDir(),
	)
	external.state = browserServerStateExternal
	lines := external.startupLines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0].text, "already available")

	running := newBrowserServerSupervisor(
		browserServerPlan{ServerURL: "http://127.0.0.1:19790"},
		t.TempDir(),
	)
	running.state = browserServerStateRunning
	running.managed = true
	running.pid = 42
	lines = running.startupLines()
	require.Len(t, lines, 2)
	require.Contains(t, lines[0].text, "auto-started")
	require.Contains(t, lines[1].text, "Browser server log:")

	failed := newBrowserServerSupervisor(
		browserServerPlan{ServerURL: "http://127.0.0.1:19790"},
		t.TempDir(),
	)
	failed.state = browserServerStateFailed
	failed.lastError = "boom"
	lines = failed.startupLines()
	require.Len(t, lines, 1)
	require.True(t, lines[0].warn)
	require.Contains(t, lines[0].text, "boom")

	starting := newBrowserServerSupervisor(
		browserServerPlan{ServerURL: "http://127.0.0.1:19790"},
		t.TempDir(),
	)
	starting.state = browserServerStateStarting
	require.Nil(t, starting.startupLines())
}

func TestBrowserServerTailHelpers(t *testing.T) {
	t.Parallel()

	tail := newBrowserServerTail(0)
	require.Equal(t, browserServerTailLines, tail.limit)

	_, err := tail.Write([]byte(" first \n\n second "))
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, tail.Lines())

	limited := newBrowserServerTail(2)
	limited.append("one")
	limited.append("two")
	limited.append("three")
	require.Equal(t, []string{"two", "three"}, limited.Lines())

	var nilTail *browserServerTail
	require.Nil(t, nilTail.Lines())
	require.Zero(t, processExitCode(nil))
}

func TestProbeBrowserServerEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, "/profiles", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	require.NoError(
		t,
		probeBrowserServerEndpoint(
			context.Background(),
			server.URL,
			"secret",
		),
	)

	badServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(badServer.Close)

	err := probeBrowserServerEndpoint(
		context.Background(),
		badServer.URL,
		"",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected status")
}

func TestBrowserServerSupervisorWaitUntilReadyExitPaths(
	t *testing.T,
) {
	originalProbe := browserServerProbeFunc
	browserServerProbeFunc = func(
		ctx context.Context,
		serverURL string,
		authToken string,
	) error {
		return fmt.Errorf("not ready")
	}
	t.Cleanup(func() {
		browserServerProbeFunc = originalProbe
	})

	sup := newBrowserServerSupervisor(
		browserServerPlan{ServerURL: "http://127.0.0.1:19790"},
		t.TempDir(),
	)

	t.Run("closed channel", func(t *testing.T) {
		doneCh := make(chan browserServerProcessExit)
		close(doneCh)

		err := sup.waitUntilReady(context.Background(), doneCh)
		require.Error(t, err)
		require.Contains(t, err.Error(), "exited before ready")
	})

	t.Run("exit error", func(t *testing.T) {
		doneCh := make(chan browserServerProcessExit, 1)
		doneCh <- browserServerProcessExit{
			Code: 2,
			Err:  fmt.Errorf("boom"),
		}
		close(doneCh)

		err := sup.waitUntilReady(context.Background(), doneCh)
		require.Error(t, err)
		require.Contains(t, err.Error(), "boom")
	})

	t.Run("exit code", func(t *testing.T) {
		doneCh := make(chan browserServerProcessExit, 1)
		doneCh <- browserServerProcessExit{Code: 2}
		close(doneCh)

		err := sup.waitUntilReady(context.Background(), doneCh)
		require.Error(t, err)
		require.Contains(t, err.Error(), "code 2")
	})

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Millisecond,
		)
		defer cancel()

		doneCh := make(chan browserServerProcessExit)
		err := sup.waitUntilReady(ctx, doneCh)
		require.Error(t, err)
		require.Contains(t, err.Error(), "wait for browser server ready")
	})
}

func TestBrowserServerSupervisorOpenLogFileRequiresPath(
	t *testing.T,
) {
	t.Parallel()

	sup := newBrowserServerSupervisor(
		browserServerPlan{ServerURL: "http://127.0.0.1:19790"},
		t.TempDir(),
	)
	sup.logPath = ""

	_, err := sup.openLogFile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "log path is empty")
}

func TestBrowserServerSupervisorStartManagedErrorPaths(
	t *testing.T,
) {
	workDir := t.TempDir()
	writeBrowserServerWorkDir(t, workDir)

	originalWorkDir := browserServerWorkDir
	originalCommand := browserServerCommand
	browserServerWorkDir = func() (string, error) {
		return workDir, nil
	}
	t.Cleanup(func() {
		browserServerWorkDir = originalWorkDir
		browserServerCommand = originalCommand
	})

	t.Run("command build error", func(t *testing.T) {
		var gotEnv []string
		browserServerCommand = func(
			workDir string,
			env []string,
			stdout io.Writer,
			stderr io.Writer,
		) (*exec.Cmd, string, error) {
			gotEnv = append([]string(nil), env...)
			return nil, "", fmt.Errorf("build command failed")
		}

		sup := newBrowserServerSupervisor(
			browserServerPlan{
				ServerURL: "http://127.0.0.1:19790",
				Addr:      "127.0.0.1:19790",
				AuthToken: "secret",
			},
			t.TempDir(),
		)

		err := sup.startManaged(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "build command failed")
		require.Contains(
			t,
			strings.Join(gotEnv, "\n"),
			browserServerTokenEnv+"=secret",
		)
		require.Equal(
			t,
			browserServerStateFailed,
			sup.BrowserManagedStatus().State,
		)
	})

	t.Run("start error", func(t *testing.T) {
		browserServerCommand = func(
			workDir string,
			env []string,
			stdout io.Writer,
			stderr io.Writer,
		) (*exec.Cmd, string, error) {
			return exec.Command("/path/does/not/exist"), "missing", nil
		}

		sup := newBrowserServerSupervisor(
			browserServerPlan{
				ServerURL: "http://127.0.0.1:19790",
				Addr:      "127.0.0.1:19790",
			},
			t.TempDir(),
		)

		err := sup.startManaged(context.Background())
		require.Error(t, err)
		require.Equal(
			t,
			browserServerStateFailed,
			sup.BrowserManagedStatus().State,
		)
	})
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

func writeBrowserServerWorkDir(t *testing.T, dir string) {
	t.Helper()

	scriptPath := filepath.Join(
		dir,
		browserServerBinDirName,
		browserServerScriptName,
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, browserServerPackage),
			[]byte("{}\n"),
			0o600,
		),
	)
	require.NoError(
		t,
		os.WriteFile(scriptPath, []byte("console.log('ok')\n"), 0o700),
	)
}
