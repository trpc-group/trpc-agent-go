//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace/noop"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func TestNewResourceUsesDefaultServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	res, err := newResource(context.Background())
	if err != nil {
		t.Fatalf("newResource() error = %v", err)
	}
	serviceName, ok := res.Set().Value(semconv.ServiceNameKey)
	if !ok {
		t.Fatal("service.name should be set by default")
	}
	defaultServiceName, ok := resource.Default().Set().Value(semconv.ServiceNameKey)
	if !ok {
		t.Fatal("OpenTelemetry default resource does not contain service.name")
	}
	if serviceName.AsString() != defaultServiceName.AsString() {
		t.Fatalf("service.name = %q, want %q", serviceName.AsString(), defaultServiceName.AsString())
	}
	if !strings.HasPrefix(serviceName.AsString(), "unknown_service:") {
		t.Fatalf("service.name = %q, want unknown_service fallback", serviceName.AsString())
	}
	if _, ok := res.Set().Value(semconv.ServiceNamespaceKey); ok {
		t.Fatal("service.namespace should be unset by default")
	}
	if _, ok := res.Set().Value(semconv.ServiceVersionKey); ok {
		t.Fatal("service.version should be unset by default")
	}
}

func TestEncodeAuth(t *testing.T) {
	pk := "public"
	sk := "secret"
	got := encodeAuth(pk, sk)
	want := base64.StdEncoding.EncodeToString([]byte(pk + ":" + sk))
	if got != want {
		t.Fatalf("encodeAuth() = %q, want %q", got, want)
	}
}

func TestStart_MissingConfig(t *testing.T) {
	ctx := context.Background()

	// Ensure env is clean so Start uses empty defaults
	_ = os.Unsetenv("LANGFUSE_SECRET_KEY")
	_ = os.Unsetenv("LANGFUSE_PUBLIC_KEY")
	_ = os.Unsetenv("LANGFUSE_HOST")
	_ = os.Unsetenv("LANGFUSE_INSECURE")
	_ = os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")

	t.Run("all missing", func(t *testing.T) {
		clean, err := Start(ctx)
		if err == nil {
			_ = clean(ctx)
			t.Fatalf("Start() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "must be provided") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing host", func(t *testing.T) {
		clean, err := Start(ctx, WithPublicKey("pk"), WithSecretKey("sk"))
		if err == nil {
			_ = clean(ctx)
			t.Fatalf("Start() expected error for missing host, got nil")
		}
	})

	t.Run("missing keys", func(t *testing.T) {
		clean, err := Start(ctx, WithHost("cloud.langfuse.com:443"))
		if err == nil {
			_ = clean(ctx)
			t.Fatalf("Start() expected error for missing keys, got nil")
		}
	})
}

func TestStart_WithObservationLeafValueMaxBytesOption(t *testing.T) {
	ctx := context.Background()

	old := getObservationMaxBytes()
	defer func() {
		if old < 0 {
			setObservationMaxBytes(nil)
			return
		}
		ov := old
		setObservationMaxBytes(&ov)
	}()

	// We don't need a successful Start to verify the option wiring.
	_, err := Start(ctx, WithObservationLeafValueMaxBytes(1024))
	if err == nil {
		t.Fatalf("Start() expected error due to missing config, got nil")
	}

	in := strings.Repeat("a", 2000)
	out := truncateObservationValue(in)
	if len([]byte(out)) > 1024 {
		t.Fatalf("truncateObservationValue() got %d bytes, want <= %d", len([]byte(out)), 1024)
	}
}

func TestStart_RejectsUnsupportedIngestionVersion(t *testing.T) {
	_, err := Start(context.Background(), WithHost("localhost:3000"), WithPublicKey("pk"), WithSecretKey("sk"), WithIngestionVersion("5"))
	if err == nil || !strings.Contains(err.Error(), "unsupported ingestion version") {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestStart_SendsIngestionVersionHeader(t *testing.T) {
	var (
		mu      sync.Mutex
		headers []http.Header
		paths   []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Clone())
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	oldProvider, oldTracer := atrace.TracerProvider, atrace.Tracer
	atrace.TracerProvider = noop.NewTracerProvider()
	defer func() { atrace.TracerProvider, atrace.Tracer = oldProvider, oldTracer }()

	clean, err := Start(context.Background(), WithHost(host.Host), WithInsecure(),
		WithPublicKey("pk"), WithSecretKey("sk"), WithIngestionVersion("4"))
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, span := atrace.Tracer.Start(context.Background(), "work")
	span.End()
	if err := clean(context.Background()); err != nil {
		t.Fatalf("shutdown error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(headers) == 0 {
		t.Fatal("no export reached the server")
	}
	for i, h := range headers {
		if got := h.Get("x-langfuse-ingestion-version"); got != "4" {
			t.Errorf("x-langfuse-ingestion-version = %q, want 4", got)
		}
		if paths[i] != "/api/public/otel/v1/traces" {
			t.Errorf("path = %q", paths[i])
		}
	}
}
