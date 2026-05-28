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
			name: "leading variable += assignment",
			in:   "X+=1 curl http://x",
			want: "leading variable assignment",
		},
		{
			name: "leading variable += plain name",
			in:   "PATH+=:./bin echo hi",
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
// "time curl", "nice curl" and friends cover the process-wrapper
// family that exec their argv tail under their own argv[0].
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
		"time curl http://x",
		"nice curl http://x",
		"ionice curl http://x",
		"taskset 1 curl http://x",
		"stdbuf -o0 curl http://x",
		"strace curl http://x",
		"ltrace curl http://x",
		"script -c 'curl http://x' /tmp/log",
		"flock /tmp/x curl http://x",
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

// TestPolicy_BuiltinDenyBlocksStatefulBuiltins guards the bypass
// vector where shell builtins register code to run later or mutate
// the shell state of subsequent segments. A deny-only policy on
// "curl" must reject "trap 'curl http://x' EXIT", "alias x=curl"
// and "export PATH=./bin && allowed_cmd" alike, otherwise an
// attacker can re-enter through the shell's own surface.
func TestPolicy_BuiltinDenyBlocksStatefulBuiltins(t *testing.T) {
	p := PolicyFromLists(nil, []string{"curl"})
	cases := []string{
		"trap 'curl http://x' EXIT",
		"alias x=curl",
		"unalias ls",
		"enable -f mod.so cmd",
		"export PATH=./bin",
		"unset PATH",
		"readonly PATH=./bin",
		"local PATH=./bin",
		"declare -x PATH=./bin",
		"typeset -x PATH=./bin",
		"set -o vi",
		"shopt -s extdebug",
		"hash -p ./echo echo",
		"cd /tmp",
		"pushd /tmp",
		"popd",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			err := CheckCommand(in, p)
			if err == nil ||
				!strings.Contains(err.Error(), "built-in policy") {
				t.Fatalf(
					"expected built-in deny for %q, got: %v", in, err,
				)
			}
		})
	}
}

// TestPolicy_BuiltinDenyBlocksVarMutatingBuiltins guards a
// state-mutation bypass vector: shell builtins that assign to a
// shell variable can rewrite PATH (or any other resolution state)
// before a subsequent allowed segment runs, even though argv[0]
// of the mutator segment is itself harmless. The classic PoC is
// `printf -v PATH ./work/bin; git`, which under bash sets PATH
// from inside the shell and then resolves `git` to
// `./work/bin/git` - a workspace-controlled binary - despite
// `printf` and `git` both passing an argv[0]-only check.
func TestPolicy_BuiltinDenyBlocksVarMutatingBuiltins(t *testing.T) {
	// Allow `git` so we exercise the "harmless argv[0] segment +
	// mutator that rewrites PATH first" shape, not just a default
	// deny on the mutator itself.
	p := PolicyFromLists([]string{"git", "echo"}, nil)
	cases := []string{
		// bash extension: `printf -v VAR FORMAT [ARGS]` writes
		// the formatted output to VAR instead of stdout.
		"printf -v PATH ./work/bin",
		"printf -v PATH ./work/bin ; git",
		// POSIX: read assigns to a named variable from stdin.
		"read PATH",
		// POSIX: getopts writes OPTARG / the named variable.
		"getopts a x",
		// bash arithmetic assignment.
		"let X=1",
		// bash array fillers.
		"mapfile X",
		"readarray X",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			err := CheckCommand(in, p)
			if err == nil ||
				!strings.Contains(err.Error(), "built-in policy") {
				t.Fatalf(
					"expected built-in deny for %q, got: %v", in, err,
				)
			}
		})
	}
}

// TestParse_DoubleQuotedBackslashPosix pins the POSIX rule that
// inside a double-quoted string the backslash is only special
// before $, `, ", \\ (and newline, which we reject outright).
// Folding `\X` to `X` unconditionally would let `"./s\afe"` parse
// as `./safe` while the shell still execs `./s\afe`, allowing a
// workspace-controlled file with a backslash-bearing name to
// bypass an allowlist entry for the folded form.
func TestParse_DoubleQuotedBackslashPosix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "backslash before non-special char preserved",
			in:   `echo "./s\afe"`,
			want: []string{"echo", `./s\afe`},
		},
		{
			name: "backslash before letter preserved",
			in:   `echo "a\bc"`,
			want: []string{"echo", `a\bc`},
		},
		{
			name: "backslash before dollar escapes",
			in:   `echo "a\$b"`,
			want: []string{"echo", "a$b"},
		},
		{
			name: "backslash before backtick escapes",
			in:   "echo \"a\\`b\"",
			want: []string{"echo", "a`b"},
		},
		{
			name: "backslash before double quote escapes",
			in:   `echo "a\"b"`,
			want: []string{"echo", `a"b`},
		},
		{
			name: "backslash before backslash escapes",
			in:   `echo "a\\b"`,
			want: []string{"echo", `a\b`},
		},
		{
			name: "multiple non-special escapes preserved",
			in:   `echo "\a\b\c"`,
			want: []string{"echo", `\a\b\c`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equal(pipe.Commands[0], tc.want) {
				t.Fatalf("argv: got %q want %q",
					pipe.Commands[0], tc.want)
			}
		})
	}
}

// TestPolicy_AllowExactPathAvoidsBackslashBypass demonstrates the
// concrete bypass that the POSIX backslash rule closes: an allow
// entry "./safe" must not admit a `"./s\afe"` invocation whose
// shell-executed argv[0] differs from the allowlisted form.
func TestPolicy_AllowExactPathAvoidsBackslashBypass(t *testing.T) {
	p := PolicyFromLists([]string{"./safe"}, nil)
	err := CheckCommand(`"./s\afe"`, p)
	if err == nil {
		t.Fatalf(
			"expected deny: parser must not fold \"./s\\afe\" to " +
				"./safe (shell would still exec ./s\\afe)",
		)
	}
	if !strings.Contains(err.Error(), "not in allowed_commands") {
		t.Fatalf("expected allowlist miss, got: %v", err)
	}
}

// TestPolicy_DenyEntryNormalizedOnWindows guards the bypass where
// the configured deny entry is written with a Windows extension or
// in mixed case while the command in the wild is the bare/lower
// form (or vice versa). Both directions must be caught.
func TestPolicy_DenyEntryNormalizedOnWindows(t *testing.T) {
	cases := []struct {
		name      string
		denyEntry string
		cmd       string
	}{
		{"entry CURL, cmd curl.exe", "CURL", "curl.exe"},
		{"entry curl.exe, cmd CURL", "curl.exe", "CURL"},
		{"entry curl.exe, cmd curl", "curl.exe", "curl"},
		{"entry CURL.EXE, cmd curl", "CURL.EXE", "curl"},
		{"entry Curl, cmd curl.exe", "Curl", "curl.exe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PolicyFromLists(nil, []string{tc.denyEntry})
			err := p.checkSegmentForGOOS(
				[]string{tc.cmd, "http://x"}, "windows",
			)
			if err == nil || !strings.Contains(
				err.Error(), "denied by denied_commands",
			) {
				t.Fatalf(
					"deny entry %q vs cmd %q on windows: got %v",
					tc.denyEntry, tc.cmd, err,
				)
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

// TestPolicy_AllowRejectsPathfulBasenameBypass guards the
// asymmetric matching contract documented on Policy: an allow
// entry "echo" must let through bare "echo" but reject "./echo",
// "work/bin/echo" and "/usr/bin/echo", because a workspace-
// controlled file at "./echo" can otherwise smuggle past a
// basename-only allowlist check.
func TestPolicy_AllowRejectsPathfulBasenameBypass(t *testing.T) {
	p := PolicyFromLists([]string{"echo"}, nil)
	if err := CheckCommand("echo hi", p); err != nil {
		t.Fatalf("bare echo should be allowed, got: %v", err)
	}
	cases := []string{
		"./echo hi",
		"work/bin/echo hi",
		"/usr/bin/echo hi",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			err := CheckCommand(in, p)
			if err == nil || !strings.Contains(
				err.Error(), "not in allowed_commands",
			) {
				t.Fatalf(
					"expected pathful basename bypass to be rejected, got: %v",
					err,
				)
			}
		})
	}
}

// TestPolicy_AllowMatchesExplicitPath confirms that operators who
// genuinely want to permit a specific path (e.g. a vetted binary
// outside the workspace) can list that exact path and have it
// match verbatim. The bare basename remains rejected because the
// operator chose to be specific.
func TestPolicy_AllowMatchesExplicitPath(t *testing.T) {
	p := PolicyFromLists([]string{"/usr/bin/echo"}, nil)
	if err := CheckCommand("/usr/bin/echo hi", p); err != nil {
		t.Fatalf("explicit path should be allowed, got: %v", err)
	}
	err := CheckCommand("echo hi", p)
	if err == nil || !strings.Contains(
		err.Error(), "not in allowed_commands",
	) {
		t.Fatalf("bare basename should not match explicit path, got: %v", err)
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

// TestNormalizeName_NonWindowsKeepsExtensions guards that the
// Windows .exe / .cmd / ... stripping never leaks into non-Windows
// GOOS. Case folding does apply universally (so "CURL" → "curl"
// even on Linux, to handle macOS APFS's case-insensitive resolver
// and to keep the deny set robust against workspace-controlled
// upper-case binaries), but suffixes like ".exe" are literal parts
// of the file name on POSIX and must stay.
func TestNormalizeName_NonWindowsKeepsExtensions(t *testing.T) {
	cases := []struct {
		in, goos, want string
	}{
		{"cmd.exe", "linux", "cmd.exe"},
		{"CURL", "linux", "curl"},
		{"Script.BAT", "linux", "script.bat"},
		{"curl", "linux", "curl"},
		{"cmd.exe", "darwin", "cmd.exe"},
		{"CURL", "darwin", "curl"},
		{"SH", "darwin", "sh"},
		{"curl", "darwin", "curl"},
		{"", "linux", ""},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.in, func(t *testing.T) {
			if got := normalizeName(tc.in, tc.goos); got != tc.want {
				t.Fatalf(
					"normalizeName(%q, %q) = %q, want %q",
					tc.in, tc.goos, got, tc.want,
				)
			}
		})
	}
}

// TestPolicy_DenyCaseInsensitiveAcrossOS guards the macOS / Windows
// bypass where the case-insensitive file system resolver matches a
// differently-cased command to the binary the user intended to
// deny ("CURL" → /usr/bin/curl on default APFS). Both Darwin and
// Linux exercise the user denylist; the Linux assertion is
// defence-in-depth - even on case-sensitive FS we prefer to fail
// closed against workspace-controlled "Curl" binaries.
func TestPolicy_DenyCaseInsensitiveAcrossOS(t *testing.T) {
	p := PolicyFromLists(nil, []string{"curl"})
	cases := []struct {
		name, goos, cmd string
	}{
		{"darwin CURL", "darwin", "CURL"},
		{"darwin Curl", "darwin", "Curl"},
		{"linux CURL", "linux", "CURL"},
		{"darwin /usr/bin/CURL", "darwin", "/usr/bin/CURL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.checkSegmentForGOOS(
				[]string{tc.cmd, "http://x"}, tc.goos,
			)
			if err == nil || !strings.Contains(
				err.Error(), "denied by denied_commands",
			) {
				t.Fatalf(
					"deny %q on %s should reject, got: %v",
					tc.cmd, tc.goos, err,
				)
			}
		})
	}
}

// TestPolicy_AllowCaseSensitiveOnLinux guards the bypass where a
// universally case-folded allowlist would silently broaden to
// admit a different workspace file on a case-sensitive FS:
// `WithAllowedCommands("./safe")` on Linux must not let "./SAFE"
// through, because Linux treats them as different paths and a
// workspace-controlled "./SAFE" can otherwise smuggle past the
// allowlist. Bare-name forms are also exact-case on Linux for
// consistency.
func TestPolicy_AllowCaseSensitiveOnLinux(t *testing.T) {
	t.Run("pathful entry rejects upper-case path", func(t *testing.T) {
		p := PolicyFromLists([]string{"./safe"}, nil)
		if err := p.checkSegmentForGOOS(
			[]string{"./safe", "arg"}, "linux",
		); err != nil {
			t.Fatalf("exact-case pathful allow should pass: %v", err)
		}
		err := p.checkSegmentForGOOS(
			[]string{"./SAFE", "arg"}, "linux",
		)
		if err == nil || !strings.Contains(
			err.Error(), "not in allowed_commands",
		) {
			t.Fatalf(
				"./SAFE must not match allow ./safe on linux, got: %v",
				err,
			)
		}
	})
	t.Run("bare entry rejects upper-case bare name", func(t *testing.T) {
		p := PolicyFromLists([]string{"echo"}, nil)
		if err := p.checkSegmentForGOOS(
			[]string{"echo", "hi"}, "linux",
		); err != nil {
			t.Fatalf("exact-case bare allow should pass: %v", err)
		}
		err := p.checkSegmentForGOOS(
			[]string{"ECHO", "hi"}, "linux",
		)
		if err == nil || !strings.Contains(
			err.Error(), "not in allowed_commands",
		) {
			t.Fatalf(
				"ECHO must not match allow echo on linux, got: %v",
				err,
			)
		}
	})
}

// TestPolicy_AllowBareFoldsOnDarwinWindows pins the bare-name
// allow fold on Windows and macOS: their default file systems
// resolve "ECHO" and "echo" to the same /bin/echo entry, and
// the policy mode resets PATH to a known-good default before
// resolution, so folding bare allow on those platforms is
// convenient and does not widen access.
func TestPolicy_AllowBareFoldsOnDarwinWindows(t *testing.T) {
	p := PolicyFromLists([]string{"echo"}, nil)
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			if err := p.checkSegmentForGOOS(
				[]string{"ECHO", "hi"}, goos,
			); err != nil {
				t.Fatalf("%s allow should fold bare case: %v", goos, err)
			}
		})
	}
}

// TestPolicy_AllowPathfulAlwaysExactCase guards the policy that
// pathful allow entries are exact-case on every OS, including
// macOS / Windows whose default file systems are
// case-insensitive. We cannot reliably tell whether the actual
// workspace volume is case-sensitive (APFS supports opt-in
// case-sensitive volumes, container layers can mix filesystems),
// so folding pathful allow would silently widen "./safe" to
// admit a workspace-controlled "./SAFE" on case-sensitive
// volumes. Operators who need both list both.
func TestPolicy_AllowPathfulAlwaysExactCase(t *testing.T) {
	p := PolicyFromLists([]string{"./safe"}, nil)
	for _, goos := range []string{"darwin", "windows", "linux"} {
		t.Run(goos+"/exact passes", func(t *testing.T) {
			if err := p.checkSegmentForGOOS(
				[]string{"./safe", "arg"}, goos,
			); err != nil {
				t.Fatalf(
					"exact-case pathful allow should pass on %s: %v",
					goos, err,
				)
			}
		})
		t.Run(goos+"/upper rejected", func(t *testing.T) {
			err := p.checkSegmentForGOOS(
				[]string{"./SAFE", "arg"}, goos,
			)
			if err == nil || !strings.Contains(
				err.Error(), "not in allowed_commands",
			) {
				t.Fatalf(
					"pathful allow must be exact-case on %s, got: %v",
					goos, err,
				)
			}
		})
	}
}

// TestPolicy_BuiltinDenyCaseInsensitiveAcrossOS covers the same
// bypass against the unconditional implicit deny set: on default
// macOS APFS "SH -c '...'" resolves to /bin/sh, so without case
// folding the wrapper deny is one capitalisation away from being
// useless. The Linux case folds defensively for the same reason
// as the user-deny test above.
func TestPolicy_BuiltinDenyCaseInsensitiveAcrossOS(t *testing.T) {
	p := PolicyFromLists(nil, []string{"curl"})
	cases := []struct {
		name, goos, cmd string
	}{
		{"darwin SH -c", "darwin", "SH"},
		{"darwin Bash -lc", "darwin", "Bash"},
		{"darwin EVAL", "darwin", "EVAL"},
		{"darwin TIME", "darwin", "TIME"},
		{"linux SH", "linux", "SH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.checkSegmentForGOOS(
				[]string{tc.cmd, "-c", "curl http://x"}, tc.goos,
			)
			if err == nil || !strings.Contains(
				err.Error(), "built-in policy",
			) {
				t.Fatalf(
					"implicit deny %q on %s should reject, got: %v",
					tc.cmd, tc.goos, err,
				)
			}
		})
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
