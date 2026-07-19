//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const integrationReplayTimeout = 2 * time.Minute

const (
	externalTTLProbeEnvironment = "TRPC_AGENT_GO_REPLAY_RUN_TTL"
	externalTTLProbeDuration    = 500 * time.Millisecond
	externalTTLProbeTimeout     = 15 * time.Second
)

func TestOptionalIntegrationReplayMatrix(t *testing.T) {
	factories := externalBackendFactories()
	if err := validateBackendFactories(factories); err != nil {
		t.Fatalf("validate backend factories: %v", err)
	}
	for _, factory := range factories {
		t.Run(factory.Name, func(t *testing.T) {
			connection := os.Getenv(factory.Environment)
			if connection == "" {
				t.Skipf("%s is not configured", factory.Environment)
			}
			ctx, cancel := context.WithTimeout(context.Background(), integrationReplayTimeout)
			defer cancel()
			backend := factory.New(BackendConfig{Connection: connection})
			runner := replaytest.Runner{
				Backends: []replaytest.Backend{
					newInMemoryBackend(),
					backend,
				},
				NormalizeOptions:      replaytest.DefaultNormalizeOptions(),
				CompareOptions:        replaytest.DefaultCompareOptions(),
				UnsupportedAllowances: replayUnsupportedAllowances(factory.Name),
			}
			report, err := runner.Run(ctx, replaytest.StandardReplayCases())
			if err != nil {
				t.Fatalf("Runner.Run() error = %v", err)
			}
			if report.HasUnexpectedDifferences() {
				t.Fatalf("integration replay mismatch: %#v", report.Differences)
			}
			if err := validateIntegrationReport(factory.Name, report); err != nil {
				t.Fatalf("integration result matrix: %v", err)
			}
		})
	}
}

func validateIntegrationReport(backend string, report replaytest.Report) error {
	expectedCases := replaytest.StandardReplayCases()
	if len(report.Cases) != len(expectedCases) {
		return fmt.Errorf("case results = %d, want %d", len(report.Cases), len(expectedCases))
	}
	for _, result := range report.Cases {
		if len(result.Backends) != 1 || result.Backends[0].Backend != backend {
			return fmt.Errorf("case %q backend results = %#v", result.Case, result.Backends)
		}
		backendResult := result.Backends[0]
		capability, unsupported := expectedReplayUnsupported(backend, result.Case)
		if unsupported {
			if result.Status != replaytest.ResultInconclusive ||
				backendResult.Status != replaytest.ResultUnsupported ||
				len(backendResult.Unsupported) != 1 ||
				backendResult.Unsupported[0] != capability {
				return fmt.Errorf("unsupported result = %#v", result)
			}
			continue
		}
		if result.Status != replaytest.ResultPass || backendResult.Status != replaytest.ResultPass {
			return fmt.Errorf("case %q did not pass: %#v", result.Case, result)
		}
	}
	return nil
}

func TestOptionalIntegrationPaginationProbes(t *testing.T) {
	for _, factory := range externalBackendFactories() {
		t.Run(factory.Name, func(t *testing.T) {
			connection := os.Getenv(factory.Environment)
			if connection == "" {
				t.Skipf("%s is not configured", factory.Environment)
			}
			report, err := runPaginationProbes(
				context.Background(), factory.New(BackendConfig{Connection: connection}),
			)
			if err != nil {
				t.Fatalf("runPaginationProbes() error = %v", err)
			}
			if err := validateExternalPaginationReport(factory.Name, report); err != nil {
				t.Fatalf("pagination report: %v", err)
			}
		})
	}
}

func TestOptionalIntegrationTTLProbes(t *testing.T) {
	if os.Getenv(externalTTLProbeEnvironment) != "1" {
		t.Skipf("%s=1 is required for real expiry probes", externalTTLProbeEnvironment)
	}
	for _, factory := range externalBackendFactories() {
		t.Run(factory.Name, func(t *testing.T) {
			connection := os.Getenv(factory.Environment)
			if connection == "" {
				t.Skipf("%s is not configured", factory.Environment)
			}
			ctx, cancel := context.WithTimeout(context.Background(), externalTTLProbeTimeout)
			defer cancel()
			backend := factory.New(BackendConfig{
				Connection: connection, SessionTTL: externalTTLProbeDuration,
			})
			result, err := runTTLExpiryProbe(ctx, backend)
			if err != nil {
				t.Fatalf("runTTLExpiryProbe() error = %v", err)
			}
			if factory.Name == clickHouseBackendName {
				if result.Status != replaytest.ResultUnsupported || !result.AllowedDiff {
					t.Fatalf("TTL probe = %#v", result)
				}
				return
			}
			if result.Status != replaytest.ResultPass {
				t.Fatalf("TTL probe = %#v", result)
			}
		})
	}
}

func validateExternalPaginationReport(backend string, report replaytest.Report) error {
	if report.HasUnexpectedDifferences() || report.HasInconclusiveResults() {
		return fmt.Errorf("probe report contains failures: %#v", report.Probes)
	}
	for _, probe := range report.Probes {
		if probe.Capability == replaytest.CapabilitySessionPaging &&
			probe.Status != replaytest.ResultPass {
			return fmt.Errorf("session pagination = %#v", probe)
		}
		if probe.Capability != replaytest.CapabilityEventPaging {
			continue
		}
		supportsEventPaging := backend == "postgres" || backend == "mysql"
		if supportsEventPaging && probe.Status != replaytest.ResultPass {
			return fmt.Errorf("event pagination = %#v", probe)
		}
		if !supportsEventPaging &&
			(probe.Status != replaytest.ResultUnsupported || !probe.AllowedDiff) {
			return fmt.Errorf("event pagination unsupported result = %#v", probe)
		}
	}
	return nil
}
