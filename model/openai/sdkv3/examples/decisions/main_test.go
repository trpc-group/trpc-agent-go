//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDecider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "http://localhost/v1")
	t.Setenv("SYSTEMONE_BASE_URL", "http://localhost")
	for _, backend := range []string{"openai", "systemone"} {
		client, err := newDecider(backend, "")
		if err != nil {
			t.Fatalf("newDecider(%q): %v", backend, err)
		}
		if client == nil {
			t.Fatalf("newDecider(%q) returned a nil client", backend)
		}
	}
	if _, err := newDecider("unknown", ""); err == nil {
		t.Fatal("expected an error for an unknown backend")
	}
	t.Setenv("SYSTEMONE_BASE_URL", "")
	if _, err := newDecider("systemone", ""); err == nil {
		t.Fatal("expected an error without SYSTEMONE_BASE_URL")
	}
}

func TestRunClassifiesTicket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"model":"test-model","answers":{"topic":{"type":"choice","choice":"shipping","confidence":1,"probabilities":{"shipping":1,"billing":0,"other":0}}}}`)
	}))
	defer server.Close()
	t.Setenv("SYSTEMONE_BASE_URL", server.URL)
	t.Setenv("SYSTEMONE_API_KEY", "")
	var out bytes.Buffer
	require.NoError(t, run(flag.NewFlagSet("decisions", flag.ContinueOnError), []string{"-backend", "systemone", "-model", "test-model"}, &out))
	require.Contains(t, out.String(), `status=answered choice="shipping"`)
}

func TestRunBackendError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("SYSTEMONE_BASE_URL", server.URL)
	t.Setenv("SYSTEMONE_API_KEY", "")
	var out bytes.Buffer
	err := run(flag.NewFlagSet("decisions", flag.ContinueOnError), []string{"-backend", "systemone"}, &out)
	require.ErrorContains(t, err, "503")
	require.Empty(t, out.String())
}
