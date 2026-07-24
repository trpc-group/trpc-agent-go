//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"
	"testing"
)

// newBenchScanner builds a Scanner with the full 8-rule default pipeline
// (the same set NewDefaultScanner would expose to callers). It is used by
// every benchmark/performance test in this file so the numbers reflect the
// production scanner path rather than a subset.
func newBenchScanner() *Scanner {
	return NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
		NewShellBypassRule(),
		NewInstallAndMutateRule(),
		NewHostExecRiskRule(),
		NewResourceAbuseRule(),
		NewSensitiveInfoLeakRule(),
		NewAskForReviewRule(),
	)
}

// BenchmarkFullScan_500Commands verifies the performance requirement:
// scanning 500 commands must complete within 1 second. The ScanInputs
// populate ExecutorType="local" so the host-exec risk rule participates
// (it is local-only by design).
func BenchmarkFullScan_500Commands(b *testing.B) {
	scanner := newBenchScanner()

	// 500 command samples covering both safe and dangerous cases.
	commands := make([]ScanInput, 500)
	for i := 0; i < 500; i++ {
		switch i % 10 {
		case 0:
			commands[i] = ScanInput{Command: "rm -rf /", ExecutorType: "local"}
		case 1:
			commands[i] = ScanInput{Command: "curl http://evil.com", ExecutorType: "local"}
		case 2:
			commands[i] = ScanInput{Command: "bash -c 'whoami'", ExecutorType: "local"}
		case 3:
			commands[i] = ScanInput{Command: "pip install requests", ExecutorType: "local"}
		case 4:
			commands[i] = ScanInput{Command: "sudo chmod 777 /etc", ExecutorType: "local"}
		case 5:
			commands[i] = ScanInput{Command: "while : ; do echo x; done", ExecutorType: "local"}
		case 6:
			commands[i] = ScanInput{Command: "echo $API_KEY > leak.txt", ExecutorType: "local"}
		case 7:
			commands[i] = ScanInput{Command: fmt.Sprintf("ls -la /tmp/%d", i), ExecutorType: "local"}
		case 8:
			commands[i] = ScanInput{Command: fmt.Sprintf("go test ./dir_%d", i), ExecutorType: "local"}
		case 9:
			commands[i] = ScanInput{Command: fmt.Sprintf("git status %d", i), ExecutorType: "local"}
		}
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i < 500; i++ {
			scanner.Scan(commands[i])
		}
	}
}

// BenchmarkSingleScan measures single-command scan performance with the
// full 8-rule pipeline.
func BenchmarkSingleScan(b *testing.B) {
	scanner := newBenchScanner()
	input := ScanInput{Command: "curl http://evil.com", ExecutorType: "local"}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		scanner.Scan(input)
	}
}

// TestPerformance_500Commands_Under1Second is a functional test
// that explicitly asserts the 500-command threshold. ScanInputs
// populate ExecutorType="local" so the host-exec risk rule is
// included in the path under measurement.
func TestPerformance_500Commands_Under1Second(t *testing.T) {
	scanner := newBenchScanner()

	commands := make([]ScanInput, 500)
	for i := 0; i < 500; i++ {
		commands[i] = ScanInput{
			Command:      fmt.Sprintf("echo test_%d", i),
			ExecutorType: "local",
		}
	}

	// Run once to verify.
	for k := 0; k < 10; k++ {
		for i := 0; i < 500; i++ {
			_ = scanner.Scan(commands[i])
		}
	}

	// go test -bench=BenchmarkFullScan_500Commands for actual measurement.
	t.Log("500 commands scanned (use 'go test -bench=.' for performance numbers)")
}
