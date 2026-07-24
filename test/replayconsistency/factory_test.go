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
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestExternalBackendFactoryRegistry(t *testing.T) {
	expectedNames := []string{"redis", "postgres", "mysql", "clickhouse"}
	factories := externalBackendFactories()
	if err := validateBackendFactories(factories); err != nil {
		t.Fatalf("validateBackendFactories() error = %v", err)
	}
	if len(factories) != len(expectedNames) {
		t.Fatalf("factory count = %d, want %d", len(factories), len(expectedNames))
	}
	for i, name := range expectedNames {
		if factories[i].Name != name {
			t.Fatalf("factory %d name = %q, want %q", i, factories[i].Name, name)
		}
	}
}

func TestBackendFactoryCreatesIsolatedFixturesWithoutSingleton(t *testing.T) {
	backend := isolatedInMemoryServiceBackend(replaytest.CapabilityTrack)
	first, err := backend.New(context.Background(), "first")
	if err != nil {
		t.Fatalf("create first fixture: %v", err)
	}
	second, err := backend.New(context.Background(), "second")
	if err != nil {
		if closeErr := first.Close(); closeErr != nil {
			t.Errorf("close first fixture: %v", closeErr)
		}
		t.Fatalf("create second fixture: %v", err)
	}
	firstFixture := first.(*replayFixture)
	secondFixture := second.(*replayFixture)
	if firstFixture == secondFixture || firstFixture.sessionService == secondFixture.sessionService ||
		firstFixture.appName == secondFixture.appName || firstFixture.userID == secondFixture.userID {
		t.Fatalf("factory reused fixture state: first=%p second=%p", firstFixture, secondFixture)
	}
	if first.Capabilities().Supports(replaytest.CapabilityTrack) ||
		second.Capabilities().Supports(replaytest.CapabilityTrack) {
		t.Fatal("exact unsupported capability was lost")
	}
	if err := first.Close(); err != nil {
		t.Errorf("close first fixture: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Errorf("close second fixture: %v", err)
	}
}

func TestValidateBackendFactoriesRejectsDuplicateRegistration(t *testing.T) {
	factory := backendFactory{
		Name: "duplicate", Environment: "DUPLICATE_ENV",
		New: func(BackendConfig) replaytest.Backend { return replaytest.Backend{Name: "duplicate"} },
	}
	tests := []struct {
		name      string
		factories []backendFactory
		want      string
	}{
		{name: "name", factories: []backendFactory{
			factory,
			{Name: factory.Name, Environment: "OTHER_ENV", New: factory.New},
		}, want: "name"},
		{name: "environment", factories: []backendFactory{
			factory,
			{Name: "other", Environment: factory.Environment, New: factory.New},
		}, want: "environment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBackendFactories(test.factories)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateBackendFactories() error = %v, want %q", err, test.want)
			}
		})
	}
}

func isolatedInMemoryServiceBackend(
	unsupported ...replaytest.Capability,
) replaytest.Backend {
	return serviceBackend("isolated", func(summarizer *replaySummarizer) (
		session.Service,
		memory.Service,
		error,
	) {
		return sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(summarizer),
		), memoryinmemory.NewMemoryService(), nil
	}, serviceBackendOptions{Unsupported: unsupported})
}
