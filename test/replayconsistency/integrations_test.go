//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestIntegrationFactoriesReportMissingEnvironment(t *testing.T) {
	for _, name := range []string{
		EnvRedisURL,
		EnvPostgresDSN,
		EnvMySQLDSN,
		EnvClickHouseDSN,
	} {
		t.Setenv(name, "")
	}
	factories := IntegrationFactoriesFromEnvironment()
	require.Len(t, factories, 4)
	for _, factory := range factories {
		require.Nil(t, factory.Open)
		require.NotEmpty(t, factory.DisabledReason)
	}
}

func TestOptionalIntegrationReplay(t *testing.T) {
	factories := []replaytest.BackendFactory{
		LightweightFactories()[0],
	}
	for _, factory := range IntegrationFactoriesFromEnvironment() {
		if factory.Open != nil {
			factories = append(factories, factory)
		}
	}
	if len(factories) == 1 {
		t.Skip("no optional replay backend environment variables are set")
	}

	runner := replaytest.Runner{
		BaselineBackend: "inmemory",
		Backends:        factories,
	}
	result, err := runner.Run(
		context.Background(),
		replaytest.StandardCases(),
	)
	require.NoError(t, err)
	require.Zero(t, result.Report.Summary.Failed)
	require.Zero(t, result.Report.Summary.DisallowedDiffs)
}

func TestAllFactoriesIncludeDisabledBackends(t *testing.T) {
	if os.Getenv(EnvRedisURL) != "" ||
		os.Getenv(EnvPostgresDSN) != "" ||
		os.Getenv(EnvMySQLDSN) != "" ||
		os.Getenv(EnvClickHouseDSN) != "" {
		t.Skip("environment has enabled integration backends")
	}
	require.Len(t, AllFactoriesFromEnvironment(), 6)
}
