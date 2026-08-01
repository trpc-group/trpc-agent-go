//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"reflect"
	"testing"
)

func TestNetworkHostExtractionBoundaries(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "curl proxy and URL",
			got: downloadCommandHosts("curl", []string{
				"-x", "http://evil.example:8080", "https://github.com/file",
			}),
			want: []string{"evil.example", "github.com"},
		},
		{
			name: "curl connect-to",
			got: downloadCommandHosts("curl", []string{
				"--connect-to", "github.com:443:evil.example:443",
			}),
			want: []string{"github.com", "evil.example"},
		},
		{
			name: "curl resolve",
			got: downloadCommandHosts("curl", []string{
				"--resolve=github.com:443:1.2.3.4,5.6.7.8",
			}),
			want: []string{"github.com", "1.2.3.4", "5.6.7.8"},
		},
		{
			name: "wget execute proxy",
			got: downloadCommandHosts("wget", []string{
				"-e", "https_proxy=http://evil.example:8080",
			}),
			want: []string{"evil.example"},
		},
		{
			name: "git submodule add",
			got: gitCommandHosts([]string{
				"submodule", "add", "https://evil.example/repo", "vendor/repo",
			}),
			want: []string{"evil.example"},
		},
		{
			name: "git URL rewrite",
			got: gitCommandHosts([]string{
				"-c", "url.https://evil.example/.insteadof=https://github.com/",
				"clone", "https://github.com/org/repo",
			}),
			want: []string{"evil.example", "github.com"},
		},
		{
			name: "go module after valued option",
			got: goCommandHosts([]string{
				"-C", "/tmp", "get", "example.com/mod@v1",
			}),
			want: []string{"example.com"},
		},
		{
			name: "scp remote",
			got: networkCommandHosts([]string{
				"scp", "-P", "22", "user@evil.example:/tmp/out",
			}),
			want: []string{"evil.example"},
		},
		{
			name: "ssh remote",
			got: networkCommandHosts([]string{
				"ssh", "-p", "22", "user@evil.example",
			}),
			want: []string{"evil.example"},
		},
		{
			name: "custom URL command",
			got: networkCommandHosts([]string{
				"custom-fetch", "https://evil.example/file",
			}),
			want: []string{"evil.example"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Fatalf("hosts = %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestStaticCodeAnalysisBoundaries(t *testing.T) {
	for _, tc := range []struct {
		condition string
		want      bool
	}{
		{condition: "true", want: true},
		{condition: "0", want: false},
		{condition: "2", want: true},
		{condition: `"x"`, want: true},
		{condition: `""`, want: false},
		{condition: "2 == 2", want: true},
		{condition: "2 != 2", want: false},
		{condition: "1 < 2", want: true},
		{condition: "2 <= 1", want: false},
		{condition: "2 > 1", want: true},
		{condition: "1 >= 2", want: false},
		{condition: "unknown", want: false},
	} {
		t.Run(tc.condition, func(t *testing.T) {
			if got := constantConditionTrue(tc.condition); got != tc.want {
				t.Fatalf("constantConditionTrue() = %v, want %v", got, tc.want)
			}
		})
	}

	if !pythonHasRiskyWildcardImport("from os import *") {
		t.Fatal("os wildcard import was not classified as risky")
	}
	if pythonHasRiskyWildcardImport("from math import *") {
		t.Fatal("math wildcard import was classified as risky")
	}

	scanner := NewScanner(DefaultPolicy())
	tests := []struct {
		name     string
		language string
		code     string
		wantRule string
	}{
		{
			name:     "python wildcard import",
			language: "python",
			code:     "from os import *",
			wantRule: ruleCodePolicy,
		},
		{
			name:     "python dynamic open",
			language: "python",
			code:     "open(path)",
			wantRule: ruleCodePolicy,
		},
		{
			name:     "javascript risky destructure",
			language: "javascript",
			code:     `const {request} = require("https")`,
			wantRule: ruleCodePolicy,
		},
		{
			name:     "javascript dynamic file read",
			language: "javascript",
			code:     `const fs = require("fs"); fs.readFileSync(path)`,
			wantRule: ruleCodePolicy,
		},
		{
			name:     "invalid Go",
			language: "go",
			code:     "package main {",
			wantRule: ruleCodePolicy,
		},
		{
			name:     "Go process call",
			language: "go",
			code:     `package main; import "os/exec"; func main() { exec.Command("sh") }`,
			wantRule: ruleCommandPolicy,
		},
		{
			name:     "Go unresolved reflection",
			language: "go",
			code:     `package main; import "reflect"; func main() { reflect.ValueOf(1) }`,
			wantRule: ruleCodePolicy,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings := scanner.scanLanguageAwareCode(codeBlock{
				language: tc.language,
				code:     tc.code,
			})
			if !findingsContainRule(findings, tc.wantRule) {
				t.Fatalf("findings = %+v, want rule %q", findings, tc.wantRule)
			}
		})
	}
}

func findingsContainRule(findings []Finding, want string) bool {
	for _, finding := range findings {
		if finding.RuleID == want {
			return true
		}
	}
	return false
}
