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
	"os"
	"strings"
	"testing"
)

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
