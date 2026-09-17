//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	openapi "github.com/getkin/kin-openapi/openapi3"
)

func TestURILoaderReadFromURI(t *testing.T) {
	const uri = "custom://spec/openapi.json"
	calls := 0
	var wantContext context.Context
	loader, err := NewURILoader(uri, WithReadFromURI(func(l *openapi.Loader, location *url.URL) ([]byte, error) {
		calls++
		if location.String() != uri {
			t.Errorf("reader URI = %q, want %q", location, uri)
		}
		if l.Context != wantContext {
			t.Error("reader did not receive the current Load context")
		}
		if err := l.Context.Err(); err != nil {
			return nil, err
		}
		return []byte(fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"load-%d","version":"1"},"paths":{}}`, calls)), nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Repeated loads must parse the current reader output, not a cached document.
	for i := 1; i <= 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		wantContext = ctx
		doc, err := loader.Load(ctx)
		cancel()
		if err != nil {
			t.Fatalf("Load %d failed: %v", i, err)
		}
		if calls != i {
			t.Fatalf("reader calls = %d, want %d", calls, i)
		}
		if want := fmt.Sprintf("load-%d", i); doc.Info.Title != want {
			t.Errorf("document title = %q, want %q", doc.Info.Title, want)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantContext = ctx
	if _, err := loader.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Load with canceled context = %v, want context.Canceled", err)
	}
	if calls != 3 {
		t.Errorf("reader calls = %d, want 3", calls)
	}
}

func TestURILoaderExternalRefs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			fmt.Fprint(w, `{
				"openapi":"3.0.3",
				"info":{"title":"external refs","version":"1"},
				"paths":{},
				"components":{"schemas":{"Pet":{"$ref":"schemas.json#/components/schemas/Pet"}}}
			}`)
		case "/schemas.json":
			fmt.Fprint(w, `{"components":{"schemas":{"Pet":{"type":"object","properties":{"name":{"type":"string"}}}}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, tt := range []struct {
		name    string
		opts    []LoaderOption
		wantErr bool
	}{
		{name: "disabled_by_default", wantErr: true},
		{name: "enabled", opts: []LoaderOption{WithExternalRefs(true)}},
		{name: "explicitly_disabled", opts: []LoaderOption{WithExternalRefs(false)}, wantErr: true},
		{name: "last_option_wins", opts: []LoaderOption{WithExternalRefs(true), WithExternalRefs(false)}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			loader, err := NewURILoader(server.URL+"/openapi.json", tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := loader.Load(context.Background())
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "disallowed external reference") {
					t.Fatalf("Load error = %v, want disallowed external reference", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(context.Background()); err != nil {
				t.Fatalf("Validate failed: %v", err)
			}
			pet := doc.Components.Schemas["Pet"].Value
			if pet == nil || pet.Properties["name"] == nil || pet.Properties["name"].Value == nil {
				t.Fatal("external Pet schema was not resolved")
			}
			if !pet.Properties["name"].Value.Type.Is("string") {
				t.Error("resolved Pet.name type is not string")
			}
		})
	}
}

func Test_urlSpecLoader_Load(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for receiver constructor.
		uri     string
		wantErr bool
	}{
		{
			name:    "LoadFromURI_INVALID_URL",
			uri:     "http://localhost:8080",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _ := NewURILoader(tt.uri, WithExternalRefs(true))
			_, gotErr := u.Load(context.Background())
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Load() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Load() succeeded unexpectedly")
			}
		})
	}
}
