//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package local

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	defaultInteractiveMaxLines = 20_000
	defaultInteractiveKillWait = 2 * time.Second
	envPathKey                 = "PATH"
	envPathExtKey              = "PATHEXT"
)

var errInteractiveTTYWindows = errors.New(
	"interactive tty is not supported on windows",
)

type interactiveSession struct {
	id      string
	command string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	closeIO func() error
	cancel  context.CancelFunc

	doneCh chan struct{}
	ioDone chan struct{}
	ioWG   sync.WaitGroup

	// pipeDrain closes parent-held stdout/stderr write ends (os.Pipe path only).
	// Called after cmd.Wait() so read goroutines observe EOF before ioWG.Wait().
	pipeDrain func()

	mu       sync.Mutex
	started  time.Time
	finished time.Time
	exitCode int
	timedOut bool
	duration time.Duration

	lineBase   int
	lines      []string
	partial    string
	pollCursor int
	maxLines   int
	closeOnce  sync.Once
	stdout     strings.Builder
	stderr     strings.Builder
}

func newInteractiveSession(
	id string,
	command string,
	maxLines int,
) *interactiveSession {
	return &interactiveSession{
		id:       id,
		command:  command,
		doneCh:   make(chan struct{}),
		ioDone:   make(chan struct{}),
		started:  time.Now(),
		maxLines: maxLines,
	}
}

func (s *interactiveSession) ID() string { return s.id }

func (s *interactiveSession) Poll(limit *int) codeexecutor.ProgramPoll {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := s.pollCursor
	if start < s.lineBase {
		start = s.lineBase
		s.pollCursor = start
	}
	end := s.lineBase + len(s.lines)
	if limit != nil && *limit > 0 {
		if want := start + *limit; want < end {
			end = want
		}
	}

	from := start - s.lineBase
	to := end - s.lineBase
	out := strings.Join(s.lines[from:to], "\n")
	if end == s.lineBase+len(s.lines) && s.partial != "" {
		if out != "" {
			out += "\n"
		}
		out += s.partial
	}
	s.pollCursor = end

	res := codeexecutor.ProgramPoll{
		Output:     out,
		Offset:     start,
		NextOffset: end,
	}
	if s.finished.IsZero() {
		res.Status = codeexecutor.ProgramStatusRunning
		return res
	}
	res.Status = codeexecutor.ProgramStatusExited
	code := s.exitCode
	res.ExitCode = &code
	return res
}

func (s *interactiveSession) Log(
	offset *int,
	limit *int,
) codeexecutor.ProgramLog {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := s.lineBase
	end := s.lineBase + len(s.lines)

	if offset != nil {
		start = *offset
	}
	if start < s.lineBase {
		start = s.lineBase
	}
	if start > end {
		start = end
	}
	if limit != nil && *limit > 0 {
		if want := start + *limit; want < end {
			end = want
		}
	}

	from := start - s.lineBase
	to := end - s.lineBase
	out := strings.Join(s.lines[from:to], "\n")
	if end == s.lineBase+len(s.lines) && s.partial != "" {
		if out != "" {
			out += "\n"
		}
		out += s.partial
	}
	return codeexecutor.ProgramLog{
		Output:     out,
		Offset:     start,
		NextOffset: end,
	}
}

func (s *interactiveSession) Write(
	data string,
	newline bool,
) error {
	if data == "" && !newline {
		return nil
	}

	s.mu.Lock()
	stdin := s.stdin
	running := s.finished.IsZero()
	s.mu.Unlock()

	if !running {
		return errors.New("session is not running")
	}
	if stdin == nil {
		return errors.New("stdin is not available")
	}

	text := data
	if newline {
		text += "\n"
	}
	_, err := io.WriteString(stdin, text)
	return err
}

func (s *interactiveSession) Kill(grace time.Duration) error {
	s.mu.Lock()
	cmd := s.cmd
	cancel := s.cancel
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		if cancel != nil {
			cancel()
		}
		return nil
	}

	if runtime.GOOS != "windows" {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-s.doneCh:
		if cancel != nil {
			cancel()
		}
		return nil
	case <-time.After(grace):
		if cancel != nil {
			cancel()
		}
		err := cmd.Process.Kill()
		if err != nil && errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
}

func (s *interactiveSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.closeIO != nil {
			err = s.closeIO()
		}
	})
	return err
}

func (s *interactiveSession) State() codeexecutor.ProgramState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := codeexecutor.ProgramState{
		Status: codeexecutor.ProgramStatusRunning,
	}
	if s.finished.IsZero() {
		return state
	}
	state.Status = codeexecutor.ProgramStatusExited
	code := s.exitCode
	state.ExitCode = &code
	return state
}

func (s *interactiveSession) RunResult() codeexecutor.RunResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return codeexecutor.RunResult{
		Stdout:   s.stdout.String(),
		Stderr:   s.stderr.String(),
		ExitCode: s.exitCode,
		Duration: s.duration,
		TimedOut: s.timedOut,
	}
}

func (s *interactiveSession) markDone(
	exitCode int,
	duration time.Duration,
	timedOut bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished.IsZero() {
		return
	}
	if s.partial != "" {
		s.lines = append(s.lines, s.partial)
		s.partial = ""
	}
	s.exitCode = exitCode
	s.duration = duration
	s.timedOut = timedOut
	s.finished = time.Now()
	close(s.doneCh)
}

func (s *interactiveSession) readFrom(
	r io.Reader,
	stream string,
) {
	if r == nil {
		return
	}
	rd := bufio.NewReaderSize(r, 32*1024)
	buf := make([]byte, 4096)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			s.appendOutput(string(buf[:n]), stream)
		}
		if err != nil {
			return
		}
	}
}

func (s *interactiveSession) appendOutput(
	chunk string,
	stream string,
) {
	text := strings.ReplaceAll(chunk, "\r\n", "\n")

	s.mu.Lock()
	defer s.mu.Unlock()

	switch stream {
	case "stderr":
		s.stderr.WriteString(text)
	default:
		s.stdout.WriteString(text)
	}

	text = s.partial + text
	parts := strings.Split(text, "\n")
	if len(parts) == 0 {
		return
	}
	s.partial = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		s.lines = append(s.lines, line)
	}
	s.trimLocked()
}

func (s *interactiveSession) trimLocked() {
	if s.maxLines <= 0 {
		return
	}
	if len(s.lines) <= s.maxLines {
		return
	}
	drop := len(s.lines) - s.maxLines
	s.lines = s.lines[drop:]
	s.lineBase += drop
	if s.pollCursor < s.lineBase {
		s.pollCursor = s.lineBase
	}
}

// StartProgram starts an interactive program in the workspace.
func (r *Runtime) StartProgram(
	ctx context.Context,
	ws codeexecutor.Workspace,
	spec codeexecutor.InteractiveProgramSpec,
) (codeexecutor.ProgramSession, error) {
	cwd := filepath.Join(ws.Path, filepath.Clean(spec.Cwd))
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return nil, err
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout()
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)

	env, err := r.buildProgramEnv(ws, spec.RunProgramSpec)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd := newLocalProgramCommand(
		tctx,
		cwd,
		spec.RunProgramSpec,
		env,
	)

	sess := newInteractiveSession(
		uuid.NewString(),
		formatInteractiveCommand(spec.Cmd, spec.Args),
		defaultInteractiveMaxLines,
	)
	sess.cmd = cmd
	sess.cancel = cancel
	startedAt := time.Now()

	if spec.TTY {
		if runtime.GOOS == "windows" {
			cancel()
			return nil, errInteractiveTTYWindows
		}
		master, closeIO, err := startPTY(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		sess.stdin = master
		sess.closeIO = closeIO
		sess.ioWG.Add(1)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(master, "stdout")
		}()
	} else {
		stdin, stdout, stderr, closeWritePipes, err := startPipes(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		sess.stdin = stdin
		sess.pipeDrain = closeWritePipes
		sess.closeIO = func() error {
			closeWritePipes()
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			return nil
		}
		if err := cmd.Start(); err != nil {
			cancel()
			_ = sess.Close()
			return nil, err
		}
		sess.ioWG.Add(2)
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stdout, "stdout")
		}()
		go func() {
			defer sess.ioWG.Done()
			sess.readFrom(stderr, "stderr")
		}()
	}

	go func() {
		err := cmd.Wait()
		if sess.pipeDrain != nil {
			sess.pipeDrain()
		}
		sess.ioWG.Wait()
		close(sess.ioDone)
		sess.markDone(
			interactiveExitCode(err),
			time.Since(startedAt),
			errors.Is(tctx.Err(), context.DeadlineExceeded),
		)
		cancel()
		_ = sess.Close()
	}()

	if spec.Stdin != "" {
		if err := sess.Write(spec.Stdin, false); err != nil {
			_ = sess.Kill(defaultInteractiveKillWait)
			_ = sess.Close()
			return nil, err
		}
	}
	return sess, nil
}

func (r *Runtime) buildProgramEnv(
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) ([]string, error) {
	var env []string
	if !spec.CleanEnv {
		env = os.Environ()
	} else if !envMapHasKey(spec.Env, envPathKey) {
		env = append(env, envPathKey+"="+cleanEnvPath())
	}
	if _, err := codeexecutor.EnsureLayout(ws.Path); err != nil {
		return nil, err
	}
	runDir := filepath.Join(
		ws.Path,
		codeexecutor.DirRuns,
		"run_"+time.Now().Format("20060102T150405.000"),
	)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, err
	}

	baseEnv := map[string]string{
		codeexecutor.WorkspaceEnvDirKey: ws.Path,
		codeexecutor.EnvSkillsDir: filepath.Join(
			ws.Path,
			codeexecutor.DirSkills,
		),
		codeexecutor.EnvWorkDir: filepath.Join(
			ws.Path,
			codeexecutor.DirWork,
		),
		codeexecutor.EnvOutputDir: filepath.Join(
			ws.Path,
			codeexecutor.DirOut,
		),
		codeexecutor.EnvRunDir: runDir,
	}
	for k, v := range baseEnv {
		if _, ok := spec.Env[k]; ok {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range spec.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env, nil
}

func envMapHasKey(env map[string]string, key string) bool {
	for k := range env {
		if envKeyEqual(k, key) {
			return true
		}
	}
	return false
}

func cleanEnvPath() string {
	if runtime.GOOS == "windows" {
		return strings.Join([]string{
			`C:\Windows\System32`,
			`C:\Windows`,
			`C:\Windows\System32\WindowsPowerShell\v1.0`,
		}, string(os.PathListSeparator))
	}
	return strings.Join([]string{
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}, string(os.PathListSeparator))
}

func newLocalProgramCommand(
	ctx context.Context,
	cwd string,
	spec codeexecutor.RunProgramSpec,
	env []string,
) *exec.Cmd {
	cmd := exec.CommandContext(ctx, spec.Cmd, spec.Args...) //nolint:gosec
	cmd.Dir = cwd
	cmd.Env = env
	if !isBareLocalCommand(spec.Cmd) {
		return cmd
	}
	if resolved, ok := localProgramCommandPath(cwd, spec.Cmd, env); ok {
		cmd.Path = resolved
		cmd.Err = nil
	} else {
		cmd.Path = spec.Cmd
		cmd.Err = exec.ErrNotFound
	}
	return cmd
}

func localProgramCommandPath(
	cwd string,
	name string,
	env []string,
) (string, bool) {
	if !isBareLocalCommand(name) {
		return name, true
	}
	pathValue, _ := envValue(env, envPathKey)
	pathExt, _ := envValue(env, envPathExtKey)
	for _, dir := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(dir) == "" {
			dir = "."
		}
		for _, candidateName := range localProgramCandidateNames(
			name,
			pathExt,
		) {
			candidate := filepath.Join(dir, candidateName)
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(cwd, candidate)
			}
			if isLocalExecutableFile(candidate) {
				return candidate, true
			}
		}
	}
	return name, false
}

func envValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(env[i], "=")
		if !ok || !envKeyEqual(name, key) {
			continue
		}
		return value, true
	}
	return "", false
}

func envKeyEqual(a string, b string) bool {
	return envKeyEqualForGOOS(a, b, runtime.GOOS)
}

func envKeyEqualForGOOS(a string, b string, goos string) bool {
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func localProgramCandidateNames(name string, pathExt string) []string {
	return localProgramCandidateNamesForGOOS(
		name,
		pathExt,
		runtime.GOOS,
		string(os.PathListSeparator),
	)
}

func localProgramCandidateNamesForGOOS(
	name string,
	pathExt string,
	goos string,
	pathListSep string,
) []string {
	if goos != "windows" || filepath.Ext(name) != "" {
		return []string{name}
	}
	exts := strings.Split(pathExt, pathListSep)
	if len(exts) == 0 || strings.TrimSpace(pathExt) == "" {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	names := make([]string, 0, len(exts))
	for _, ext := range exts {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		names = append(names, name+ext)
	}
	return names
}

func isBareLocalCommand(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name == filepath.Base(name)
}

func isLocalExecutableFile(name string) bool {
	return isLocalExecutableFileForGOOS(name, runtime.GOOS)
}

func isLocalExecutableFileForGOOS(name string, goos string) bool {
	info, err := os.Stat(name)
	if err != nil || info.IsDir() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func formatInteractiveCommand(cmd string, args []string) string {
	if len(args) == 0 {
		return cmd
	}
	return cmd + " " + strings.Join(args, " ")
}

func interactiveExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func startPipes(
	cmd *exec.Cmd,
) (io.WriteCloser, io.ReadCloser, io.ReadCloser, func(), error) {
	if cmd.Stdout != nil {
		return nil, nil, nil, nil, errors.New("exec: Stdout already set")
	}
	if cmd.Stderr != nil {
		return nil, nil, nil, nil, errors.New("exec: Stderr already set")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, err
	}
	cmd.Stdout = outW
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, nil, nil, err
	}
	cmd.Stderr = errW

	var closeOnce sync.Once
	closeWritePipes := func() {
		closeOnce.Do(func() {
			_ = outW.Close()
			_ = errW.Close()
		})
	}
	return stdin, outR, errR, closeWritePipes, nil
}
