//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type hostnameTransport func(*http.Request) (*http.Response, error)

func (f hostnameTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientIDHostSuffix(t *testing.T) {
	for _, domain := range []struct{ name, field, host string }{
		{"missing", "", "configured.test"},
		{"null", `,"domain":null`, "configured.test"},
		{"empty", `,"domain":""`, "configured.test"},
		{"provided", `,"domain":"actual.test"`, "actual.test"},
	} {
		for _, mode := range []struct {
			name     string
			disabled bool
			options  []Option
		}{
			{name: "default"},
			{name: "enabled", options: []Option{WithClientIDHostSuffix(true)}},
			{name: "disabled", disabled: true, options: []Option{WithClientIDHostSuffix(false)}},
			{name: "re-enabled", options: []Option{WithClientIDHostSuffix(false), WithClientIDHostSuffix(true)}},
			{name: "last-disabled", disabled: true, options: []Option{WithClientIDHostSuffix(true), WithClientIDHostSuffix(false)}},
		} {
			for _, reconnect := range []bool{false, true} {
				for _, clientID := range []string{"worker", ""} {
					t.Run(fmt.Sprintf("%s/%s/connect=%t/client=%s", domain.name, mode.name, reconnect, clientID), func(t *testing.T) {
						t.Setenv("E2B_DEBUG", "false")
						t.Setenv("E2B_ACCESS_TOKEN", "")
						id := "sandbox"
						if !mode.disabled && domain.name != "provided" && clientID != "" {
							id += "-" + clientID
						}
						wantHost := "49999-" + id + "." + domain.host
						var executions, setups int
						client := &http.Client{Transport: hostnameTransport(func(r *http.Request) (*http.Response, error) {
							body := ""
							switch r.URL.Path {
							case "/sandboxes", "/sandboxes/sandbox/connect":
								setups++
								assert.Equal(t, http.MethodPost, r.Method)
								wantPath := "/sandboxes"
								if reconnect {
									wantPath += "/sandbox/connect"
								}
								assert.Equal(t, wantPath, r.URL.Path)
								assert.Equal(t, "management.test", r.URL.Host)
								body = fmt.Sprintf(`{"sandboxID":"sandbox","clientID":%q%s}`, clientID, domain.field)
							case "/execute":
								executions++
								assert.Equal(t, wantHost, r.URL.Host)
								assert.Equal(t, wantHost, r.Host)
								body = "{\"type\":\"result\",\"text\":\"ok\",\"is_main_result\":true}\n"
							default:
								t.Errorf("unexpected request: %s %s", r.Method, r.URL)
							}
							return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
						})}
						opts := []Option{WithAPIKey("key"), WithAPIURL("https://management.test"), WithDomain("configured.test"), WithHTTPClient(client)}
						opts = append(opts, mode.options...)
						if reconnect {
							opts = append(opts, WithSandboxID("sandbox"))
						}
						executor, err := NewWithContext(context.Background(), opts...)
						require.NoError(t, err)
						assert.Equal(t, "sandbox", executor.SandboxID())
						assert.Equal(t, clientID, executor.Sandbox().ClientID())
						assert.Equal(t, "3000-"+id+"."+domain.host, executor.Sandbox().GetHost(3000))
						_, err = executor.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{CodeBlocks: []codeexecutor.CodeBlock{{Code: "1", Language: "python"}}})
						require.NoError(t, err)
						assert.Equal(t, 1, setups)
						assert.Equal(t, 1, executions)
					})
				}
			}
		}
	}
}
