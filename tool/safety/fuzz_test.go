//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

func FuzzRedactStringProperties(f *testing.F) {
	seeds := [][2]string{
		{"", ""},
		{"ordinary prefix", "ordinary suffix"},
		{"中文前缀", "中文后缀"},
		{"api_key=ordinary", "token budget"},
		{"\x00\x01", "\xff\xfe"},
	}

	for _, seed := range seeds {
		f.Add(seed[0], seed[1])
	}

	knownSecrets := []string{
		"api_key=sk-abcdefghijklmnop",
		"password=correct-horse-battery-staple",
		"Authorization: Bearer " +
			"abcdefghijklmnop123456",
		syntheticAWSAccessKey("1234567890ABCDEF"),
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
		"github_pat_11AA22BB33CC44DD55EE66FF",
		"-----BEGIN PRIVATE KEY-----\n" +
			"abcdefghijklmnop123456\n" +
			"-----END PRIVATE KEY-----",
	}

	f.Fuzz(func(
		t *testing.T,
		prefix string,
		suffix string,
	) {
		arbitraryInput := prefix + suffix

		first, _ := RedactString(arbitraryInput)
		second, _ := RedactString(first)

		if second != first {
			t.Fatalf(
				"RedactString() is not idempotent:\n"+
					"input=%q\nfirst=%q\nsecond=%q",
				arbitraryInput,
				first,
				second,
			)
		}

		for _, secret := range knownSecrets {
			input := prefix +
				"\n" +
				secret +
				"\n" +
				suffix

			redacted, changed := RedactString(input)

			if !changed {
				t.Fatalf(
					"RedactString() did not detect known secret %q",
					secret,
				)
			}

			if strings.Contains(redacted, secret) {
				t.Fatalf(
					"RedactString() leaked known secret %q: %q",
					secret,
					redacted,
				)
			}

			again, _ := RedactString(redacted)
			if again != redacted {
				t.Fatalf(
					"redacted output is not idempotent:\n"+
						"first=%q\nsecond=%q",
					redacted,
					again,
				)
			}
		}
	})
}

func FuzzScannerProperties(f *testing.F) {
	seeds := []string{
		"",
		"echo hello",
		"go test ./tool/safety",
		"echo hello | wc -c",
		"echo $(whoami)",
		"echo $HOME",
		"echo hello > output.txt",
		`""`,
		"rm -rf /",
		"cat ~/.ssh/id_rsa",
		"curl https://download.evil.example/data",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"echo",
		"go",
		"wc",
		"cat",
		"curl",
	}
	policy.NetworkAllowlist = []string{
		"github.com",
	}

	scanner := NewScanner(policy)

	f.Fuzz(func(t *testing.T, input string) {
		report, err := scanner.Scan(
			context.Background(),
			ScanRequest{
				ToolName: "workspace_exec",
				Command:  input,
				Backend:  BackendWorkspaceExec,
			},
		)
		if err != nil {
			t.Fatalf(
				"Scanner.Scan() error = %v",
				err,
			)
		}

		switch report.Decision {
		case DecisionAllow,
			DecisionAsk,
			DecisionDeny:
		default:
			t.Fatalf(
				"invalid Decision %q",
				report.Decision,
			)
		}

		if report.RuleID == "" {
			t.Fatal(
				"Scanner returned an empty RuleID",
			)
		}

		wantBlocked :=
			report.Decision != DecisionAllow
		if report.Blocked != wantBlocked {
			t.Fatalf(
				"Blocked = %t, want %t for Decision %q",
				report.Blocked,
				wantBlocked,
				report.Decision,
			)
		}

		if _, parseErr := shellsafe.Parse(input); parseErr != nil &&
			report.Decision == DecisionAllow {
			t.Fatalf(
				"unparseable command was silently allowed: %q",
				input,
			)
		}

		const secret = "ghp_" +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZ123456"

		secretReport, err := scanner.Scan(
			context.Background(),
			ScanRequest{
				ToolName: "workspace_exec",
				Command: "echo " +
					secret +
					" " +
					input,
				Backend: BackendWorkspaceExec,
			},
		)
		if err != nil {
			t.Fatalf(
				"Scanner.Scan(secret input) error = %v",
				err,
			)
		}

		encoded, err := json.Marshal(secretReport)
		if err != nil {
			t.Fatalf(
				"marshal ScanReport: %v",
				err,
			)
		}

		if strings.Contains(
			string(encoded),
			secret,
		) {
			t.Fatal(
				"ScanReport leaked the injected secret",
			)
		}
	})
}
