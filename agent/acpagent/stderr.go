//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	defaultStderrTailBytes    = 8 << 10
	maxPendingStderrLineBytes = 16 << 10
)

type stderrLineSink interface {
	writeStderrLine(name string, line string)
}

type platformStderrWriter struct{}

func (platformStderrWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line != "" {
		log.Infof("acpagent stderr: %s", line)
	}
	return len(p), nil
}

func (platformStderrWriter) writeStderrLine(name string, line string) {
	log.Infof("acpagent: %s stderr: %s", name, line)
}

type processStderr struct {
	name        string
	destination io.Writer
	lineSink    stderrLineSink

	mu      sync.Mutex
	pending []byte
	tail    stderrTail
}

func newProcessStderr(
	name string,
	destination io.Writer,
) *processStderr {
	lineSink, _ := destination.(stderrLineSink)
	return &processStderr{
		name:        name,
		destination: destination,
		lineSink:    lineSink,
		tail:        stderrTail{max: defaultStderrTailBytes},
	}
}

func (s *processStderr) Write(p []byte) (int, error) {
	if s == nil {
		return len(p), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tail.write(p)
	if s.lineSink != nil {
		s.writeLogLinesLocked(p)
		return len(p), nil
	}
	if s.destination == nil {
		return len(p), nil
	}

	n, err := s.destination.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (s *processStderr) Flush() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return
	}
	s.logLineLocked(s.lineSink, string(s.pending))
	s.pending = nil
}

func (s *processStderr) Tail() string {
	if s == nil {
		return ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.tail.buf))
}

func (s *processStderr) writeLogLinesLocked(p []byte) {
	s.pending = append(s.pending, p...)
	for {
		index := bytes.IndexByte(s.pending, '\n')
		if index < 0 {
			break
		}
		s.logLineLocked(s.lineSink, string(s.pending[:index]))
		s.pending = s.pending[index+1:]
	}
	for len(s.pending) > maxPendingStderrLineBytes {
		line := string(s.pending[:maxPendingStderrLineBytes])
		s.pending = append([]byte(nil), s.pending[maxPendingStderrLineBytes:]...)
		s.logLineLocked(s.lineSink, line+" [line truncated]")
	}
}

func (s *processStderr) logLineLocked(sink stderrLineSink, line string) {
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return
	}
	sink.writeStderrLine(s.name, line)
}

type stderrTail struct {
	max int
	buf []byte
}

func (t *stderrTail) write(p []byte) {
	if t.max <= 0 || len(p) == 0 {
		return
	}
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return
	}

	overflow := len(t.buf) + len(p) - t.max
	if overflow > 0 {
		copy(t.buf, t.buf[overflow:])
		t.buf = t.buf[:len(t.buf)-overflow]
	}
	t.buf = append(t.buf, p...)
}
