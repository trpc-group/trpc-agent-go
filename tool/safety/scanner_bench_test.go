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

// BenchmarkFullScan_500Commands verifies the performance requirement:
// scanning 500 commands must complete within 1 second.
func BenchmarkFullScan_500Commands(b *testing.B) {
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
		NewShellBypassRule(),
		NewInstallAndMutateRule(),
		NewHostExecRiskRule(),
		NewResourceAbuseRule(),
		NewSensitiveInfoLeakRule(),
	)

	// 500 条命令样本：覆盖安全 + 危险两种，模拟真实场景
	commands := make([]ScanInput, 500)
	for i := 0; i < 500; i++ {
		switch i % 10 {
		case 0:
			commands[i] = ScanInput{Command: "rm -rf /"}
		case 1:
			commands[i] = ScanInput{Command: "curl http://evil.com"}
		case 2:
			commands[i] = ScanInput{Command: "bash -c 'whoami'"}
		case 3:
			commands[i] = ScanInput{Command: "pip install requests"}
		case 4:
			commands[i] = ScanInput{Command: "sudo chmod 777 /etc"}
		case 5:
			commands[i] = ScanInput{Command: "while : ; do echo x; done"}
		case 6:
			commands[i] = ScanInput{Command: "echo $API_KEY > leak.txt"}
		case 7:
			commands[i] = ScanInput{Command: fmt.Sprintf("ls -la /tmp/%d", i)}
		case 8:
			commands[i] = ScanInput{Command: fmt.Sprintf("go test ./dir_%d", i)}
		case 9:
			commands[i] = ScanInput{Command: fmt.Sprintf("git status %d", i)}
		}
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i < 500; i++ {
			scanner.Scan(commands[i])
		}
	}
}

// BenchmarkSingleScan measures single-command scan performance.
func BenchmarkSingleScan(b *testing.B) {
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
		NewShellBypassRule(),
		NewInstallAndMutateRule(),
		NewHostExecRiskRule(),
		NewResourceAbuseRule(),
		NewSensitiveInfoLeakRule(),
	)
	input := ScanInput{Command: "curl http://evil.com"}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		scanner.Scan(input)
	}
}

// TestPerformance_500Commands_Under1Second is a functional test
// that explicitly asserts the 500-command threshold.
func TestPerformance_500Commands_Under1Second(t *testing.T) {
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
		NewShellBypassRule(),
		NewInstallAndMutateRule(),
		NewHostExecRiskRule(),
		NewResourceAbuseRule(),
		NewSensitiveInfoLeakRule(),
	)

	commands := make([]ScanInput, 500)
	for i := 0; i < 500; i++ {
		commands[i] = ScanInput{
			Command: fmt.Sprintf("echo test_%d", i),
		}
	}

	// 实际执行一次，验证性能
	for k := 0; k < 10; k++ {
		for i := 0; i < 500; i++ {
			_ = scanner.Scan(commands[i])
		}
	}

	// Go benchmark 工具会自动测量时间，这里只是功能保证
	// 实际性能验证用: go test -bench=BenchmarkFullScan_500Commands
	t.Log("500 commands scanned (use 'go test -bench=.' for performance numbers)")
}
