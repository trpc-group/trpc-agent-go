// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2ui

import "strings"

// parser splits streaming text into JSONL records.
//
// Each non-empty line is treated as one JSONL message.
// Lines may arrive in fragments across multiple Append calls.
// Trailing "\r" from CRLF line endings is removed.
// Blank lines are ignored.
type parser struct {
	pending string
}

// newParser creates a new JSONL parser.
func newParser() *parser {
	return &parser{}
}

// append appends streaming text and returns all completed JSONL lines.
//
// Incomplete trailing data is buffered until more text arrives or flush is called.
func (p *parser) append(text string) []string {
	if text == "" {
		return nil
	}
	data := p.pending + text
	p.pending = ""
	return p.consume(data, false)
}

// flush returns the final buffered line, if any, and resets the pending state.
//
// Blank final content is ignored.
func (p *parser) flush() []string {
	if p.pending == "" {
		return nil
	}
	data := p.pending
	p.pending = ""
	return p.consume(data, true)
}

// reset clears all buffered state.
func (p *parser) reset() {
	p.pending = ""
}

func (p *parser) consume(data string, flush bool) []string {
	var out []string
	start := 0
	for {
		i := strings.IndexByte(data[start:], '\n')
		if i < 0 {
			break
		}
		end := start + i
		if line := normalizeLine(data[start:end]); line != "" {
			out = append(out, strings.Clone(line))
		}
		start = end + 1
	}
	rest := data[start:]
	if flush {
		if line := normalizeLine(rest); line != "" {
			out = append(out, strings.Clone(line))
		}
		return out
	}
	p.pending = rest
	return out
}

func normalizeLine(s string) string {
	s = strings.TrimSuffix(s, "\r")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return s
}
