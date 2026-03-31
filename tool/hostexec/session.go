//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hostexec

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	programStatusRunning = "running"
	programStatusExited  = "exited"
)

type session struct {
	id      string
	command string

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	closeIO func() error
	cancel  context.CancelFunc

	processGroupID int

	doneCh chan struct{}
	ioDone chan struct{}
	ioWG   sync.WaitGroup

	mu       sync.Mutex
	started  time.Time
	finished time.Time
	exitCode int

	lineBase   int
	lines      []string
	partial    string
	pollCursor int
	maxLines   int
	closeOnce  sync.Once
}

func newSession(id string, command string, maxLines int) *session {
	return &session{
		id:       id,
		command:  command,
		doneCh:   make(chan struct{}),
		ioDone:   make(chan struct{}),
		started:  time.Now(),
		maxLines: maxLines,
	}
}

func newSessionID() string {
	return uuid.NewString()
}

func (s *session) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished.IsZero()
}

func (s *session) doneAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

func (s *session) markDone(exitCode int) {
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
	s.finished = time.Now()
	close(s.doneCh)
}

func (s *session) readFrom(reader io.Reader) {
	if reader == nil {
		return
	}

	bufReader := bufio.NewReaderSize(reader, 32*1024)
	for {
		chunk, err := bufReader.ReadBytes('\n')
		if len(chunk) > 0 {
			s.appendOutput(string(chunk))
		}
		if err != nil {
			return
		}
	}
}

func (s *session) appendOutput(chunk string) {
	text := strings.ReplaceAll(chunk, "\r\n", "\n")

	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *session) trimLocked() {
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

func (s *session) tail(lines int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lines <= 0 {
		return ""
	}
	start := 0
	if len(s.lines) > lines {
		start = len(s.lines) - lines
	}
	out := strings.Join(s.lines[start:], "\n")
	if s.partial != "" {
		if out != "" {
			out += "\n"
		}
		out += s.partial
	}
	return out
}

func trimOutputTail(output string, lines int) string {
	if lines <= 0 || output == "" {
		return ""
	}
	parts := strings.Split(output, "\n")
	if len(parts) <= lines {
		return output
	}
	return strings.Join(parts[len(parts)-lines:], "\n")
}

func (s *session) pollTail(lines int) string {
	poll := s.poll(nil)
	return trimOutputTail(poll.Output, lines)
}

func (s *session) allOutput() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := strings.Join(s.lines, "\n")
	if s.partial != "" {
		if out != "" {
			out += "\n"
		}
		out += s.partial
	}
	return out, s.exitCode
}

type processPoll struct {
	Status     string
	Output     string
	Offset     int
	NextOffset int
	ExitCode   *int
}

func (s *session) poll(limit *int) processPoll {
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

	res := processPoll{
		Status:     programStatusRunning,
		Output:     out,
		Offset:     start,
		NextOffset: end,
	}
	if s.finished.IsZero() {
		return res
	}
	res.Status = programStatusExited
	res.ExitCode = intPtr(s.exitCode)
	return res
}

func (s *session) write(data string, newline bool) error {
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

func killProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil &&
		!errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (s *session) kill(
	ctx context.Context,
	grace time.Duration,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	cmd := s.cmd
	cancel := s.cancel
	processGroupID := s.processGroupID
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		if cancel != nil {
			cancel()
		}
		return nil
	}

	err := terminateProcessTree(
		ctx,
		cmd.Process,
		processGroupID,
		grace,
	)
	if cancel != nil {
		cancel()
	}
	return err
}

func (s *session) close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.closeIO != nil {
			err = s.closeIO()
		}
	})
	return err
}
