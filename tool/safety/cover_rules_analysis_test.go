//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoverrules_BuildAnalysis_EmptyInput(t *testing.T) {
	a := buildAnalysis(ScanInput{}, DefaultPolicy())
	require.Nil(t, a.Pipeline)
	require.NoError(t, a.ParseError)
	require.Empty(t, a.CommandHash)
	require.Empty(t, a.CommandSummary)
	require.Empty(t, a.Executables)
}

func TestCoverrules_BuildAnalysis_ExplicitArgvSyntheticPipeline(t *testing.T) {
	a := buildAnalysis(ScanInput{Args: []string{"sleep", "30"}}, DefaultPolicy())
	require.NotNil(t, a.Pipeline)
	require.NoError(t, a.ParseError)
	require.Equal(t, []string{"sleep"}, a.Executables)
	require.Equal(t, int64(30), a.SleepSeconds)
}

func TestCoverrules_BuildAnalysis_ExplicitArgvWrapper(t *testing.T) {
	a := buildAnalysis(ScanInput{Args: []string{"sh", "-c", "ls"}}, DefaultPolicy())
	require.Contains(t, a.WrapperNames, "sh")
}

func TestCoverrules_BuildAnalysis_ExplicitArgvInstall(t *testing.T) {
	a := buildAnalysis(ScanInput{Args: []string{"npm", "install", "left-pad"}}, DefaultPolicy())
	require.True(t, a.InstallPackages)
}

func TestCoverrules_BuildAnalysis_ExplicitArgvOutputBomb(t *testing.T) {
	a := buildAnalysis(ScanInput{Args: []string{"yes"}}, DefaultPolicy())
	require.True(t, a.HasOutputBomb)
}

func TestCoverrules_BuildAnalysis_CommandAndCodeBlocks(t *testing.T) {
	in := ScanInput{
		Command: "go test ./...",
		CodeBlocks: []CodeBlock{
			{Language: "python", Code: `os.system("ls /tmp")`},
		},
	}
	a := buildAnalysis(in, DefaultPolicy())
	require.NoError(t, a.ParseError)
	require.NotEmpty(t, a.CommandHash)
	require.Contains(t, a.CommandSummary, "go test")
	// The embedded shell command is parsed, so "ls" is an executable.
	require.Contains(t, a.Executables, "go")
	require.Contains(t, a.Executables, "ls")
}

func TestCoverrules_BuildAnalysis_CodeOnlyInput(t *testing.T) {
	in := ScanInput{
		CodeBlocks: []CodeBlock{
			{Language: "python", Code: "print('hello')"},
		},
	}
	a := buildAnalysis(in, DefaultPolicy())
	// No command means no parse failure, but hash/summary are non-empty.
	require.NoError(t, a.ParseError)
	require.NotEmpty(t, a.CommandHash)
	require.Contains(t, a.CommandSummary, "python:")
}

func TestCoverrules_HashAnalysisInput_EmptyInput(t *testing.T) {
	require.Empty(t, hashAnalysisInput(ScanInput{}))
}

func TestCoverrules_HashAnalysisInput_Deterministic(t *testing.T) {
	in := ScanInput{
		Command:    "ls",
		CodeBlocks: []CodeBlock{{Language: "go", Code: "package main"}},
	}
	require.Equal(t, hashAnalysisInput(in), hashAnalysisInput(in))
	require.Len(t, hashAnalysisInput(in), 64)
}

func TestCoverrules_SummarizeAnalysisInput_ManyCodeBlocks(t *testing.T) {
	in := ScanInput{
		CodeBlocks: []CodeBlock{
			{Language: "go", Code: "a"},
			{Language: "python", Code: "b"},
			{Language: "ruby", Code: "c"},
		},
	}
	s := summarizeAnalysisInput(in)
	require.Contains(t, s, "go:a")
	require.Contains(t, s, "python:b")
	require.Contains(t, s, "...")
	require.NotContains(t, s, "ruby:")
}

func TestCoverrules_SummarizeAnalysisInput_LongHintTruncated(t *testing.T) {
	long := strings.Repeat("x", 200)
	in := ScanInput{CodeBlocks: []CodeBlock{{Language: "go", Code: long}}}
	s := summarizeAnalysisInput(in)
	require.LessOrEqual(t, len(s), summaryMaxLen)
}

func TestCoverrules_SummarizeAnalysisInput_RedactsSecrets(t *testing.T) {
	in := ScanInput{Command: "curl -H 'Authorization: Bearer abcdefghijklmnop1234' https://github.com"}
	s := summarizeAnalysisInput(in)
	require.NotContains(t, s, "abcdefghijklmnop1234")
	require.Contains(t, s, "[REDACTED:")
}

func TestCoverrules_ClassifyParseError_AllShapes(t *testing.T) {
	cases := []struct {
		msg          string
		substitution bool
		redirection  bool
		background   bool
	}{
		{"command substitution is not allowed", true, false, false},
		{"parameter expansion is not allowed", true, false, false},
		{"arithmetic expansion is not allowed", true, false, false},
		{"process substitution is not allowed", true, false, false},
		{"backtick quoting is not allowed", true, false, false},
		{"redirection is not allowed", false, true, false},
		{"background execution is not allowed", false, false, true},
		{"some other parse failure", false, false, false},
	}
	for _, tc := range cases {
		var a analysis
		classifyParseError(&a, errors.New(tc.msg))
		require.Equal(t, tc.substitution, a.HasSubstitution, tc.msg)
		require.Equal(t, tc.redirection, a.HasRedirection, tc.msg)
		require.Equal(t, tc.background, a.HasBackground, tc.msg)
	}
}

func TestCoverrules_AnalyzeShell_RedirectionParseError(t *testing.T) {
	a := analyzeShell("echo hi > out.txt")
	require.Error(t, a.ParseError)
	require.True(t, a.HasRedirection)
}

func TestCoverrules_IsNetworkSubcommand_GitVariants(t *testing.T) {
	require.False(t, isNetworkSubcommand(nil))

	require.True(t, isNetworkSubcommand([]string{"git", "clone", "github.com/org/repo"}))
	require.False(t, isNetworkSubcommand([]string{"git", "status"}))

	// Flags before the subcommand are skipped.
	require.False(t, isNetworkSubcommand([]string{"git", "-C", "/tmp", "status"}))

	// A valueless flag is skipped; the fetch subcommand still counts.
	require.True(t, isNetworkSubcommand([]string{"git", "--bare", "fetch", "origin"}))

	// Non-git network commands treat any bare host as a target.
	require.True(t, isNetworkSubcommand([]string{"curl", "github.com"}))
}

// TestCoverrules_IsNetworkSubcommand_PerSegment is the X5 regression: in
// `git status && git clone evil.com/x` the clone argument must be judged
// against its own segment, not the first segment of the pipeline.
func TestCoverrules_IsNetworkSubcommand_PerSegment(t *testing.T) {
	a := analyzeShell("git status && git clone evil.com/x")
	require.NoError(t, a.ParseError)
	found := false
	for _, tgt := range a.NetworkTargets {
		if tgt.Host == "evil.com" {
			found = true
		}
	}
	require.True(t, found,
		"bare host of the second git segment must be a network target; targets=%+v",
		a.NetworkTargets)
}

func TestCoverrules_ClassifyToken_GitCloneBareHost(t *testing.T) {
	a := analyzeShell("git clone github.com/org/repo")
	require.NotEmpty(t, a.NetworkTargets)
	require.Equal(t, "github.com", a.NetworkTargets[0].Host)
}

// TestCoverrules_ClassifyToken_KeyValuePathToken is the X12 regression:
// `dd of=/etc/passwd` must classify the value of the key=value token as
// a path-like argument.
func TestCoverrules_ClassifyToken_KeyValuePathToken(t *testing.T) {
	a := analyzeShell("dd if=x of=/etc/passwd count=1")
	require.Contains(t, a.PathOps,
		pathOp{Token: "/etc/passwd", Op: "write", Executable: "dd"})
}

func TestCoverrules_ClassifyToken_GitCloneAmbiguousHostSkipped(t *testing.T) {
	// A bare host without a dot is ambiguous and must not become a target.
	a := analyzeShell("git clone myhost/repo")
	for _, tgt := range a.NetworkTargets {
		require.NotEqual(t, "myhost", tgt.Host)
	}
}

func TestCoverrules_ClassifyToken_GitStatusNotNetworkTarget(t *testing.T) {
	a := analyzeShell("git status")
	require.Empty(t, a.NetworkTargets)
}

func TestCoverrules_OpForCommand_Table(t *testing.T) {
	cases := map[string]string{
		"rm":      "delete",
		"rmdir":   "delete",
		"unlink":  "delete",
		"mv":      "write",
		"cp":      "write",
		"dd":      "write",
		"cat":     "read",
		"grep":    "read",
		">":       "write",
		">>":      "write",
		">x":      "write",
		"python3": "execute",
	}
	for exec, want := range cases {
		require.Equal(t, want, opForCommand(exec), exec)
	}
}

func TestCoverrules_HashCommand_Empty(t *testing.T) {
	require.Empty(t, hashCommand(""))
	require.Len(t, hashCommand("ls"), 64)
}

func TestCoverrules_ExtractNetworkTarget(t *testing.T) {
	t.Run("full URL", func(t *testing.T) {
		tgt := extractNetworkTarget("https://example.com/x")
		require.False(t, tgt.Malformed)
		require.Equal(t, "example.com", tgt.Host)
		require.Equal(t, "https", tgt.Scheme)
	})
	t.Run("ambiguous URL host is malformed", func(t *testing.T) {
		tgt := extractNetworkTarget("https://127.0.0.1/x")
		require.True(t, tgt.Malformed)
	})
	t.Run("URL without host is malformed", func(t *testing.T) {
		tgt := extractNetworkTarget("https:///path")
		require.True(t, tgt.Malformed)
	})
	t.Run("bare host with port and path", func(t *testing.T) {
		tgt := extractNetworkTarget("Example.COM:8080/repo")
		require.False(t, tgt.Malformed)
		require.Equal(t, "example.com", tgt.Host)
	})
	t.Run("SCP-like user prefix is stripped", func(t *testing.T) {
		tgt := extractNetworkTarget("git@github.com:org/repo.git")
		require.False(t, tgt.Malformed)
		require.Equal(t, "github.com", tgt.Host)
	})
	t.Run("single trailing dot is trimmed", func(t *testing.T) {
		tgt := extractNetworkTarget("github.com.")
		require.False(t, tgt.Malformed)
		require.Equal(t, "github.com", tgt.Host)
	})
	t.Run("localhost is malformed", func(t *testing.T) {
		tgt := extractNetworkTarget("localhost")
		require.True(t, tgt.Malformed)
		require.Equal(t, "localhost", tgt.Host)
	})
	t.Run("bare name without dot is malformed", func(t *testing.T) {
		tgt := extractNetworkTarget("internal")
		require.True(t, tgt.Malformed)
	})
	t.Run("empty host yields empty target", func(t *testing.T) {
		tgt := extractNetworkTarget("/:x")
		require.Empty(t, tgt.Raw)
		require.Empty(t, tgt.Host)
	})
}

func TestCoverrules_IsPathLike(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"", false},
		{"~/.ssh", true},
		{"/abs/path", true},
		{"./rel", true},
		{"../up", true},
		{".env", true},
		{"..", false},
		{"a/b", true},
		{"plain", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, isPathLike(tc.tok), tc.tok)
	}
}

func TestCoverrules_LooksLikeURL(t *testing.T) {
	require.True(t, looksLikeURL("https://example.com"))
	require.True(t, looksLikeURL("git://example.com/repo"))
	require.False(t, looksLikeURL("example.com"))
	require.False(t, looksLikeURL("http:/missing-slash"))
}

func TestCoverrules_MergeAnalysis_PreservesHashAndSummary(t *testing.T) {
	a := analysis{CommandHash: "hash", CommandSummary: "summary", SleepSeconds: -1}
	shell := analyzeShell("sleep 5")
	mergeAnalysis(&a, &shell)
	require.Equal(t, "hash", a.CommandHash)
	require.Equal(t, "summary", a.CommandSummary)
	require.Equal(t, int64(5), a.SleepSeconds)
	require.Equal(t, shell.Source, a.Source)
}

// TestCoverrules_MergeAnalysis_ParseFailureIsSticky is the X1 regression:
// a later successful parse must not erase an earlier parse failure, and
// the merged pipeline must stay nil so the raw-source fallbacks engage.
func TestCoverrules_MergeAnalysis_ParseFailureIsSticky(t *testing.T) {
	a := analysis{SleepSeconds: -1}

	bad := analyzeShell("cat ~/.ssh/id_rsa; echo $HOME")
	require.Error(t, bad.ParseError)
	mergeAnalysis(&a, &bad)
	require.Error(t, a.ParseError)
	require.Nil(t, a.Pipeline)
	require.True(t, a.HasSubstitution)

	good := analyzeShell("ls")
	require.NoError(t, good.ParseError)
	mergeAnalysis(&a, &good)
	require.Error(t, a.ParseError,
		"a later successful parse must not erase an earlier failure")
	require.Nil(t, a.Pipeline,
		"pipeline must stay nil so raw-source fallbacks engage")
	// The failed block's source must survive for the raw-source scans.
	require.Contains(t, a.Source, "id_rsa")
}

// TestCoverrules_MergeAnalysis_SuccessfulPipelinesAccumulate verifies
// that two successful merges concatenate pipeline segments instead of
// the later one replacing the earlier one.
func TestCoverrules_MergeAnalysis_SuccessfulPipelinesAccumulate(t *testing.T) {
	a := analysis{SleepSeconds: -1}
	first := analyzeShell("ls /tmp")
	second := analyzeShell("cat /etc/hostname")
	mergeAnalysis(&a, &first)
	mergeAnalysis(&a, &second)
	require.NoError(t, a.ParseError)
	require.NotNil(t, a.Pipeline)
	require.Len(t, a.Pipeline.Commands, 2)
}

func TestCoverrules_IsGitSubcommandName(t *testing.T) {
	require.True(t, isGitSubcommandName("clone"))
	require.True(t, isGitSubcommandName("status"))
	require.False(t, isGitSubcommandName("github.com"))
}

func TestCoverrules_IsNetworkCommandForPolicy_Configured(t *testing.T) {
	require.True(t, isNetworkCommandForPolicy("curl", nil))
	require.True(t, isNetworkCommandForPolicy("/usr/bin/mycurl", []string{"mycurl"}))
	require.False(t, isNetworkCommandForPolicy("ls", []string{"mycurl"}))
}
