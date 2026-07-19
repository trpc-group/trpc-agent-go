//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replayconsistency

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	localTTLProbeDuration        = 150 * time.Millisecond
	localTTLProbeTimeout         = 5 * time.Second
	expectedPaginationProbeCount = 2
)

func TestLightweightPaginationCapabilityProbes(t *testing.T) {
	backends := []replaytest.Backend{
		newInMemoryBackend(),
		newSQLiteBackend(t.TempDir()),
	}
	for _, backend := range backends {
		t.Run(backend.Name, func(t *testing.T) {
			report, err := runPaginationProbes(context.Background(), backend)
			if err != nil {
				t.Fatalf("runPaginationProbes() error = %v", err)
			}
			assertPaginationProbeReport(t, backend.Name, report)
		})
	}
}

func TestLightweightTTLExpiryProbes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), localTTLProbeTimeout)
	defer cancel()
	config := BackendConfig{SessionTTL: localTTLProbeDuration}
	backends := []replaytest.Backend{
		newInMemoryBackendWithConfig(config),
		newSQLiteBackendWithConfig(t.TempDir(), config),
	}
	for _, backend := range backends {
		t.Run(backend.Name, func(t *testing.T) {
			result, err := runTTLExpiryProbe(ctx, backend)
			if err != nil {
				t.Fatalf("runTTLExpiryProbe() error = %v", err)
			}
			if result.Status != replaytest.ResultPass {
				t.Fatalf("TTL probe = %#v", result)
			}
		})
	}
}

func assertPaginationProbeReport(
	t *testing.T,
	backend string,
	report replaytest.Report,
) {
	t.Helper()
	if report.HasUnexpectedDifferences() || report.HasInconclusiveResults() {
		t.Fatalf("pagination report has failure: %#v", report)
	}
	if len(report.Probes) != expectedPaginationProbeCount {
		t.Fatalf(
			"probe count = %d, want %d", len(report.Probes), expectedPaginationProbeCount,
		)
	}
	for _, probe := range report.Probes {
		if probe.Backend != backend {
			t.Fatalf("probe backend = %q, want %q", probe.Backend, backend)
		}
		if probe.Capability == replaytest.CapabilitySessionPaging &&
			probe.Status != replaytest.ResultPass {
			t.Fatalf("session paging probe = %#v", probe)
		}
		if probe.Capability == replaytest.CapabilityEventPaging &&
			(probe.Status != replaytest.ResultUnsupported || !probe.AllowedDiff) {
			t.Fatalf("event paging probe = %#v", probe)
		}
	}
}
