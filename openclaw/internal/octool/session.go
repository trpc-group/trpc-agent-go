//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package octool

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type session struct {
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

	lineBase   int
	lines      []string
	partial    string
	pollCursor int
	maxLines   int
}

func newSession(id, command string, maxLines int) *session {
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

func (s *session) readFrom(r io.Reader) {
	if r == nil {
		return
	}
	rd := bufio.NewReaderSize(r, 32*1024)
	for {
		b, err := rd.ReadBytes('\n')
		if len(b) > 0 {
			s.appendOutput(string(b))
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

type processSession struct {
	SessionID string `json:"sessionId"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt"`
	DoneAt    string `json:"doneAt,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
}

func (s *session) snapshot() processSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := processSession{
		SessionID: s.id,
		Command:   s.command,
		StartedAt: s.started.Format(time.RFC3339),
	}
	if s.finished.IsZero() {
		out.Status = "running"
		return out
	}
	out.Status = "exited"
	out.DoneAt = s.finished.Format(time.RFC3339)
	code := s.exitCode
	out.ExitCode = &code
	return out
}

type processPoll struct {
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"nextOffset"`
	ExitCode   *int   `json:"exitCode,omitempty"`
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
	s.pollCursor = end

	res := processPoll{
		Output:     out,
		Offset:     start,
		NextOffset: end,
	}
	if s.finished.IsZero() {
		res.Status = "running"
		return res
	}
	res.Status = "exited"
	code := s.exitCode
	res.ExitCode = &code
	return res
}

type processLog struct {
	Output     string `json:"output,omitempty"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"nextOffset"`
}

func (s *session) log(offset *int, limit *int) processLog {
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

	if offset == nil && limit == nil {
		if end-start > defaultLogLimit {
			start = end - defaultLogLimit
		}
	} else if limit != nil && *limit > 0 {
		if want := start + *limit; want < end {
			end = want
		}
	}

	from := start - s.lineBase
	to := end - s.lineBase
	out := strings.Join(s.lines[from:to], "\n")

	return processLog{
		Output:     out,
		Offset:     start,
		NextOffset: end,
	}
}

type processWrite struct {
	OK bool `json:"ok"`
}

func (s *session) write(data string, newline bool) (processWrite, error) {
	if data == "" && !newline {
		return processWrite{OK: true}, nil
	}

	s.mu.Lock()
	stdin := s.stdin
	running := s.finished.IsZero()
	s.mu.Unlock()

	if !running {
		return processWrite{}, errors.New("session is not running")
	}
	if stdin == nil {
		return processWrite{}, errors.New("stdin is not available")
	}

	text := data
	if newline {
		text += "\n"
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		return processWrite{}, err
	}
	return processWrite{OK: true}, nil
}

func (s *session) kill(grace time.Duration) error {
	s.mu.Lock()
	cmd := s.cmd
	cancel := s.cancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if runtime.GOOS != "windows" {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-s.doneCh:
		return nil
	case <-time.After(grace):
		return cmd.Process.Kill()
	}
}

func sortSessions(sessions []processSession) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].SessionID < sessions[j].SessionID
	})
}
