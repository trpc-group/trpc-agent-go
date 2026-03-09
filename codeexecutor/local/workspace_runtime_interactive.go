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
	defaultInteractiveIODrain  = 1 * time.Second
	defaultInteractiveKillWait = 2 * time.Second
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
		return cmd.Process.Kill()
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

	cmd := exec.CommandContext(tctx, spec.Cmd, spec.Args...) //nolint:gosec
	cmd.Dir = cwd

	env, err := r.buildProgramEnv(ws, spec.RunProgramSpec)
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Env = env

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
		stdin, stdout, stderr, err := startPipes(cmd)
		if err != nil {
			cancel()
			return nil, err
		}
		sess.stdin = stdin
		sess.closeIO = func() error {
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
		sess.ioWG.Wait()
		close(sess.ioDone)
	}()

	go func() {
		err := cmd.Wait()
		waitInteractiveIODone(sess.ioDone, defaultInteractiveIODrain)
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
	env := os.Environ()
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

func waitInteractiveIODone(
	done <-chan struct{},
	timeout time.Duration,
) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func startPipes(
	cmd *exec.Cmd,
) (io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	return stdin, stdout, stderr, nil
}
