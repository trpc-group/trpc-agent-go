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
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	requireExternalEnvironment   = "TRPC_AGENT_GO_REPLAY_REQUIRE_EXTERNAL"
	requireExternalEnabledValue  = "1"
	expectedExternalBackendCount = 4
	expectedRequiredCaseCount    = 10
	clickHouseBackendName        = "clickhouse"
	trackEventsCaseName          = "track-events"
	summaryUpdateCaseName        = "summary-update"
	summaryTruncationCaseName    = "summary-truncation"
	clickHouseTrackReason        = "session/clickhouse does not implement session.TrackService"
	clickHouseSummaryReason      = "session/clickhouse cannot scan Summary JSON on the verified ClickHouse 25.3 image"
)

func TestRequiredExternalReplayMatrix(t *testing.T) {
	if os.Getenv(requireExternalEnvironment) != requireExternalEnabledValue {
		t.Skipf("%s=%s is required", requireExternalEnvironment, requireExternalEnabledValue)
	}
	factories := externalBackendFactories()
	if err := validateBackendFactories(factories); err != nil {
		t.Fatalf("validate backend factories: %v", err)
	}
	backends, missing := loadRequiredExternalBackends(factories, os.Getenv)
	if len(missing) > 0 {
		t.Fatalf("required external backend variables are missing: %s", strings.Join(missing, ", "))
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationReplayTimeout)
	defer cancel()
	runner := replaytest.Runner{
		Backends:              append([]replaytest.Backend{newInMemoryBackend()}, backends...),
		NormalizeOptions:      replaytest.DefaultNormalizeOptions(),
		CompareOptions:        replaytest.DefaultCompareOptions(),
		UnsupportedAllowances: replayUnsupportedAllowances(backendNames(backends)...),
	}
	report, err := runner.Run(ctx, replaytest.StandardReplayCases())
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if report.HasUnexpectedDifferences() {
		t.Fatalf("required external replay mismatch: %#v", report.Differences)
	}
	if err := validateRequiredExternalReport(report); err != nil {
		t.Fatalf("required external result matrix: %v", err)
	}
}

func loadRequiredExternalBackends(
	factories []backendFactory,
	getenv func(string) string,
) ([]replaytest.Backend, []string) {
	backends := make([]replaytest.Backend, 0, len(factories))
	missing := make([]string, 0)
	for _, factory := range factories {
		connection := getenv(factory.Environment)
		if connection == "" {
			missing = append(missing, factory.Environment)
			continue
		}
		backends = append(backends, factory.New(BackendConfig{Connection: connection}))
	}
	sort.Strings(missing)
	return backends, missing
}

func validateRequiredExternalReport(report replaytest.Report) error {
	if len(report.Cases) != expectedRequiredCaseCount {
		return fmt.Errorf("case results = %d, want %d", len(report.Cases), expectedRequiredCaseCount)
	}
	expected := requiredExternalBackendNames()
	for _, result := range report.Cases {
		if err := validateRequiredCaseResult(result, expected); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredCaseResult(
	result replaytest.CaseResult,
	expected map[string]struct{},
) error {
	if result.Status != replaytest.ResultPass {
		return fmt.Errorf("case %q status = %q", result.Case, result.Status)
	}
	if len(result.Backends) != expectedExternalBackendCount {
		return fmt.Errorf(
			"case %q backend results = %d, want %d",
			result.Case, len(result.Backends), expectedExternalBackendCount,
		)
	}
	seen := make(map[string]struct{}, len(result.Backends))
	for _, backend := range result.Backends {
		if _, ok := expected[backend.Backend]; !ok {
			return fmt.Errorf("case %q has unexpected backend %q", result.Case, backend.Backend)
		}
		if _, duplicate := seen[backend.Backend]; duplicate {
			return fmt.Errorf("case %q duplicates backend %q", result.Case, backend.Backend)
		}
		seen[backend.Backend] = struct{}{}
		if err := validateRequiredBackendResult(result.Case, backend); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredBackendResult(
	caseName string,
	result replaytest.CaseBackendResult,
) error {
	capability, unsupported := expectedReplayUnsupported(result.Backend, caseName)
	if unsupported {
		if result.Status == replaytest.ResultUnsupported &&
			len(result.Unsupported) == 1 &&
			result.Unsupported[0] == capability {
			return nil
		}
		return fmt.Errorf("ClickHouse track result = %#v", result)
	}
	if result.Status != replaytest.ResultPass || len(result.Unsupported) != 0 {
		return fmt.Errorf("case %q backend result = %#v", caseName, result)
	}
	return nil
}

func requiredExternalBackendNames() map[string]struct{} {
	return map[string]struct{}{
		"redis": {}, "postgres": {}, "mysql": {}, clickHouseBackendName: {},
	}
}

func replayUnsupportedAllowances(backendNames ...string) []replaytest.UnsupportedAllowance {
	hasClickHouse := false
	for _, name := range backendNames {
		if name == clickHouseBackendName {
			hasClickHouse = true
			break
		}
	}
	if !hasClickHouse {
		return nil
	}
	return []replaytest.UnsupportedAllowance{
		newClickHouseAllowance(trackEventsCaseName, replaytest.CapabilityTrack, clickHouseTrackReason),
		newClickHouseAllowance(summaryUpdateCaseName, replaytest.CapabilitySummary, clickHouseSummaryReason),
		newClickHouseAllowance(summaryTruncationCaseName, replaytest.CapabilitySummary, clickHouseSummaryReason),
	}
}

func backendNames(backends []replaytest.Backend) []string {
	names := make([]string, 0, len(backends))
	for _, backend := range backends {
		names = append(names, backend.Name)
	}
	return names
}

func newClickHouseAllowance(
	caseName string,
	capability replaytest.Capability,
	reason string,
) replaytest.UnsupportedAllowance {
	return replaytest.UnsupportedAllowance{
		Backend: clickHouseBackendName, Case: caseName,
		Capability: capability, Reason: reason,
	}
}

func TestReplayUnsupportedAllowancesOnlyReferenceEnabledBackends(t *testing.T) {
	if got := replayUnsupportedAllowances("redis"); len(got) != 0 {
		t.Fatalf("redis allowances = %#v, want none", got)
	}
	got := replayUnsupportedAllowances("redis", clickHouseBackendName)
	if len(got) != 3 {
		t.Fatalf("clickhouse allowances = %d, want 3", len(got))
	}
	for _, allowance := range got {
		if allowance.Backend != clickHouseBackendName {
			t.Fatalf("allowance references disabled backend: %#v", allowance)
		}
	}
}

func expectedReplayUnsupported(backend, caseName string) (replaytest.Capability, bool) {
	if backend != clickHouseBackendName {
		return "", false
	}
	if caseName == trackEventsCaseName {
		return replaytest.CapabilityTrack, true
	}
	switch caseName {
	case summaryUpdateCaseName, summaryTruncationCaseName:
		return replaytest.CapabilitySummary, true
	default:
		return "", false
	}
}

func TestLoadRequiredExternalBackendsReportsMissingVariables(t *testing.T) {
	backends, missing := loadRequiredExternalBackends(
		externalBackendFactories(), func(string) string { return "" },
	)
	if len(backends) != 0 || len(missing) != expectedExternalBackendCount {
		t.Fatalf("backends = %d, missing = %v", len(backends), missing)
	}
}
