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
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	toolpkg "trpc.group/trpc-go/trpc-agent-go/tool"
)

const testPNG = "image/png"

type fakeDriver struct {
	startStatus driverStatus
	startErr    error
	status      driverStatus
	statusErr   error
	stopErr     error
	callResult  map[string]any
	callErr     error
	calls       []fakeCall
}

type fakeCall struct {
	Tool string
	Args map[string]any
}

type lookupCallableTool struct {
	name string
}

func (t lookupCallableTool) Declaration() *toolpkg.Declaration {
	return &toolpkg.Declaration{Name: t.name}
}

func (t lookupCallableTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	return nil, nil
}

type lookupDeclTool struct {
	name string
}

func (t lookupDeclTool) Declaration() *toolpkg.Declaration {
	return &toolpkg.Declaration{Name: t.name}
}

func (f *fakeDriver) Start(ctx context.Context) (driverStatus, error) {
	if f.startErr != nil {
		return driverStatus{}, f.startErr
	}
	if f.startStatus.State == "" {
		return driverStatus{State: stateReady, ToolCount: 1}, nil
	}
	return f.startStatus, nil
}

func (f *fakeDriver) Status(ctx context.Context) (driverStatus, error) {
	if f.statusErr != nil {
		return driverStatus{}, f.statusErr
	}
	if f.status.State == "" {
		return driverStatus{State: stateStopped}, nil
	}
	return f.status, nil
}

func (f *fakeDriver) Stop() error { return f.stopErr }

func (f *fakeDriver) Call(
	ctx context.Context,
	toolName string,
	args map[string]any,
) (any, error) {
	f.calls = append(f.calls, fakeCall{
		Tool: toolName,
		Args: args,
	})
	if f.callErr != nil {
		return nil, f.callErr
	}
	if raw, ok := f.callResult[toolName]; ok {
		return raw, nil
	}
	return []map[string]any{{
		"type": "text",
		"text": "ok",
	}}, nil
}

func TestToolCall_SnapshotWrapsUntrustedText(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionSnapshot}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.True(t, got.Untrusted)
	require.Equal(t, driverTypePlaywrightMCP, got.Driver)
	require.Contains(t, got.Text, untrustedBrowserWarning)
	require.Contains(t, got.Text, "ok")
	require.Len(t, drv.calls, 1)
	require.Equal(t, mcpToolSnapshot, drv.calls[0].Tool)
}

func TestToolCall_ActClickSelectsRequestedTab(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	_, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action":   actionAct,
			"targetId": "tab-2",
			"request": map[string]any{
				"kind": actClick,
				"ref":  "e12",
			},
		}),
	)
	require.NoError(t, err)
	require.Len(t, drv.calls, 2)
	require.Equal(t, mcpToolTabs, drv.calls[0].Tool)
	require.Equal(t, tabActionSelect, drv.calls[0].Args["action"])
	require.Equal(t, 2, drv.calls[0].Args["index"])
	require.Equal(t, mcpToolClick, drv.calls[1].Tool)
	require.Equal(t, "e12", drv.calls[1].Args["ref"])
	require.Equal(t, "element e12", drv.calls[1].Args["element"])
}

func TestToolCall_ActEvaluateDisabled(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	_, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind": actEvaluate,
				"fn":   "() => 1",
			},
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
}

func TestToolCall_ProfilesAreSorted(t *testing.T) {
	t.Parallel()

	openclawDriver := &fakeDriver{
		status: driverStatus{State: stateReady, ToolCount: 3},
	}
	chromeDriver := &fakeDriver{
		status: driverStatus{State: stateStopped},
	}

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			"chrome": {
				Name: "chrome",
			},
			defaultProfileName: {
				Name: defaultProfileName,
			},
		},
		map[string]driver{
			"chrome":           chromeDriver,
			defaultProfileName: openclawDriver,
		},
	)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionProfiles}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Len(t, got.Profiles, 2)
	require.Equal(t, ToolName, got.Driver)
	require.Equal(t, "chrome", got.Profiles[0].Name)
	require.Equal(t, defaultProfileName, got.Profiles[1].Name)
	require.Equal(t, driverTypePlaywrightMCP, got.Profiles[0].Driver)
	require.Equal(t, driverTypePlaywrightMCP, got.Profiles[1].Driver)
}

func TestToolCall_ScreenshotPreservesContent(t *testing.T) {
	t.Parallel()

	image := map[string]any{
		"type":     "image",
		"data":     "ZmFrZQ==",
		"mimeType": testPNG,
	}
	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolScreenshot: []map[string]any{image},
		},
	}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionScreenshot}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.NotNil(t, got.Content)
}

func TestToolCall_OpenCreatesThenNavigates(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	_, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionOpen,
			"url":    "https://example.com",
		}),
	)
	require.NoError(t, err)
	require.Len(t, drv.calls, 3)
	require.Equal(t, mcpToolTabs, drv.calls[0].Tool)
	require.Equal(t, tabActionNew, drv.calls[0].Args["action"])
	_, ok := drv.calls[0].Args["url"]
	require.False(t, ok)
	require.Equal(t, mcpToolNavigate, drv.calls[1].Tool)
	require.Equal(
		t,
		"https://example.com",
		drv.calls[1].Args["url"],
	)
}

func TestToolCall_FillUsesFillForm(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	_, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind": actFill,
				"fields": []map[string]any{{
					"name": "email",
					"ref":  "e1",
					"text": "a@example.com",
				}},
			},
		}),
	)
	require.NoError(t, err)
	require.Len(t, drv.calls, 1)
	require.Equal(t, mcpToolFillForm, drv.calls[0].Tool)
}

func TestToolCall_WaitConvertsMilliseconds(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	_, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind":   actWait,
				"timeMs": 1500,
			},
		}),
	)
	require.NoError(t, err)
	require.Len(t, drv.calls, 1)
	require.Equal(t, mcpToolWait, drv.calls[0].Tool)
	require.Equal(t, 1.5, drv.calls[0].Args["time"])
}

func TestToolCall_NavigateBlocksLoopbackByDefault(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	_, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionNavigate,
			"url":    "http://127.0.0.1:8080",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked")
}

func TestToolCall_TabsLimit(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolTabs: []map[string]any{{
				"type": "text",
				"text": "> 1 A - https://a.example\n 2 B - https://b.example",
			}},
		},
	}
	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionTabs,
			"limit":  1,
		}),
	)
	require.NoError(t, err)
	got := raw.(Result)
	require.Len(t, got.Tabs, 1)
}

func TestToolResolveDriver_HostServerPreferred(t *testing.T) {
	t.Parallel()

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		&serverTargetConfig{
			ID:        targetHost,
			ServerURL: "http://127.0.0.1:4321",
			AuthToken: "secret",
		},
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: &fakeDriver{},
		},
	)

	profile, drv, err := tool.resolveDriver(input{})
	require.NoError(t, err)
	require.Equal(t, defaultProfileName, profile)

	serverDrv, ok := drv.(*serverProfileDriver)
	require.True(t, ok)
	require.Equal(t, "http://127.0.0.1:4321", serverDrv.baseURL)
	require.Equal(t, "secret", serverDrv.token)
}

func TestToolCall_ProfilesUseBrowserServerDriver(t *testing.T) {
	t.Parallel()

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {
				Name:             defaultProfileName,
				BrowserServerURL: "http://127.0.0.1:19790",
			},
		},
		map[string]driver{
			defaultProfileName: &fakeDriver{},
		},
	)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionProfiles}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Len(t, got.Profiles, 1)
	require.Equal(
		t,
		driverTypeBrowserServer,
		got.Profiles[0].Driver,
	)
}

func TestToolCall_UsesBrowserServerDriverForHostTarget(t *testing.T) {
	t.Parallel()

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		&serverTargetConfig{
			ID:        targetHost,
			ServerURL: "http://127.0.0.1:19790",
		},
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: &fakeDriver{},
		},
	)

	result, err := tool.handleStatus(context.Background())
	require.NoError(t, err)
	require.Equal(t, ToolName, result.Driver)
	require.Equal(
		t,
		driverTypeBrowserServer,
		result.Profiles[0].Driver,
	)
}

func TestToolStatusDriver_UsesHostServerForServerBackedProfiles(
	t *testing.T,
) {
	t.Parallel()

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		&serverTargetConfig{
			ID:        targetHost,
			ServerURL: "http://127.0.0.1:4321",
			AuthToken: "secret",
		},
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		nil,
	)

	drv := tool.statusDriver(
		defaultProfileName,
		tool.profiles[defaultProfileName],
	)
	serverDrv, ok := drv.(*serverProfileDriver)
	require.True(t, ok)
	require.Equal(t, "http://127.0.0.1:4321", serverDrv.baseURL)
}

func TestToolResolveDriver_NodeTargetUsesConfiguredNode(t *testing.T) {
	t.Parallel()

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		map[string]serverTargetConfig{
			"edge": {
				ID:        "edge",
				ServerURL: "http://node.example:7777",
				AuthToken: "node-token",
			},
		},
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: &fakeDriver{},
		},
	)

	profile, drv, err := tool.resolveDriver(input{
		Target: targetNode,
		Node:   "edge",
	})
	require.NoError(t, err)
	require.Equal(t, defaultProfileName, profile)

	serverDrv, ok := drv.(*serverProfileDriver)
	require.True(t, ok)
	require.Equal(t, "http://node.example:7777", serverDrv.baseURL)
	require.Equal(t, "node-token", serverDrv.token)
}

func TestToolServerDriverForTarget_ConcurrentCache(t *testing.T) {
	t.Parallel()

	const goroutineCount = 32

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		nil,
	)
	target := &serverTargetConfig{
		ID:        targetHost,
		ServerURL: "http://127.0.0.1:4321",
		AuthToken: "secret",
	}
	errDriverMissing := errors.New("expected cached browser driver")
	results := make([]driver, goroutineCount)
	errs := make(chan error, goroutineCount)

	var wg sync.WaitGroup
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			drv, ok := tool.serverDriverForTarget(
				target,
				defaultProfileName,
			)
			if !ok || drv == nil {
				errs <- errDriverMissing
				return
			}
			results[index] = drv
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	require.Len(t, tool.serverDrivers, 1)
	first := results[0]
	require.NotNil(t, first)
	for _, drv := range results[1:] {
		require.Same(t, first, drv)
	}
}

func TestNewTool_DeclarationExposesSchema(t *testing.T) {
	t.Parallel()

	tool, err := NewTool(Config{
		Profiles: []ProfileConfig{{
			Name:      defaultProfileName,
			Transport: transportStdio,
			Command:   "npx",
		}},
	})
	require.NoError(t, err)

	decl := tool.Declaration()
	require.Equal(t, ToolName, decl.Name)
	require.Contains(t, decl.Description, "current browser tab")
	require.NotNil(t, decl.InputSchema)
	require.Equal(t, "object", decl.InputSchema.Type)
	require.Contains(t, decl.InputSchema.Properties, "action")
	require.Contains(t, decl.InputSchema.Properties, "request")
}

func TestLookupTool_ValidatesCallableTools(t *testing.T) {
	t.Parallel()

	callable, err := lookupTool([]toolpkg.Tool{
		lookupCallableTool{name: mcpToolTabs},
	}, mcpToolTabs)
	require.NoError(t, err)
	require.NotNil(t, callable)

	_, err = lookupTool([]toolpkg.Tool{
		lookupDeclTool{name: mcpToolTabs},
	}, mcpToolTabs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not callable")

	_, err = lookupTool([]toolpkg.Tool{
		lookupCallableTool{name: mcpToolClick},
	}, mcpToolTabs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not expose")
}

func TestToolCall_StartAndStop(t *testing.T) {
	t.Parallel()

	tool := newTestTool(&fakeDriver{
		startStatus: driverStatus{
			State:     stateReady,
			ToolCount: 4,
		},
	})

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionStart}),
	)
	require.NoError(t, err)
	started := raw.(Result)
	require.Equal(t, actionStart, started.Action)
	require.Equal(t, stateReady, started.State)
	require.Equal(t, 4, started.ToolCount)

	raw, err = tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionStop}),
	)
	require.NoError(t, err)
	stopped := raw.(Result)
	require.Equal(t, actionStop, stopped.Action)
	require.Equal(t, stateStopped, stopped.State)
}

func TestToolCall_FocusRefreshesTabs(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolTabs: tabsPayload(),
		},
	}
	tool := newTestTool(drv)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action":   actionFocus,
			"targetId": "tab-2",
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionFocus, got.Action)
	require.Equal(t, "tab-2", got.TargetID)
	require.Len(t, drv.calls, 2)
	require.Equal(t, mcpToolTabs, drv.calls[0].Tool)
	require.Equal(t, tabActionSelect, drv.calls[0].Args["action"])
	require.Equal(t, 2, drv.calls[0].Args["index"])
	require.Equal(t, mcpToolTabs, drv.calls[1].Tool)
}

func TestToolCall_CloseRefreshesTabs(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolTabs: tabsPayload(),
		},
	}
	tool := newTestTool(drv)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action":   actionClose,
			"targetId": "tab-3",
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionClose, got.Action)
	require.Len(t, drv.calls, 2)
	require.Equal(t, tabActionClose, drv.calls[0].Args["action"])
	require.Equal(t, 3, drv.calls[0].Args["index"])
	require.Len(t, got.Tabs, 2)
}

func TestToolCall_NavigateSelectsTarget(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolNavigate: textPayload("navigated"),
		},
	}
	tool := newTestTool(drv)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action":   actionNavigate,
			"targetId": "tab-2",
			"url":      "https://example.com",
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionNavigate, got.Action)
	require.Contains(t, got.Text, "navigated")
	require.Len(t, drv.calls, 2)
	require.Equal(t, mcpToolTabs, drv.calls[0].Tool)
	require.Equal(t, mcpToolNavigate, drv.calls[1].Tool)
}

func TestToolCall_NavigateRequiresURL(t *testing.T) {
	t.Parallel()

	_, err := newTestTool(&fakeDriver{}).Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionNavigate}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires url")
}

func TestToolCall_ConsolePDFUploadAndDialog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		input      map[string]any
		wantTool   string
		wantAction string
		assertCall func(*testing.T, fakeCall)
	}{
		{
			name: "console",
			input: map[string]any{
				"action":   actionConsole,
				"targetId": "tab-2",
				"level":    "error",
				"filename": "console.txt",
			},
			wantTool:   mcpToolConsole,
			wantAction: actionConsole,
			assertCall: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, "error", call.Args["level"])
				require.Equal(t, "console.txt", call.Args["filename"])
			},
		},
		{
			name: "pdf",
			input: map[string]any{
				"action":   actionPDF,
				"targetId": "tab-2",
				"filename": "page.pdf",
			},
			wantTool:   mcpToolPDF,
			wantAction: actionPDF,
			assertCall: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, "page.pdf", call.Args["filename"])
			},
		},
		{
			name: "upload",
			input: map[string]any{
				"action":   actionUpload,
				"targetId": "tab-2",
				"paths":    []string{"/tmp/a.txt"},
			},
			wantTool:   mcpToolUpload,
			wantAction: actionUpload,
			assertCall: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, []string{"/tmp/a.txt"}, call.Args["paths"])
			},
		},
		{
			name: "dialog",
			input: map[string]any{
				"action":     actionDialog,
				"targetId":   "tab-2",
				"accept":     true,
				"promptText": "ok",
			},
			wantTool:   mcpToolDialog,
			wantAction: actionDialog,
			assertCall: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, true, call.Args["accept"])
				require.Equal(t, "ok", call.Args["promptText"])
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			drv := &fakeDriver{
				callResult: map[string]any{
					tc.wantTool: textPayload(tc.name),
				},
			}
			tool := newTestTool(drv)

			raw, err := tool.Call(
				context.Background(),
				mustJSON(t, tc.input),
			)
			require.NoError(t, err)

			got := raw.(Result)
			require.Equal(t, tc.wantAction, got.Action)
			require.Contains(t, got.Text, tc.name)
			require.Len(t, drv.calls, 2)
			require.Equal(t, mcpToolTabs, drv.calls[0].Tool)
			require.Equal(t, tc.wantTool, drv.calls[1].Tool)
			tc.assertCall(t, drv.calls[1])
		})
	}
}

func TestToolCall_UploadRequiresPaths(t *testing.T) {
	t.Parallel()

	_, err := newTestTool(&fakeDriver{}).Call(
		context.Background(),
		mustJSON(t, map[string]any{"action": actionUpload}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires paths")
}

func TestToolCall_ActRoutesLegacyFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     map[string]any
		wantTool  string
		assertArg func(*testing.T, fakeCall)
	}{
		{
			name: "type",
			input: map[string]any{
				"action": actionAct,
				"kind":   actType,
				"ref":    "e1",
				"text":   "hello",
				"submit": true,
			},
			wantTool: mcpToolType,
			assertArg: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, "element e1", call.Args["element"])
				require.Equal(t, "hello", call.Args["text"])
				require.Equal(t, true, call.Args["submit"])
			},
		},
		{
			name: "press",
			input: map[string]any{
				"action": actionAct,
				"kind":   actPress,
				"key":    "Enter",
			},
			wantTool: mcpToolPressKey,
			assertArg: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, "Enter", call.Args["key"])
			},
		},
		{
			name: "hover",
			input: map[string]any{
				"action": actionAct,
				"kind":   actHover,
				"ref":    "e2",
			},
			wantTool: mcpToolHover,
			assertArg: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, "element e2", call.Args["element"])
			},
		},
		{
			name: "drag",
			input: map[string]any{
				"action":   actionAct,
				"kind":     actDrag,
				"startRef": "e1",
				"endRef":   "e2",
			},
			wantTool: mcpToolDrag,
			assertArg: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, "element e1", call.Args["startElement"])
				require.Equal(t, "element e2", call.Args["endElement"])
			},
		},
		{
			name: "select",
			input: map[string]any{
				"action": actionAct,
				"kind":   actSelect,
				"ref":    "e3",
				"values": []string{"a"},
			},
			wantTool: mcpToolSelect,
			assertArg: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, []string{"a"}, call.Args["values"])
			},
		},
		{
			name: "resize",
			input: map[string]any{
				"action": actionAct,
				"kind":   actResize,
				"width":  1280,
				"height": 720,
			},
			wantTool: mcpToolResize,
			assertArg: func(t *testing.T, call fakeCall) {
				t.Helper()
				require.Equal(t, 1280, call.Args["width"])
				require.Equal(t, 720, call.Args["height"])
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			drv := &fakeDriver{
				callResult: map[string]any{
					tc.wantTool: textPayload(tc.name),
				},
			}
			tool := newTestTool(drv)

			raw, err := tool.Call(
				context.Background(),
				mustJSON(t, tc.input),
			)
			require.NoError(t, err)

			got := raw.(Result)
			require.Equal(t, actionAct, got.Action)
			require.Contains(t, got.Text, tc.name)
			require.Len(t, drv.calls, 1)
			require.Equal(t, tc.wantTool, drv.calls[0].Tool)
			tc.assertArg(t, drv.calls[0])
		})
	}
}

func TestToolCall_ActCloseRefreshesTabs(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolTabs: tabsPayload(),
		},
	}
	tool := newTestTool(drv)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind": actClose,
			},
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionAct, got.Action)
	require.Len(t, got.Tabs, 2)
	require.Len(t, drv.calls, 2)
	require.Equal(t, tabActionClose, drv.calls[0].Args["action"])
}

func TestToolCall_ActEvaluateEnabled(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolEvaluate: textPayload("evaluated"),
		},
	}
	tool := newToolWithDrivers(
		defaultProfileName,
		true,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind": actEvaluate,
				"fn":   "() => 1",
				"ref":  "e1",
			},
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionAct, got.Action)
	require.Contains(t, got.Text, "evaluated")
	require.Len(t, drv.calls, 1)
	require.Equal(t, mcpToolEvaluate, drv.calls[0].Tool)
	require.Equal(t, "element e1", drv.calls[0].Args["element"])
}

func TestToolCall_ScreenshotPassesOptions(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolScreenshot: []map[string]any{{
				"type":     "image",
				"data":     "ZmFrZQ==",
				"mimeType": testPNG,
			}},
		},
	}
	tool := newTestTool(drv)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action":   actionScreenshot,
			"targetId": "tab-2",
			"fullPage": true,
			"filename": "page.png",
			"ref":      "e1",
			"element":  "hero",
			"type":     "png",
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionScreenshot, got.Action)
	require.NotNil(t, got.Content)
	require.Len(t, drv.calls, 2)
	require.Equal(t, mcpToolTabs, drv.calls[0].Tool)
	require.Equal(t, mcpToolScreenshot, drv.calls[1].Tool)
	require.Equal(t, true, drv.calls[1].Args["fullPage"])
	require.Equal(t, "page.png", drv.calls[1].Args["filename"])
	require.Equal(t, "e1", drv.calls[1].Args["ref"])
	require.Equal(t, "hero", drv.calls[1].Args["element"])
	require.Equal(t, "png", drv.calls[1].Args["type"])
}

func TestToolCall_ActWaitPassesSupportedArgs(t *testing.T) {
	t.Parallel()

	drv := &fakeDriver{
		callResult: map[string]any{
			mcpToolWait: textPayload("waited"),
		},
	}
	tool := newTestTool(drv)

	raw, err := tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind":      actWait,
				"timeMs":    1500,
				"text":      "Ready",
				"textGone":  "Busy",
				"timeoutMs": 2000,
			},
		}),
	)
	require.NoError(t, err)

	got := raw.(Result)
	require.Equal(t, actionAct, got.Action)
	require.Contains(t, got.Text, "waited")
	require.Len(t, drv.calls, 1)
	require.Equal(t, mcpToolWait, drv.calls[0].Tool)
	require.Equal(t, 1.5, drv.calls[0].Args["time"])
	require.Equal(t, "Ready", drv.calls[0].Args["text"])
	require.Equal(t, "Busy", drv.calls[0].Args["textGone"])
	require.Equal(t, 2000, drv.calls[0].Args["timeoutMs"])
}

func TestToolCall_ActRejectsUnsupportedWaitShape(t *testing.T) {
	t.Parallel()

	_, err := newTestTool(&fakeDriver{}).Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind":     actWait,
				"selector": "#main",
			},
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func TestToolCall_ActFillRequiresFields(t *testing.T) {
	t.Parallel()

	_, err := newTestTool(&fakeDriver{}).Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind": actFill,
			},
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires fields")
}

func TestToolCall_ActRejectsUnsupportedKind(t *testing.T) {
	t.Parallel()

	_, err := newTestTool(&fakeDriver{}).Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionAct,
			"request": map[string]any{
				"kind": "dance",
			},
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported browser act kind")
}

func TestToolCall_RejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tool := newTestTool(&fakeDriver{})

	_, err := tool.Call(context.Background(), []byte("{"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid browser args")

	_, err = tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "action is required")

	_, err = tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": "unknown",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported browser action")

	_, err = tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionSnapshot,
			"target": "elsewhere",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown browser target")

	_, err = tool.Call(
		context.Background(),
		mustJSON(t, map[string]any{
			"action": actionSnapshot,
			"node":   "edge",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "only valid")
}

func TestToolResolveDriver_ErrorPaths(t *testing.T) {
	t.Parallel()

	tool := newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		map[string]serverTargetConfig{
			"a": {ID: "a", ServerURL: "http://a.example"},
			"b": {ID: "b", ServerURL: "http://b.example"},
		},
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: &fakeDriver{},
		},
	)

	_, _, err := tool.resolveDriver(input{Target: targetSandbox})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not configured")

	_, _, err = tool.resolveDriver(input{Target: targetNode})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires node")

	_, _, err = tool.resolveDriver(input{Profile: "missing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not configured")
}

func TestToolDriverHelpers(t *testing.T) {
	t.Parallel()

	value := true
	size := 12

	require.True(t, boolValue(&value))
	require.False(t, boolValue(nil))
	require.Equal(t, 12, intValue(&size))
	require.Zero(t, intValue(nil))
	require.Equal(t, "element e1", describeElement("e1", ""))
	require.Equal(t, "element fallback", describeElement("", "fallback"))
	require.Equal(t, "element", describeElement("", ""))
}

func textPayload(text string) []map[string]any {
	return []map[string]any{{
		"type": "text",
		"text": text,
	}}
}

func tabsPayload() []map[string]any {
	return textPayload(
		"> 2 Example - https://example.com\n" +
			" 3 Other - https://other.example",
	)
}

func newTestTool(drv driver) *Tool {
	return newToolWithDrivers(
		defaultProfileName,
		false,
		navigationPolicy{},
		nil,
		nil,
		nil,
		map[string]ProfileConfig{
			defaultProfileName: {Name: defaultProfileName},
		},
		map[string]driver{
			defaultProfileName: drv,
		},
	)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	require.NoError(t, err)
	return body
}
