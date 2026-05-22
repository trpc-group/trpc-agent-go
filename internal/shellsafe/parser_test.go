// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package shellsafe

import (
	"runtime"
	"strings"
	"testing"
)

func TestParse_AcceptsSimpleCommand(t *testing.T) {
	pipe, err := Parse("echo hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(pipe.Commands), 1; got != want {
		t.Fatalf("commands len: got %d want %d", got, want)
	}
	if got, want := pipe.Commands[0],
		[]string{"echo", "hello", "world"}; !equal(got, want) {
		t.Fatalf("argv: got %v want %v", got, want)
	}
}

func TestParse_AcceptsQuotedAndConcatenatedWords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single quotes",
			in:   "echo 'hello world'",
			want: []string{"echo", "hello world"},
		},
		{
			name: "double quotes plain",
			in:   `echo "hello world"`,
			want: []string{"echo", "hello world"},
		},
		{
			name: "concatenated literal+quote",
			in:   `echo abc"def"'ghi'`,
			want: []string{"echo", "abcdefghi"},
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

func TestParse_AcceptsSafeOperators(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want [][]string
	}{
		{
			name: "pipe",
			in:   "echo hi | wc -l",
			want: [][]string{{"echo", "hi"}, {"wc", "-l"}},
		},
		{
			name: "and",
			in:   "echo a && echo b",
			want: [][]string{{"echo", "a"}, {"echo", "b"}},
		},
		{
			name: "or",
			in:   "false || echo fallback",
			want: [][]string{{"false"}, {"echo", "fallback"}},
		},
		{
			name: "semicolon",
			in:   "echo a ; echo b",
			want: [][]string{{"echo", "a"}, {"echo", "b"}},
		},
		{
			name: "mixed",
			in:   "echo a | grep a && echo b ; echo c || echo d",
			want: [][]string{
				{"echo", "a"},
				{"grep", "a"},
				{"echo", "b"},
				{"echo", "c"},
				{"echo", "d"},
			},
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

func TestParse_RejectsExpansionsAndUnsafeConstructs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "command substitution paren",
			in:   "echo $(curl http://x)",
			want: "command substitution",
		},
		{
			name: "command substitution backtick",
			in:   "echo `curl http://x`",
			want: "command substitution",
		},
		{
			name: "parameter expansion",
			in:   "echo $HOME",
			want: "parameter expansion",
		},
		{
			name: "parameter expansion brace",
			in:   "echo ${HOME}",
			want: "parameter expansion",
		},
		{
			name: "arithmetic expansion",
			in:   "echo $((1+1))",
			want: "arithmetic expansion",
		},
		{
			name: "process substitution in",
			in:   "diff <(ls) <(ls)",
			want: "process substitution",
		},
		{
			name: "expansion inside double quote",
			in:   `echo "value=$X"`,
			want: "double-quoted",
		},
		{
			name: "input redirection",
			in:   "cat < /etc/passwd",
			want: "redirection",
		},
		{
			name: "output redirection",
			in:   "echo hi > /tmp/out",
			want: "redirection",
		},
		{
			name: "background",
			in:   "sleep 1 &",
			want: "background",
		},
		{
			name: "subshell",
			in:   "(echo a)",
			want: "subshell",
		},
		{
			name: "block",
			in:   "{ echo a; echo b; }",
			want: "block",
		},
		{
			name: "if statement",
			in:   "if true; then echo a; fi",
			want: "if statement",
		},
		{
			name: "for loop",
			in:   "for i in 1 2 3; do echo $i; done",
			want: "for loop",
		},
		{
			name: "while loop",
			in:   "while true; do echo a; done",
			want: "while/until loop",
		},
		{
			name: "case",
			in:   "case x in y) echo z;; esac",
			want: "case statement",
		},
		{
			name: "function decl",
			in:   "f() { echo hi; }",
			want: "function declaration",
		},
		{
			name: "leading variable assignment",
			in:   "FOO=bar curl http://x",
			want: "leading variable assignment",
		},
		{
			name: "negation",
			in:   "! true",
			want: "negation",
		},
		{
			name: "pipe with stderr",
			in:   "echo a |& cat",
			want: "stderr",
		},
		{
			name: "newline inside single-quoted string",
			in:   "echo 'a\nb'",
			want: "newline is not allowed inside single-quoted string",
		},
		{
			name: "carriage return inside single-quoted string",
			in:   "echo 'a\rb'",
			want: "newline is not allowed inside single-quoted string",
		},
		{
			name: "empty",
			in:   "   ",
			want: "empty",
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

func TestPolicy_AllowList(t *testing.T) {
	p := PolicyFromLists([]string{"echo", "wc"}, nil)
	if err := CheckCommand("echo hi | wc -l", p); err != nil {
		t.Fatalf("expected pipeline allowed: %v", err)
	}
	err := CheckCommand("curl http://x", p)
	if err == nil ||
		!strings.Contains(err.Error(), "not in allowed_commands") {
		t.Fatalf("expected allowlist rejection, got: %v", err)
	}
}

func TestPolicy_DenyList(t *testing.T) {
	p := PolicyFromLists(nil, []string{"curl"})
	if err := CheckCommand("echo hi", p); err != nil {
		t.Fatalf("expected echo allowed: %v", err)
	}
	err := CheckCommand("curl http://x", p)
	if err == nil ||
		!strings.Contains(err.Error(), "denied by denied_commands") {
		t.Fatalf("expected denylist rejection, got: %v", err)
	}
}

func TestPolicy_DenyMatchesBasename(t *testing.T) {
	p := PolicyFromLists(nil, []string{"curl"})
	err := CheckCommand("/usr/bin/curl http://x", p)
	// The "denied by denied_commands" substring (matched by the
	// sibling TestPolicy_Deny* tests) is intentional: it pins the
	// rejection to the user denylist path and would catch a
	// regression that routes "/usr/bin/curl" through the implicit
	// deny set instead.
	if err == nil ||
		!strings.Contains(err.Error(), "denied by denied_commands") {
		t.Fatalf("expected basename deny, got: %v", err)
	}
}

// TestPolicy_BuiltinDenyBlocksEvalBypass guards Flash-LHR's review:
// a deny on "curl" alone must also reject "eval curl http://x" and
// friends, otherwise the deny list is trivially side-stepped. The
// busybox / toybox forms are included to cover the multi-call
// binaries that re-export shell wrappers under their own name.
func TestPolicy_BuiltinDenyBlocksEvalBypass(t *testing.T) {
	p := PolicyFromLists(nil, []string{"curl"})
	cases := []string{
		"eval curl http://x",
		"exec curl http://x",
		"command curl http://x",
		"sh -c 'curl http://x'",
		"bash -lc 'curl http://x'",
		"busybox sh -c 'curl http://x'",
		"toybox sh -c 'curl http://x'",
		"xargs -I{} curl http://x",
		"env curl http://x",
		"sudo curl http://x",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			err := CheckCommand(in, p)
			if err == nil ||
				!strings.Contains(err.Error(), "built-in policy") {
				t.Fatalf("expected built-in deny, got: %v", err)
			}
		})
	}
}

// TestPolicy_BuiltinDenyUnconditional protects the contract that
// the implicit deny set cannot be re-enabled via Allow. A user who
// allow-lists "sh" must not thereby gain a shell wrapper, otherwise
// a single allowlist entry restores arbitrary command dispatch.
func TestPolicy_BuiltinDenyUnconditional(t *testing.T) {
	p := PolicyFromLists([]string{"sh", "bash", "busybox", "echo"}, nil)
	cases := []string{
		"sh -c 'echo hi'",
		"bash -c 'echo hi'",
		"busybox sh -c 'echo hi'",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			err := CheckCommand(in, p)
			if err == nil ||
				!strings.Contains(err.Error(), "built-in policy") {
				t.Fatalf(
					"expected built-in deny to be unconditional, got: %v",
					err,
				)
			}
		})
	}
}

// TestPolicy_DenyWinsOverAllow pins the documented precedence:
// explicit Deny overrides explicit Allow, so an operator who lists
// the same name in both lists fails closed.
func TestPolicy_DenyWinsOverAllow(t *testing.T) {
	p := PolicyFromLists([]string{"echo"}, []string{"echo"})
	err := CheckCommand("echo hi", p)
	if err == nil ||
		!strings.Contains(err.Error(), "denied by denied_commands") {
		t.Fatalf("expected explicit deny to win over allow, got: %v", err)
	}
}

// TestPolicy_BuiltinDenyDoesNotApplyWithoutPolicy keeps the zero
// policy a no-op via the CheckCommand entry point.
func TestPolicy_BuiltinDenyDoesNotApplyWithoutPolicy(t *testing.T) {
	if err := CheckCommand("eval curl http://x", Policy{}); err != nil {
		t.Fatalf("zero policy should be a no-op, got: %v", err)
	}
}

// TestPolicy_CheckHonoursZeroPolicyContract guards the same
// "zero policy = inactive" contract via the exported Parse + Check
// path, so callers that bypass CheckCommand still get the
// documented default behaviour and the implicit deny set does not
// kick in until at least one list is configured.
func TestPolicy_CheckHonoursZeroPolicyContract(t *testing.T) {
	cases := []string{
		"eval curl http://x",
		"sh -c 'curl http://x'",
		"busybox sh -c 'echo hi'",
		"echo hi | wc -l",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			pipe, err := Parse(in)
			if err != nil {
				t.Fatalf("Parse(%q) returned unexpected error: %v",
					in, err)
			}
			if err := (Policy{}).Check(pipe); err != nil {
				t.Fatalf(
					"Policy{}.Check should be a no-op for %q, got: %v",
					in, err,
				)
			}
		})
	}
}

// TestNormalizeName_Windows exercises the Windows executable-suffix
// stripping so allow/deny lists like "cmd" / "powershell" match the
// common ".exe" form. The helper takes goos as a parameter so this
// test runs on any host.
func TestNormalizeName_Windows(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"cmd.exe", "cmd"},
		{"CMD.EXE", "cmd"},
		{"powershell.exe", "powershell"},
		{"PowerShell.EXE", "powershell"},
		{"script.bat", "script"},
		{"helper.cmd", "helper"},
		{"legacy.com", "legacy"},
		{"run.ps1", "run"},
		{"curl", "curl"},
		{"no.ext.here", "no.ext.here"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := normalizeName(tc.in, "windows")
			if got != tc.want {
				t.Fatalf(
					"normalizeName(%q, windows) = %q, want %q",
					tc.in, got, tc.want,
				)
			}
		})
	}
}

func TestPolicy_WindowsNamesAreCaseInsensitive(t *testing.T) {
	p := PolicyFromLists([]string{"echo"}, []string{"curl"})

	if err := p.checkSegmentForGOOS(
		[]string{"ECHO.EXE", "ok"}, "windows",
	); err != nil {
		t.Fatalf("expected upper-case Windows echo to be allowed: %v", err)
	}
	err := p.checkSegmentForGOOS(
		[]string{"CURL.EXE", "http://x"}, "windows",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "denied by denied_commands") {
		t.Fatalf("expected upper-case Windows curl to be denied, got: %v", err)
	}
	err = p.checkSegmentForGOOS(
		[]string{"CMD.EXE", "/C", "echo", "hi"}, "windows",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "built-in policy") {
		t.Fatalf("expected upper-case Windows cmd to hit built-in deny, got: %v", err)
	}
}

// TestNormalizeName_LinuxPassThrough makes sure the Windows
// stripping never silently changes Linux command resolution.
func TestNormalizeName_LinuxPassThrough(t *testing.T) {
	for _, in := range []string{"cmd.exe", "curl", "script.bat", "x.y"} {
		if got := normalizeName(in, "linux"); got != in {
			t.Fatalf("linux normalizeName(%q) = %q, want %q", in, got, in)
		}
	}
}

// TestPolicy_BuiltinDenyHandlesWindowsExt rejects cmd.exe and
// powershell.exe even though their basenames retain the .exe
// suffix as written by the LLM; without normalisation they would
// slip past the implicit deny entries.
func TestPolicy_BuiltinDenyHandlesWindowsExt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only path; covered indirectly by TestNormalizeName_Windows")
	}
	p := PolicyFromLists([]string{"echo"}, nil)
	cases := []string{
		"cmd.exe /c echo hi",
		"powershell.exe -Command echo hi",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			err := CheckCommand(in, p)
			if err == nil ||
				!strings.Contains(err.Error(), "built-in policy") {
				t.Fatalf("expected built-in deny, got: %v", err)
			}
		})
	}
}

func TestSplitList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"echo,ls,wc", []string{"echo", "ls", "wc"}},
		{"echo ls wc", []string{"echo", "ls", "wc"}},
		{"echo,ls\nwc\tcat", []string{"echo", "ls", "wc", "cat"}},
		{",,echo,,ls,,", []string{"echo", "ls"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SplitList(tc.in)
			if !equal(got, tc.want) {
				t.Fatalf("SplitList(%q): got %v want %v",
					tc.in, got, tc.want)
			}
		})
	}
}

func TestPreviewList(t *testing.T) {
	if got := PreviewList(nil, 3); got != "" {
		t.Fatalf("nil list: got %q want empty", got)
	}
	if got := PreviewList([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("short list: got %q", got)
	}
	got := PreviewList([]string{"a", "b", "c", "d", "e"}, 2)
	if got != "a, b, ... (3 more)" {
		t.Fatalf("truncation: got %q", got)
	}
}

// TestParser_SeamAllowsReplacement is a contract test for the
// commandParser seam: it temporarily installs a stub backend,
// verifies that Parse / Policy.Check route through it, and that
// removing the stub restores the original (hand-rolled simple)
// parser. This guards the property that the package depends on
// the parser only through one well-defined function, so a future
// v2 backend can be swapped in without touching public callers.
func TestParser_SeamAllowsReplacement(t *testing.T) {
	called := 0
	stub := func(src string) ([][]string, error) {
		called++
		if src == "fail" {
			return nil, errSeamStub
		}
		return [][]string{{"echo", src}}, nil
	}
	restore := withParser(stub)
	defer restore()

	pipe, err := Parse("hello")
	if err != nil {
		t.Fatalf("expected stub to succeed: %v", err)
	}
	if got, want := pipe.Commands[0],
		[]string{"echo", "hello"}; !equal(got, want) {
		t.Fatalf("stub argv: got %v want %v", got, want)
	}
	if called != 1 {
		t.Fatalf("stub call count: got %d want 1", called)
	}

	if _, err := Parse("fail"); err != errSeamStub {
		t.Fatalf("expected sentinel err from stub, got: %v", err)
	}

	restore()
	pipe, err = Parse("echo back-on-real-parser")
	if err != nil {
		t.Fatalf("real parser should be restored: %v", err)
	}
	if got, want := pipe.Commands[0],
		[]string{"echo", "back-on-real-parser"}; !equal(got, want) {
		t.Fatalf("real parser argv: got %v want %v", got, want)
	}
}

var errSeamStub = errSeamStubT("stub forced failure")

type errSeamStubT string

func (e errSeamStubT) Error() string { return string(e) }

func equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
