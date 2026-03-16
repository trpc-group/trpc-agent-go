//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServerProfileDriver_StatusReadsProfilesPayload(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/profiles", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"profiles": []map[string]any{{
				"name":  "openclaw",
				"state": stateReady,
				"tabs":  2,
			}},
		})
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "secret", defaultProfileName)
	status, err := drv.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, stateReady, status.State)
	require.Equal(t, 2, status.ToolCount)
}

func TestServerProfileDriver_CallTabsListUsesQueryProfile(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/tabs", r.URL.Path)
		require.Equal(t, defaultProfileName, r.URL.Query().Get("profile"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "ok",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
	raw, err := drv.Call(context.Background(), mcpToolTabs, map[string]any{
		"action": tabActionList,
	})
	require.NoError(t, err)

	body := raw.(map[string]any)
	require.NotNil(t, body["content"])
}

func TestServerProfileDriver_CallWaitWrapsActRequest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/act", r.URL.Path)

		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, defaultProfileName, payload["profile"])

		request := payload["request"].(map[string]any)
		require.Equal(t, actWait, request["kind"])
		require.Equal(t, "selector", request["selector"])
		require.Equal(t, float64(1.5), request["time"])

		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "waited",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
	raw, err := drv.Call(context.Background(), mcpToolWait, map[string]any{
		"time":     1.5,
		"selector": "selector",
	})
	require.NoError(t, err)

	body := raw.(map[string]any)
	require.NotNil(t, body["content"])
}

func TestServerProfileDriver_StartAndStop(t *testing.T) {
	t.Parallel()

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/start":
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, defaultProfileName, payload["profile"])
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/stop":
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, defaultProfileName, payload["profile"])
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/profiles":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"profiles": []map[string]any{{
					"name":  defaultProfileName,
					"state": stateReady,
					"tabs":  1,
				}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
	status, err := drv.Start(context.Background())
	require.NoError(t, err)
	require.Equal(t, stateReady, status.State)

	require.NoError(t, drv.Stop())
	require.Equal(
		t,
		[]string{
			"POST /start",
			"GET /profiles",
			"POST /stop",
		},
		calls,
	)
}

func TestServerProfileDriver_CallRoutesBrowserActions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		toolName   string
		args       map[string]any
		wantPath   string
		wantMethod string
		assertBody func(*testing.T, map[string]any)
	}{
		{
			name:       "snapshot",
			toolName:   mcpToolSnapshot,
			args:       map[string]any{"filename": "page.txt"},
			wantPath:   "/snapshot",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "page.txt", body["filename"])
			},
		},
		{
			name:     "snapshot advanced",
			toolName: mcpToolSnapshot,
			args: map[string]any{
				"mode":           "efficient",
				"compact":        true,
				"depth":          4,
				"selector":       "#main",
				"frame":          "iframe#main",
				"labels":         true,
				"refs":           "role",
				"interactive":    true,
				"snapshotFormat": "role",
			},
			wantPath:   "/snapshot",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "efficient", body["mode"])
				require.Equal(t, true, body["compact"])
				require.Equal(t, float64(4), body["depth"])
				require.Equal(t, "#main", body["selector"])
				require.Equal(t, "iframe#main", body["frame"])
				require.Equal(t, true, body["labels"])
				require.Equal(t, "role", body["refs"])
				require.Equal(t, true, body["interactive"])
				require.Equal(t, "role", body["snapshotFormat"])
			},
		},
		{
			name:       "screenshot",
			toolName:   mcpToolScreenshot,
			args:       map[string]any{"type": "png"},
			wantPath:   "/screenshot",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "png", body["type"])
			},
		},
		{
			name:       "navigate",
			toolName:   mcpToolNavigate,
			args:       map[string]any{"url": "https://example.com"},
			wantPath:   "/navigate",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "https://example.com", body["url"])
			},
		},
		{
			name:       "console",
			toolName:   mcpToolConsole,
			args:       map[string]any{"level": "error"},
			wantPath:   "/console",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "error", body["level"])
			},
		},
		{
			name:       "cookies",
			toolName:   mcpToolCookies,
			args:       map[string]any{"targetId": "tab-1"},
			wantPath:   "/cookies",
			wantMethod: http.MethodGet,
		},
		{
			name:       "cookies set",
			toolName:   mcpToolCookiesSet,
			args:       map[string]any{"cookie": map[string]any{"name": "sid"}},
			wantPath:   "/cookies/set",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				cookie := body["cookie"].(map[string]any)
				require.Equal(t, "sid", cookie["name"])
			},
		},
		{
			name:       "cookies clear",
			toolName:   mcpToolCookiesClear,
			args:       map[string]any{"targetId": "tab-1"},
			wantPath:   "/cookies/clear",
			wantMethod: http.MethodPost,
		},
		{
			name:       "storage get",
			toolName:   mcpToolStorageGet,
			args:       map[string]any{"kind": "session", "key": "token"},
			wantPath:   "/storage/session",
			wantMethod: http.MethodGet,
		},
		{
			name:       "storage set",
			toolName:   mcpToolStorageSet,
			args:       map[string]any{"kind": "local", "key": "token", "value": "x"},
			wantPath:   "/storage/local/set",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "local", body["kind"])
				require.Equal(t, "token", body["key"])
				require.Equal(t, "x", body["value"])
			},
		},
		{
			name:       "storage clear",
			toolName:   mcpToolStorageClear,
			args:       map[string]any{"kind": "session"},
			wantPath:   "/storage/session/clear",
			wantMethod: http.MethodPost,
		},
		{
			name:       "credentials",
			toolName:   mcpToolSetCreds,
			args:       map[string]any{"username": "u", "password": "p"},
			wantPath:   "/set/credentials",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "u", body["username"])
				require.Equal(t, "p", body["password"])
			},
		},
		{
			name:     "geolocation",
			toolName: mcpToolSetGeo,
			args: map[string]any{
				"latitude":  37.7,
				"longitude": -122.4,
				"origin":    "https://example.com",
			},
			wantPath:   "/set/geolocation",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, 37.7, body["latitude"])
				require.Equal(t, -122.4, body["longitude"])
				require.Equal(
					t,
					"https://example.com",
					body["origin"],
				)
			},
		},
		{
			name:       "media",
			toolName:   mcpToolSetMedia,
			args:       map[string]any{"colorScheme": "dark"},
			wantPath:   "/set/media",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "dark", body["colorScheme"])
			},
		},
		{
			name:       "pdf",
			toolName:   mcpToolPDF,
			args:       map[string]any{"filename": "page.pdf"},
			wantPath:   "/pdf",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "page.pdf", body["filename"])
			},
		},
		{
			name:     "download",
			toolName: mcpToolDownload,
			args: map[string]any{
				"ref":       "e1",
				"path":      "report.pdf",
				"timeoutMs": 2500,
			},
			wantPath:   "/download",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "e1", body["ref"])
				require.Equal(t, "report.pdf", body["path"])
				require.Equal(t, float64(2500), body["timeoutMs"])
			},
		},
		{
			name:     "wait download",
			toolName: mcpToolWaitDownload,
			args: map[string]any{
				"path":      "report.pdf",
				"timeoutMs": 2500,
			},
			wantPath:   "/wait/download",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "report.pdf", body["path"])
				require.Equal(t, float64(2500), body["timeoutMs"])
			},
		},
		{
			name:     "upload",
			toolName: mcpToolUpload,
			args: map[string]any{
				"paths":    []string{"/tmp/a.txt"},
				"inputRef": "file-input",
			},
			wantPath:   "/upload",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, []any{"/tmp/a.txt"}, body["paths"])
				require.Equal(t, "file-input", body["inputRef"])
			},
		},
		{
			name:     "upload with ref",
			toolName: mcpToolUpload,
			args: map[string]any{
				"paths":     []string{"/tmp/a.txt"},
				"ref":       "e1",
				"timeoutMs": 2500,
			},
			wantPath:   "/upload",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, []any{"/tmp/a.txt"}, body["paths"])
				require.Equal(t, "e1", body["ref"])
				require.Equal(t, float64(2500), body["timeoutMs"])
			},
		},
		{
			name:       "dialog",
			toolName:   mcpToolDialog,
			args:       map[string]any{"accept": true},
			wantPath:   "/dialog",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, true, body["accept"])
			},
		},
		{
			name:       "offline",
			toolName:   mcpToolSetOffline,
			args:       map[string]any{"offline": true},
			wantPath:   "/set/offline",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, true, body["offline"])
			},
		},
		{
			name:       "headers",
			toolName:   mcpToolSetHeaders,
			args:       map[string]any{"headers": map[string]string{"X-Test": "1"}},
			wantPath:   "/set/headers",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				headers := body["headers"].(map[string]any)
				require.Equal(t, "1", headers["X-Test"])
			},
		},
		{
			name:       "timezone",
			toolName:   mcpToolSetTZ,
			args:       map[string]any{"timezoneId": "Asia/Shanghai"},
			wantPath:   "/set/timezone",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "Asia/Shanghai", body["timezoneId"])
			},
		},
		{
			name:       "locale",
			toolName:   mcpToolSetLocale,
			args:       map[string]any{"locale": "en-US"},
			wantPath:   "/set/locale",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "en-US", body["locale"])
			},
		},
		{
			name:       "device",
			toolName:   mcpToolSetDevice,
			args:       map[string]any{"name": "iPhone 15"},
			wantPath:   "/set/device",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				require.Equal(t, "iPhone 15", body["name"])
			},
		},
		{
			name:       "click",
			toolName:   mcpToolClick,
			args:       map[string]any{"ref": "e1"},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actClick, request["kind"])
				require.Equal(t, "e1", request["ref"])
			},
		},
		{
			name:       "hover",
			toolName:   mcpToolHover,
			args:       map[string]any{"ref": "e1"},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actHover, request["kind"])
				require.Equal(t, "e1", request["ref"])
			},
		},
		{
			name:       "scroll",
			toolName:   mcpToolScroll,
			args:       map[string]any{"ref": "e2"},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actScrollIntoView, request["kind"])
				require.Equal(t, "e2", request["ref"])
			},
		},
		{
			name:       "fill form",
			toolName:   mcpToolFillForm,
			args:       map[string]any{"fields": []string{"email"}},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actFill, request["kind"])
				require.Equal(t, []any{"email"}, request["fields"])
			},
		},
		{
			name:       "drag",
			toolName:   mcpToolDrag,
			args:       map[string]any{"startRef": "a", "endRef": "b"},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actDrag, request["kind"])
				require.Equal(t, "a", request["startRef"])
				require.Equal(t, "b", request["endRef"])
			},
		},
		{
			name:       "resize",
			toolName:   mcpToolResize,
			args:       map[string]any{"width": 1280, "height": 720},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actResize, request["kind"])
				require.Equal(t, float64(1280), request["width"])
			},
		},
		{
			name:       "evaluate",
			toolName:   mcpToolEvaluate,
			args:       map[string]any{"function": "() => 1"},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertBody: func(t *testing.T, body map[string]any) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actEvaluate, request["kind"])
				require.Equal(t, "() => 1", request["function"])
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				r *http.Request,
			) {
				require.Equal(t, tc.wantMethod, r.Method)
				require.Equal(t, tc.wantPath, r.URL.Path)

				payload := map[string]any{}
				if r.Method == http.MethodGet ||
					r.Method == http.MethodDelete {
					for key, values := range r.URL.Query() {
						if len(values) == 0 {
							continue
						}
						payload[key] = values[len(values)-1]
					}
				} else {
					require.NoError(
						t,
						json.NewDecoder(r.Body).Decode(&payload),
					)
				}
				require.Equal(
					t,
					defaultProfileName,
					payload["profile"],
				)
				if tc.assertBody != nil {
					tc.assertBody(t, payload)
				}

				_ = json.NewEncoder(w).Encode(map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": "ok",
					}},
				})
			}))
			t.Cleanup(srv.Close)

			drv := newServerProfileDriver(
				srv.URL,
				"secret",
				defaultProfileName,
			)
			raw, err := drv.Call(
				context.Background(),
				tc.toolName,
				tc.args,
			)
			require.NoError(t, err)

			body := raw.(map[string]any)
			require.NotNil(t, body["content"])
		})
	}
}

func TestServerProfileDriver_CallTabActions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		args       map[string]any
		wantPath   string
		wantMethod string
		assertReq  func(*testing.T, *http.Request, map[string]any)
	}{
		{
			name: "open",
			args: map[string]any{
				"action": tabActionNew,
				"url":    "https://example.com",
			},
			wantPath:   "/tabs/open",
			wantMethod: http.MethodPost,
			assertReq: func(t *testing.T, r *http.Request, body map[string]any) {
				t.Helper()
				require.Equal(t, "https://example.com", body["url"])
			},
		},
		{
			name: "focus",
			args: map[string]any{
				"action": tabActionSelect,
				"index":  7,
			},
			wantPath:   "/tabs/focus",
			wantMethod: http.MethodPost,
			assertReq: func(t *testing.T, r *http.Request, body map[string]any) {
				t.Helper()
				require.Equal(t, "tab-7", body["targetId"])
			},
		},
		{
			name: "close",
			args: map[string]any{
				"action": tabActionClose,
				"index":  7,
			},
			wantPath:   "/tabs/tab-7",
			wantMethod: http.MethodDelete,
			assertReq: func(t *testing.T, r *http.Request, body map[string]any) {
				t.Helper()
				require.Equal(
					t,
					defaultProfileName,
					r.URL.Query().Get("profile"),
				)
				require.Nil(t, body)
			},
		},
		{
			name: "close current tab",
			args: map[string]any{
				"action": tabActionClose,
			},
			wantPath:   "/act",
			wantMethod: http.MethodPost,
			assertReq: func(
				t *testing.T,
				r *http.Request,
				body map[string]any,
			) {
				t.Helper()
				request := body["request"].(map[string]any)
				require.Equal(t, actClose, request["kind"])
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				r *http.Request,
			) {
				require.Equal(t, tc.wantMethod, r.Method)
				require.Equal(t, tc.wantPath, r.URL.Path)

				var payload map[string]any
				if r.Body != nil && tc.wantMethod == http.MethodPost {
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				}
				tc.assertReq(t, r, payload)

				_ = json.NewEncoder(w).Encode(map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": "ok",
					}},
				})
			}))
			t.Cleanup(srv.Close)

			drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
			raw, err := drv.Call(
				context.Background(),
				mcpToolTabs,
				tc.args,
			)
			require.NoError(t, err)

			body := raw.(map[string]any)
			require.NotNil(t, body["content"])
		})
	}
}

func TestServerProfileDriver_ErrorPaths(t *testing.T) {
	t.Parallel()

	drv := newServerProfileDriver("http://127.0.0.1:1", "", defaultProfileName)
	_, err := drv.Call(context.Background(), "unknown_tool", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported browser server tool")

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	drv = newServerProfileDriver(srv.URL, "", defaultProfileName)
	_, err = drv.Status(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed")
}

func TestServerProfileDriver_CallRejectsBadJSONPayload(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write([]byte("{"))
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
	_, err := drv.Call(context.Background(), mcpToolSnapshot, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode browser server payload")
}

func TestServerProfileDriver_HelperConversions(t *testing.T) {
	t.Parallel()

	require.Equal(t, "value", stringValue(" value "))
	require.Empty(t, stringValue(1))
	require.Equal(t, 3, numberValue(3))
	require.Equal(t, 7, numberValue(float64(7)))
	require.Equal(t, 9, numberValue(json.Number("9")))
	require.Zero(t, numberValue("bad"))

	require.Equal(t, map[string]any{
		"kind": actClick,
		"ref":  "e1",
	}, mapActArgs(actClick, map[string]any{"ref": "e1"}))
	require.Nil(t, queryArgs(nil))
	require.Equal(t, map[string]string{
		"ref": "e1",
	}, queryArgs(map[string]any{
		"ref":   "e1",
		"empty": " ",
		"nil":   nil,
	}))
}

func TestServerProfileDriver_StatusReturnsStoppedWhenMissing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"profiles": []map[string]any{{
				"name": "chrome",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
	status, err := drv.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, stateStopped, status.State)
}

func TestServerProfileDriver_StatusRejectsBadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write([]byte("{"))
	}))
	t.Cleanup(srv.Close)

	drv := newServerProfileDriver(srv.URL, "", defaultProfileName)
	_, err := drv.Status(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode browser server status")
}
