//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewParser(t *testing.T) {
	p := newParser()
	assert.NotNil(t, p)
}

func TestParserAppendAndFlush(t *testing.T) {
	p := newParser()
	lines := p.append(`{"step":1}` + "\n" + `{"step":2}` + "\n" + `{"step":3}`)
	assert.Equal(t, []string{`{"step":1}`, `{"step":2}`}, lines)
	lines = p.flush()
	assert.Equal(t, []string{`{"step":3}`}, lines)
}

func TestParserReset(t *testing.T) {
	p := newParser()
	lines := p.append(`{"step":1}` + "\n")
	assert.Equal(t, []string{`{"step":1}`}, lines)
	p.reset()
	lines = p.flush()
	assert.Empty(t, lines)
	lines = p.append(`{"step":2}` + "\n")
	assert.Equal(t, []string{`{"step":2}`}, lines)
	lines = p.flush()
	assert.Empty(t, lines)
}

func TestParserTrimWhitespaceAndEmptyLines(t *testing.T) {
	p := newParser()
	lines := p.append(`  ` + "\n" + `{"step":1}` + "\n" + " \t\n" + `{"step":2}` + "\n ")
	assert.Equal(t, []string{`{"step":1}`, `{"step":2}`}, lines)
}
