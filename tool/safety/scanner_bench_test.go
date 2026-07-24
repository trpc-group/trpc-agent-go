// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"fmt"
	"testing"
)

// BenchmarkScanner500Commands500Lines scans a corpus of 500 separate,
// single-line commands. shellsafe intentionally rejects a 500-line command,
// so the batch models 500 independent pre-execution requests.
func BenchmarkScanner500Commands500Lines(b *testing.B) {
	inputs := make([]ScanInput, 500)
	for i := range inputs {
		inputs[i] = ScanInput{Command: fmt.Sprintf("echo line-%d", i)}
	}
	scanner := NewScanner(&Policy{AllowedCommands: []string{"echo"}})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			_ = scanner.Scan(input)
		}
	}
}
