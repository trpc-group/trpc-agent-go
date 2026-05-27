// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package shellsafe

import (
	"strings"
	"testing"
)

// TestSimple_AcceptsCommonShapes covers the "happy path" shapes the
// v1 lexer is designed to accept: bare words, single- and
// double-quoted strings, backslash escapes, UTF-8 in barewords,
// concatenated word parts, and tab as a word separator.
func TestSimple_AcceptsCommonShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"bare cmd", "curl https://x.com", []string{"curl", "https://x.com"}},
		{
			name: "squoted URL with reserved bytes",
			in:   "curl 'https://x.com/p?q=1&r=2'",
			want: []string{"curl", "https://x.com/p?q=1&r=2"},
		},
		{
			name: "dquoted URL",
			in:   `curl "https://x.com/p"`,
			want: []string{"curl", "https://x.com/p"},
		},
		{"backslash escape", `echo a\ b`, []string{"echo", "a b"}},
		{"squote keeps dollar", `echo 'a$b'`, []string{"echo", "a$b"}},
		{"squote keeps backtick", "echo 'a`b'", []string{"echo", "a`b"}},
		{"empty squote", `cmd ''`, []string{"cmd", ""}},
		{"empty dquote", `cmd ""`, []string{"cmd", ""}},
		{
			name: "dquote with escaped quote",
			in:   `echo "a\"b"`,
			want: []string{"echo", `a"b`},
		},
		{"utf8 in bareword", "echo 你好", []string{"echo", "你好"}},
		{"tab as separator", "echo\ta", []string{"echo", "a"}},
		{
			name: "concatenated bare + squote + dquote",
			in:   `echo abc'def'"ghi"`,
			want: []string{"echo", "abcdefghi"},
		},
		{
			name: "leading and trailing whitespace",
			in:   "   echo  hi   ",
			want: []string{"echo", "hi"},
		},
		{
			// POSIX double-quote rule: backslash is only special
			// before $, `, ", \, and newline. Before any other
			// byte the backslash is preserved literally, so
			// "a\nb" is the three-byte sequence a-\-n-b. The
			// stricter, POSIX-accurate behaviour is covered by
			// TestParse_DoubleQuotedBackslashPosix in parser_test.go.
			name: "double-quoted backslash before non-special preserved",
			in:   `echo "a\nb"`,
			want: []string{"echo", `a\nb`},
		},
		{
			name: "xargs literal brace placeholder",
			in:   "xargs -I{} curl",
			want: []string{"xargs", "-I{}", "curl"},
		},
		{
			name: "find exec literal brace placeholder",
			in:   "find . -exec cat {} +",
			want: []string{"find", ".", "-exec", "cat", "{}", "+"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equal(pipe.Commands[0], tc.want) {
				t.Fatalf("argv: got %v want %v",
					pipe.Commands[0], tc.want)
			}
		})
	}
}

// TestSimple_AcceptsPipelinesAndSequencing covers the four safe
// operators between simple commands, including the no-whitespace
// shape that some models emit ("a|b", "a&&b").
func TestSimple_AcceptsPipelinesAndSequencing(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [][]string
	}{
		{
			name: "pipe with spaces",
			in:   "echo a | wc -l",
			want: [][]string{{"echo", "a"}, {"wc", "-l"}},
		},
		{
			name: "pipe without spaces",
			in:   "echo a|wc -l",
			want: [][]string{{"echo", "a"}, {"wc", "-l"}},
		},
		{
			name: "and without spaces",
			in:   "echo a&&echo b",
			want: [][]string{{"echo", "a"}, {"echo", "b"}},
		},
		{
			name: "all four operators mixed",
			in:   "a | b && c || d ; e",
			want: [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(pipe.Commands) != len(tc.want) {
				t.Fatalf("segments: got %v want %v",
					pipe.Commands, tc.want)
			}
			for i := range tc.want {
				if !equal(pipe.Commands[i], tc.want[i]) {
					t.Fatalf("segment %d: got %v want %v",
						i, pipe.Commands[i], tc.want[i])
				}
			}
		})
	}
}

// TestSimple_RejectsBypassAndUnsafeShapes is the v1 lexer's own
// rejection corpus. It complements (and overlaps slightly with)
// TestParse_RejectsExpansionsAndUnsafeConstructs in parser_test.go;
// the goal here is to exercise lexer-level shapes that are harder
// to reach from the public-API tests, especially around quoting,
// escapes and operator placement.
func TestSimple_RejectsBypassAndUnsafeShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare newline", "echo a\necho b", "newline"},
		{"escaped newline", "echo a\\\nb", "escaped newline"},
		{"unterminated squote", "echo 'abc", "unterminated single"},
		{"unterminated dquote", `echo "abc`, "unterminated double"},
		{"trailing backslash bare", `echo abc\`, "trailing backslash"},
		{
			name: "trailing backslash in dquote",
			in:   `echo "abc\`,
			want: "trailing backslash",
		},
		{"dollar inside dquote", `echo "a$b"`, "double-quoted"},
		{
			name: "backtick inside dquote",
			in:   "echo \"a`b\"",
			want: "double-quoted",
		},
		{"newline inside dquote", "echo \"a\nb\"", "newline"},
		{"trailing pipe", "echo a |", "empty right"},
		{"trailing and", "echo a &&", "empty right"},
		{"leading pipe", "| echo a", "empty left"},
		{"leading semi", "; echo a", "empty left"},
		{"double semi", "a ;; b", "case statement"},
		{"pipe and stderr", "a |& b", "stderr"},
		{"glob star", "ls *.txt", "glob"},
		{"glob question", "ls ?.txt", "glob"},
		{"square bracket", "ls [a].txt", "test expression"},
		{"hash comment", "echo a # comment", "comment"},
		{"history bang mid", "echo !1", "history expansion"},
		{"raw ampersand", "cmd &", "background"},
		{"leading assignment", "FOO=bar curl http://x", "leading variable"},
		{
			name: "brace expansion comma",
			in:   "echo {a,b}",
			want: "brace expansion",
		},
		{
			name: "brace expansion range",
			in:   "echo {1..3}",
			want: "brace expansion",
		},
		{
			name: "unmatched open brace",
			in:   "echo {abc",
			want: "unmatched",
		},
		{
			name: "standalone open brace at start (block)",
			in:   "{ echo a; }",
			want: "block",
		},
		{
			name: "control byte rejected",
			in:   "echo \x01",
			want: "control character",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.in)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf(
					"error %q should contain %q",
					err.Error(), tc.want,
				)
			}
		})
	}
}

// TestSimple_DosCaps covers the two hard limits the v1 lexer
// enforces so a pathological model output cannot run unbounded.
func TestSimple_DosCaps(t *testing.T) {
	t.Run("max command length", func(t *testing.T) {
		long := strings.Repeat("a", maxCommandLen+1)
		_, err := Parse(long)
		if err == nil || !strings.Contains(err.Error(), "too long") {
			t.Fatalf("expected length cap, got: %v", err)
		}
	})
	t.Run("max segments", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i <= maxSegments+1; i++ {
			if i > 0 {
				sb.WriteString(" | ")
			}
			sb.WriteString("cmd")
		}
		_, err := Parse(sb.String())
		if err == nil ||
			!strings.Contains(err.Error(), "too many pipeline") {
			t.Fatalf("expected segment cap, got: %v", err)
		}
	})
}

// TestSimple_LeadingAssignmentDetector pins down the heuristic that
// decides whether argv[0] should be rejected as "FOO=bar". A pure
// command name that happens to contain '=' later (like "a=b" passed
// as a literal command name) is not really plausible, but the
// detector should at least not crash on those shapes.
func TestSimple_LeadingAssignmentDetector(t *testing.T) {
	cases := []struct {
		word string
		want bool
	}{
		{"FOO=bar", true},
		{"_underscore=1", true},
		{"a=", true},
		{"1FOO=bar", false}, // identifier may not start with digit
		{"=bar", false},     // identifier may not be empty
		{"FOO-bar=1", false},
		{"FOO", false},
		{"", false},
		{"--flag=value", false}, // CLI flag form, not assignment
	}
	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			got := isLeadingAssignment(tc.word)
			if got != tc.want {
				t.Fatalf("isLeadingAssignment(%q): got %v want %v",
					tc.word, got, tc.want)
			}
		})
	}
}
