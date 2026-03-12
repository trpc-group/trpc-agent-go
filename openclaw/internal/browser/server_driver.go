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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const browserServerTimeout = 60 * time.Second

type serverProfileDriver struct {
	baseURL string
	token   string
	profile string
	client  *http.Client
}

func newServerProfileDriver(
	baseURL string,
	token string,
	profile string,
) *serverProfileDriver {
	return &serverProfileDriver{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		profile: strings.TrimSpace(profile),
		client: &http.Client{
			Timeout: browserServerTimeout,
		},
	}
}

func (d *serverProfileDriver) Start(
	ctx context.Context,
) (driverStatus, error) {
	if _, err := d.request(ctx, http.MethodPost, "/start", nil); err != nil {
		return driverStatus{}, err
	}
	return d.Status(ctx)
}

func (d *serverProfileDriver) Status(
	ctx context.Context,
) (driverStatus, error) {
	body, err := d.request(ctx, http.MethodGet, "/profiles", nil)
	if err != nil {
		return driverStatus{}, err
	}

	var payload struct {
		Profiles []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Tabs  int    `json:"tabs"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return driverStatus{}, fmt.Errorf(
			"decode browser server status: %w",
			err,
		)
	}
	for i := range payload.Profiles {
		if payload.Profiles[i].Name == d.profile {
			return driverStatus{
				State:     payload.Profiles[i].State,
				ToolCount: payload.Profiles[i].Tabs,
			}, nil
		}
	}
	return driverStatus{State: stateStopped}, nil
}

func (d *serverProfileDriver) Stop() error {
	_, err := d.request(context.Background(), http.MethodPost, "/stop", nil)
	return err
}

func (d *serverProfileDriver) Call(
	ctx context.Context,
	toolName string,
	args map[string]any,
) (any, error) {
	method := http.MethodPost
	path := "/act"
	body := map[string]any{}

	switch toolName {
	case mcpToolTabs:
		action, _ := args["action"].(string)
		switch action {
		case tabActionList:
			method = http.MethodGet
			path = "/tabs"
			body = nil
		case tabActionNew:
			path = "/tabs/open"
			body = map[string]any{
				"url": stringValue(args["url"]),
			}
		case tabActionSelect:
			path = "/tabs/focus"
			body = map[string]any{
				"targetId": formatTargetID(numberValue(args["index"])),
			}
		case tabActionClose:
			targetID := ""
			if index := numberValue(args["index"]); index > 0 {
				targetID = formatTargetID(index)
			}
			if targetID == "" {
				path = "/act"
				body = map[string]any{
					"request": map[string]any{
						"kind": actClose,
					},
				}
				break
			}
			method = http.MethodDelete
			path = "/tabs/" + url.PathEscape(targetID)
			body = nil
		default:
			return nil, fmt.Errorf(
				"unsupported browser tabs action %q",
				action,
			)
		}
	case mcpToolSnapshot:
		path = "/snapshot"
		body = args
	case mcpToolScreenshot:
		path = "/screenshot"
		body = args
	case mcpToolNavigate:
		path = "/navigate"
		body = args
	case mcpToolConsole:
		path = "/console"
		body = args
	case mcpToolCookies:
		method = http.MethodGet
		path = "/cookies"
		body = args
	case mcpToolCookiesSet:
		path = "/cookies/set"
		body = args
	case mcpToolCookiesClear:
		path = "/cookies/clear"
		body = args
	case mcpToolStorageGet:
		method = http.MethodGet
		switch stringValue(args["kind"]) {
		case "session":
			path = "/storage/session"
		default:
			path = "/storage/local"
		}
		body = args
	case mcpToolStorageSet:
		switch stringValue(args["kind"]) {
		case "session":
			path = "/storage/session/set"
		default:
			path = "/storage/local/set"
		}
		body = args
	case mcpToolStorageClear:
		switch stringValue(args["kind"]) {
		case "session":
			path = "/storage/session/clear"
		default:
			path = "/storage/local/clear"
		}
		body = args
	case mcpToolPDF:
		path = "/pdf"
		body = args
	case mcpToolDownload:
		path = "/download"
		body = args
	case mcpToolWaitDownload:
		path = "/wait/download"
		body = args
	case mcpToolUpload:
		path = "/upload"
		body = args
	case mcpToolDialog:
		path = "/dialog"
		body = args
	case mcpToolSetOffline:
		path = "/set/offline"
		body = args
	case mcpToolSetHeaders:
		path = "/set/headers"
		body = args
	case mcpToolSetCreds:
		path = "/set/credentials"
		body = args
	case mcpToolSetGeo:
		path = "/set/geolocation"
		body = args
	case mcpToolSetMedia:
		path = "/set/media"
		body = args
	case mcpToolSetTZ:
		path = "/set/timezone"
		body = args
	case mcpToolSetLocale:
		path = "/set/locale"
		body = args
	case mcpToolSetDevice:
		path = "/set/device"
		body = args
	case mcpToolClick:
		body = map[string]any{"request": mapActArgs(actClick, args)}
	case mcpToolType:
		body = map[string]any{"request": mapActArgs(actType, args)}
	case mcpToolHover:
		body = map[string]any{"request": mapActArgs(actHover, args)}
	case mcpToolSelect:
		body = map[string]any{"request": mapActArgs(actSelect, args)}
	case mcpToolPressKey:
		body = map[string]any{"request": mapActArgs(actPress, args)}
	case mcpToolResize:
		body = map[string]any{"request": mapActArgs(actResize, args)}
	case mcpToolScroll:
		body = map[string]any{
			"request": mapActArgs(actScrollIntoView, args),
		}
	case mcpToolEvaluate:
		body = map[string]any{"request": mapActArgs(actEvaluate, args)}
	case mcpToolWait:
		body = map[string]any{"request": mapActArgs(actWait, args)}
	case mcpToolFillForm:
		body = map[string]any{"request": mapActArgs(actFill, args)}
	case mcpToolDrag:
		body = map[string]any{"request": mapActArgs(actDrag, args)}
	default:
		return nil, fmt.Errorf(
			"unsupported browser server tool %q",
			toolName,
		)
	}

	raw, err := d.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode browser server payload: %w", err)
	}
	return payload, nil
}

func mapActArgs(kind string, args map[string]any) map[string]any {
	out := map[string]any{"kind": kind}
	for key, value := range args {
		out[key] = value
	}
	return out
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func numberValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		number, _ := strconv.Atoi(v.String())
		return number
	default:
		return 0
	}
}

func (d *serverProfileDriver) request(
	ctx context.Context,
	method string,
	path string,
	body any,
) ([]byte, error) {
	fullURL := d.baseURL + path
	if method == http.MethodGet || method == http.MethodDelete {
		values := url.Values{}
		if d.profile != "" {
			values.Set("profile", d.profile)
		}
		for key, value := range queryArgs(body) {
			values.Set(key, value)
		}
		if strings.Contains(fullURL, "?") {
			fullURL += "&" + values.Encode()
		} else {
			fullURL += "?" + values.Encode()
		}
	}

	var reader io.Reader
	if method != http.MethodGet && method != http.MethodDelete {
		payload := map[string]any{
			"profile": d.profile,
		}
		if bodyMap, ok := body.(map[string]any); ok {
			for key, value := range bodyMap {
				payload[key] = value
			}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal browser server request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return nil, err
	}
	if method != http.MethodGet && method != http.MethodDelete {
		req.Header.Set("Content-Type", "application/json")
	}
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call browser server: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf(
			"browser server %s %s failed: %s",
			method,
			path,
			strings.TrimSpace(string(data)),
		)
	}
	return data, nil
}

func queryArgs(body any) map[string]string {
	args, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	values := make(map[string]string)
	for key, value := range args {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			continue
		}
		if text == "<nil>" {
			continue
		}
		values[key] = text
	}
	return values
}
