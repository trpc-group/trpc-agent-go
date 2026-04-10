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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
	ocbrowser "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/browser"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	browserServerManagedPort = "19790"
	browserServerScheme      = "http"

	browserServerDirEnv = "OPENCLAW_BROWSER_SERVER_DIR"

	browserServerAddrEnv  = "OPENCLAW_BROWSER_SERVER_ADDR"
	browserServerTokenEnv = "OPENCLAW_BROWSER_SERVER_TOKEN"

	browserServerDirName    = "browser-server"
	browserServerPackage    = "package.json"
	browserServerBinDirName = "bin"
	browserServerScriptName = "openclaw-browser-server.js"
	browserServerNodeBin    = "node"

	browserServerStateExternal = "external"
	browserServerStateStarting = "starting"
	browserServerStateRunning  = "running"
	browserServerStateStopping = "stopping"
	browserServerStateStopped  = "stopped"
	browserServerStateFailed   = "failed"

	browserServerLogDirName  = "services"
	browserServerLogFileName = "browser-server.log"
	browserServerTailLines   = 40
)

const (
	browserServerProbeTimeout  = 1500 * time.Millisecond
	browserServerStartTimeout  = 20 * time.Second
	browserServerStartInterval = 200 * time.Millisecond
	browserServerStopTimeout   = 5 * time.Second
)

var (
	browserServerNow       = time.Now
	browserServerProbeFunc = probeBrowserServerEndpoint
	browserServerWorkDir   = defaultBrowserServerWorkDir
	browserServerCommand   = defaultBrowserServerCommand
)

type browserServerPlan struct {
	ProviderName string
	ServerURL    string
	Addr         string
	AuthToken    string
}

type browserManagedStatus = admin.BrowserManagedService

type browserServerProcessExit struct {
	Code int
	Err  error
}

type browserServerSup struct {
	mu sync.RWMutex

	plan browserServerPlan
	tail *browserServerTail

	state           string
	managed         bool
	pid             int
	workDir         string
	command         string
	logPath         string
	logRelativePath string
	startedAt       *time.Time
	stoppedAt       *time.Time
	exitCode        *int
	lastError       string
	stopRequested   bool

	cmd    *exec.Cmd
	doneCh chan browserServerProcessExit
}

type browserServerTail struct {
	mu      sync.Mutex
	pending string
	lines   []string
	limit   int
}

func maybeStartBrowserServerSupervisor(
	ctx context.Context,
	specs []pluginSpec,
	debugDir string,
) (*browserServerSup, error) {
	plan, ok := detectManagedBrowserServerPlan(specs)
	if !ok {
		return nil, nil
	}

	sup := newBrowserServerSupervisor(plan, debugDir)
	if err := sup.Start(ctx); err != nil {
		return sup, err
	}
	return sup, nil
}

func detectManagedBrowserServerPlan(
	specs []pluginSpec,
) (browserServerPlan, bool) {
	for i := range specs {
		spec := specs[i]
		if strings.TrimSpace(spec.Type) != toolProviderBrowser {
			continue
		}

		var cfg ocbrowser.Config
		if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
			continue
		}

		addr, ok := managedBrowserServerAddr(cfg.ServerURL)
		if !ok {
			continue
		}
		return browserServerPlan{
			ProviderName: strings.TrimSpace(spec.Name),
			ServerURL:    strings.TrimSpace(cfg.ServerURL),
			Addr:         addr,
			AuthToken:    strings.TrimSpace(cfg.AuthToken),
		}, true
	}
	return browserServerPlan{}, false
}

func managedBrowserServerAddr(raw string) (string, bool) {
	serverURL, err := parseBrowserServerURL(raw)
	if err != nil {
		return "", false
	}
	if !strings.EqualFold(serverURL.Scheme, browserServerScheme) {
		return "", false
	}

	host := strings.TrimSpace(serverURL.Hostname())
	port := strings.TrimSpace(serverURL.Port())
	if host == "" || port != browserServerManagedPort {
		return "", false
	}
	if !isManagedBrowserServerHost(host) {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

func parseBrowserServerURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("browser server url is empty")
	}
	if strings.Contains(trimmed, "://") {
		return url.Parse(trimmed)
	}
	return url.Parse(browserServerScheme + "://" + trimmed)
}

func isManagedBrowserServerHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func newBrowserServerSupervisor(
	plan browserServerPlan,
	debugDir string,
) *browserServerSup {
	return &browserServerSup{
		plan: plan,
		tail: newBrowserServerTail(browserServerTailLines),
		logPath: filepath.Join(
			strings.TrimSpace(debugDir),
			browserServerLogDirName,
			browserServerLogFileName,
		),
		logRelativePath: filepath.ToSlash(filepath.Join(
			browserServerLogDirName,
			browserServerLogFileName,
		)),
	}
}

func newBrowserServerTail(limit int) *browserServerTail {
	if limit <= 0 {
		limit = browserServerTailLines
	}
	return &browserServerTail{limit: limit}
}

func (t *browserServerTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.pending += string(p)
	for {
		idx := strings.IndexByte(t.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(t.pending[:idx])
		t.pending = t.pending[idx+1:]
		if line == "" {
			continue
		}
		t.append(line)
	}
	return len(p), nil
}

func (t *browserServerTail) Lines() []string {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	out := append([]string(nil), t.lines...)
	if strings.TrimSpace(t.pending) != "" {
		out = append(out, strings.TrimSpace(t.pending))
	}
	return out
}

func (t *browserServerTail) append(line string) {
	t.lines = append(t.lines, line)
	if len(t.lines) > t.limit {
		t.lines = append([]string(nil), t.lines[len(t.lines)-t.limit:]...)
	}
}

func (s *browserServerSup) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if err := s.probe(ctx); err == nil {
		s.mu.Lock()
		s.managed = false
		s.state = browserServerStateExternal
		s.lastError = ""
		s.mu.Unlock()
		return nil
	}
	return s.startManaged(ctx)
}

func (s *browserServerSup) startManaged(ctx context.Context) error {
	workDir, err := browserServerWorkDir()
	if err != nil {
		s.setFailed(err)
		return err
	}

	logFile, err := s.openLogFile()
	if err != nil {
		s.setFailed(err)
		return err
	}

	writer := io.MultiWriter(logFile, s.tail)
	env := append(os.Environ(), browserServerAddrEnv+"="+s.plan.Addr)
	if s.plan.AuthToken != "" {
		env = append(
			env,
			browserServerTokenEnv+"="+s.plan.AuthToken,
		)
	}

	cmd, command, err := browserServerCommand(
		workDir,
		env,
		writer,
		writer,
	)
	if err != nil {
		_ = logFile.Close()
		s.setFailed(err)
		return err
	}

	s.mu.Lock()
	s.workDir = workDir
	s.command = command
	s.lastError = ""
	s.mu.Unlock()

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		s.setFailed(err)
		return err
	}

	startedAt := browserServerNow()
	doneCh := make(chan browserServerProcessExit, 1)

	s.mu.Lock()
	s.managed = true
	s.pid = cmd.Process.Pid
	s.state = browserServerStateStarting
	s.startedAt = &startedAt
	s.stoppedAt = nil
	s.exitCode = nil
	s.stopRequested = false
	s.cmd = cmd
	s.doneCh = doneCh
	s.mu.Unlock()

	go s.waitForExit(cmd, logFile, doneCh)

	if err := s.waitUntilReady(ctx, doneCh); err != nil {
		_ = s.Close()
		s.setFailed(err)
		return err
	}

	s.mu.Lock()
	s.state = browserServerStateRunning
	s.lastError = ""
	s.mu.Unlock()
	return nil
}

func (s *browserServerSup) openLogFile() (*os.File, error) {
	path := strings.TrimSpace(s.logPath)
	if path == "" {
		return nil, fmt.Errorf("browser server log path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create browser server log dir: %w", err)
	}
	f, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open browser server log file: %w", err)
	}
	return f, nil
}

func (s *browserServerSup) waitUntilReady(
	ctx context.Context,
	doneCh <-chan browserServerProcessExit,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	startCtx, cancel := context.WithTimeout(
		ctx,
		browserServerStartTimeout,
	)
	defer cancel()

	ticker := time.NewTicker(browserServerStartInterval)
	defer ticker.Stop()

	for {
		if err := s.probe(startCtx); err == nil {
			return nil
		}

		select {
		case exit, ok := <-doneCh:
			if !ok {
				return fmt.Errorf("browser server exited before ready")
			}
			if exit.Err != nil {
				return fmt.Errorf(
					"browser server exited before ready: %w",
					exit.Err,
				)
			}
			return fmt.Errorf(
				"browser server exited before ready with code %d",
				exit.Code,
			)
		case <-ticker.C:
		case <-startCtx.Done():
			return fmt.Errorf(
				"wait for browser server ready: %w",
				startCtx.Err(),
			)
		}
	}
}

func (s *browserServerSup) waitForExit(
	cmd *exec.Cmd,
	logFile *os.File,
	doneCh chan<- browserServerProcessExit,
) {
	err := cmd.Wait()
	_ = logFile.Close()

	exitCode := processExitCode(cmd.ProcessState)
	exit := browserServerProcessExit{
		Code: exitCode,
		Err:  err,
	}
	doneCh <- exit
	close(doneCh)

	stoppedAt := browserServerNow()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.pid = 0
	s.cmd = nil
	s.doneCh = nil
	s.stoppedAt = &stoppedAt
	s.exitCode = &exitCode

	if s.stopRequested {
		s.state = browserServerStateStopped
		return
	}

	if err != nil {
		s.state = browserServerStateFailed
		s.lastError = err.Error()
		return
	}

	s.state = browserServerStateStopped
}

func processExitCode(state *os.ProcessState) int {
	if state == nil {
		return 0
	}
	return state.ExitCode()
}

func (s *browserServerSup) probe(ctx context.Context) error {
	timeout := browserServerProbeTimeout
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			untilDeadline := time.Until(deadline)
			if untilDeadline <= 0 {
				return context.DeadlineExceeded
			}
			if untilDeadline < timeout {
				timeout = untilDeadline
			}
		}
	}

	probeCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return browserServerProbeFunc(
		probeCtx,
		s.plan.ServerURL,
		s.plan.AuthToken,
	)
}

func probeBrowserServerEndpoint(
	ctx context.Context,
	serverURL string,
	authToken string,
) error {
	endpoint := strings.TrimRight(strings.TrimSpace(serverURL), "/") +
		"/profiles"
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint,
		nil,
	)
	if err != nil {
		return fmt.Errorf("build browser server probe: %w", err)
	}
	if strings.TrimSpace(authToken) != "" {
		req.Header.Set(
			"Authorization",
			"Bearer "+strings.TrimSpace(authToken),
		)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func defaultBrowserServerWorkDir() (string, error) {
	if override := strings.TrimSpace(
		os.Getenv(browserServerDirEnv),
	); override != "" {
		if isBrowserServerWorkDir(override) {
			return override, nil
		}
		return "", fmt.Errorf(
			"%s does not point to openclaw/browser-server",
			browserServerDirEnv,
		)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve browser server work dir: %w", err)
	}

	for dir := cwd; ; dir = filepath.Dir(dir) {
		if candidate, ok := browserServerWorkDirIn(dir); ok {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", fmt.Errorf(
		"browser server work dir not found; set %s or run from "+
			"a repository checkout",
		browserServerDirEnv,
	)
}

func browserServerWorkDirIn(root string) (string, bool) {
	candidates := []string{
		filepath.Join(root, browserServerDirName),
		filepath.Join(root, appName, browserServerDirName),
	}
	for i := range candidates {
		if isBrowserServerWorkDir(candidates[i]) {
			return candidates[i], true
		}
	}
	return "", false
}

func isBrowserServerWorkDir(dir string) bool {
	if !dirExists(dir) {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, browserServerPackage)); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(
		dir,
		browserServerBinDirName,
		browserServerScriptName,
	))
	return err == nil
}

func defaultBrowserServerCommand(
	workDir string,
	env []string,
	stdout io.Writer,
	stderr io.Writer,
) (*exec.Cmd, string, error) {
	nodePath, err := exec.LookPath(browserServerNodeBin)
	if err != nil {
		return nil, "", fmt.Errorf(
			"find %s: %w",
			browserServerNodeBin,
			err,
		)
	}

	script := filepath.Join(
		".",
		browserServerBinDirName,
		browserServerScriptName,
	)
	cmd := exec.Command(nodePath, script)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd, nodePath + " " + script, nil
}

func (s *browserServerSup) setFailed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = browserServerStateFailed
	if err != nil {
		s.lastError = err.Error()
	}
	if s.cmd == nil {
		s.managed = false
		s.pid = 0
	}
}

func (s *browserServerSup) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	if s.cmd == nil || s.cmd.Process == nil {
		s.mu.Unlock()
		return nil
	}

	cmd := s.cmd
	doneCh := s.doneCh
	s.stopRequested = true
	s.state = browserServerStateStopping
	s.mu.Unlock()

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			return fmt.Errorf(
				"stop browser server: signal=%v kill=%v",
				err,
				killErr,
			)
		}
	}

	select {
	case <-doneCh:
		return nil
	case <-time.After(browserServerStopTimeout):
		if err := cmd.Process.Kill(); err != nil {
			return fmt.Errorf("kill browser server: %w", err)
		}
		select {
		case <-doneCh:
			return nil
		case <-time.After(browserServerStopTimeout):
			return fmt.Errorf("wait for browser server stop timed out")
		}
	}
}

func (s *browserServerSup) startupLines() []startupLogLine {
	if s == nil {
		return nil
	}

	status := s.BrowserManagedStatus()
	if !status.Enabled {
		return nil
	}

	switch status.State {
	case browserServerStateExternal:
		return []startupLogLine{{
			text: fmt.Sprintf(
				"Browser server already available at %s",
				status.URL,
			),
		}}
	case browserServerStateRunning:
		lines := []startupLogLine{{
			text: fmt.Sprintf(
				"Browser server auto-started at %s (pid=%d)",
				status.URL,
				status.PID,
			),
		}}
		if status.LogPath != "" {
			lines = append(lines, startupLogLine{
				text: fmt.Sprintf(
					"Browser server log: %s",
					status.LogPath,
				),
			})
		}
		return lines
	case browserServerStateFailed:
		return []startupLogLine{{
			warn: true,
			text: fmt.Sprintf(
				"Browser server auto-start failed: %s",
				status.LastError,
			),
		}}
	default:
		return nil
	}
}

func (s *browserServerSup) BrowserManagedStatus() browserManagedStatus {
	if s == nil {
		return browserManagedStatus{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	status := browserManagedStatus{
		Enabled:         true,
		Managed:         s.managed,
		State:           strings.TrimSpace(s.state),
		URL:             strings.TrimSpace(s.plan.ServerURL),
		PID:             s.pid,
		WorkDir:         strings.TrimSpace(s.workDir),
		Command:         strings.TrimSpace(s.command),
		LogPath:         strings.TrimSpace(s.logPath),
		LogRelativePath: strings.TrimSpace(s.logRelativePath),
		LastError:       strings.TrimSpace(s.lastError),
		RecentLogs:      s.tail.Lines(),
	}
	if s.startedAt != nil {
		startedAt := *s.startedAt
		status.StartedAt = &startedAt
	}
	if s.stoppedAt != nil {
		stoppedAt := *s.stoppedAt
		status.StoppedAt = &stoppedAt
	}
	if s.exitCode != nil {
		exitCode := *s.exitCode
		status.ExitCode = &exitCode
	}
	return status
}
