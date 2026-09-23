//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseIgnoreLine(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		line string
		want ignoreRule
		ok   bool
	}{
		{name: "blank", line: "   "},
		{name: "comment", line: "# build output"},
		{name: "only slash", line: "/"},
		{
			name: "literal name at any depth",
			line: "node_modules",
			want: ignoreRule{literalName: "node_modules"},
			ok:   true,
		},
		{
			name: "directory only",
			line: "build/",
			want: ignoreRule{literalName: "build", dirOnly: true},
			ok:   true,
		},
		{
			name: "glob at any depth",
			line: "*.log",
			want: ignoreRule{pattern: "**/*.log"},
			ok:   true,
		},
		{
			name: "anchored by leading slash",
			line: "/dist",
			want: ignoreRule{pattern: "dist"},
			ok:   true,
		},
		{
			name: "anchored by inner slash",
			line: "docs/*.tmp",
			want: ignoreRule{pattern: "docs/*.tmp"},
			ok:   true,
		},
		{
			name: "negation",
			line: "!keep.log",
			want: ignoreRule{literalName: "keep.log", negate: true},
			ok:   true,
		},
		{
			name: "escaped bang is literal",
			line: `\!important`,
			want: ignoreRule{literalName: "!important"},
			ok:   true,
		},
		{
			name: "escaped hash is literal",
			line: `\#notes`,
			want: ignoreRule{literalName: "#notes"},
			ok:   true,
		},
		{
			name: "nested ignore file anchors under its directory",
			dir:  "sub",
			line: "/out",
			want: ignoreRule{pattern: "sub/out", dir: "sub"},
			ok:   true,
		},
		{
			name: "nested ignore file glob stays below its directory",
			dir:  "sub",
			line: "*.bak",
			want: ignoreRule{pattern: "sub/**/*.bak", dir: "sub"},
			ok:   true,
		},
		{
			name: "trailing spaces trimmed",
			line: "cache   ",
			want: ignoreRule{literalName: "cache"},
			ok:   true,
		},
		{name: "invalid glob dropped", line: "[unclosed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseIgnoreLine(tt.dir, tt.line)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestIgnoreRules_Ignored(t *testing.T) {
	rules := ignoreRules{
		"": parseIgnoreRules("", []byte(
			"node_modules/\n*.log\n!keep.log\n/dist\nbuild\n",
		)),
		"sub": parseIgnoreRules("sub", []byte("/out\n*.bak\n")),
	}
	tests := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{rel: "node_modules", isDir: true, want: true},
		{rel: "a/b/node_modules", isDir: true, want: true},
		{rel: "node_modules", isDir: false, want: false},
		{rel: "x.log", want: true},
		{rel: "a/x.log", want: true},
		{rel: "keep.log", want: false},
		{rel: "a/keep.log", want: false},
		{rel: "dist", isDir: true, want: true},
		{rel: "a/dist", isDir: true, want: false},
		{rel: "build", want: true},
		{rel: "a/build", isDir: true, want: true},
		{rel: "sub/out", isDir: true, want: true},
		{rel: "other/out", isDir: true, want: false},
		{rel: "sub/deep/x.bak", want: true},
		{rel: "other/x.bak", want: false},
		{rel: "main.go", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.rel, func(t *testing.T) {
			assert.Equal(t, tt.want, rules.ignored(tt.rel, tt.isDir))
		})
	}
	assert.False(t, ignoreRules{}.ignored("anything", false))
}
