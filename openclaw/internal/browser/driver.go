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
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const (
	driverTypePlaywrightMCP = "playwright_mcp"
	driverTypeBrowserServer = "browser_server"

	stateReady   = "ready"
	stateStopped = "stopped"
)

const (
	mcpToolClick        = "browser_click"
	mcpToolClose        = "browser_close"
	mcpToolConsole      = "browser_console_messages"
	mcpToolCookies      = "browser_cookies"
	mcpToolCookiesSet   = "browser_cookies_set"
	mcpToolCookiesClear = "browser_cookies_clear"
	mcpToolDrag         = "browser_drag"
	mcpToolEvaluate     = "browser_evaluate"
	mcpToolFillForm     = "browser_fill_form"
	mcpToolStorageGet   = "browser_storage_get"
	mcpToolStorageSet   = "browser_storage_set"
	mcpToolStorageClear = "browser_storage_clear"
	mcpToolUpload       = "browser_file_upload"
	mcpToolDialog       = "browser_handle_dialog"
	mcpToolDownload     = "browser_download"
	mcpToolWaitDownload = "browser_wait_download"
	mcpToolSetOffline   = "browser_set_offline"
	mcpToolSetHeaders   = "browser_set_headers"
	mcpToolSetCreds     = "browser_set_credentials"
	mcpToolSetGeo       = "browser_set_geolocation"
	mcpToolSetMedia     = "browser_set_media"
	mcpToolSetTZ        = "browser_set_timezone"
	mcpToolSetLocale    = "browser_set_locale"
	mcpToolSetDevice    = "browser_set_device"
	mcpToolHover        = "browser_hover"
	mcpToolInstall      = "browser_install"
	mcpToolNavigate     = "browser_navigate"
	mcpToolPDF          = "browser_pdf_save"
	mcpToolPressKey     = "browser_press_key"
	mcpToolResize       = "browser_resize"
	mcpToolScroll       = "browser_scroll_into_view"
	mcpToolScreenshot   = "browser_take_screenshot"
	mcpToolSelect       = "browser_select_option"
	mcpToolSnapshot     = "browser_snapshot"
	mcpToolTabs         = "browser_tabs"
	mcpToolType         = "browser_type"
	mcpToolWait         = "browser_wait_for"
)

var supportedMCPTools = []string{
	mcpToolClick,
	mcpToolClose,
	mcpToolConsole,
	mcpToolDrag,
	mcpToolEvaluate,
	mcpToolFillForm,
	mcpToolUpload,
	mcpToolDialog,
	mcpToolHover,
	mcpToolInstall,
	mcpToolNavigate,
	mcpToolPDF,
	mcpToolPressKey,
	mcpToolResize,
	mcpToolScroll,
	mcpToolScreenshot,
	mcpToolSelect,
	mcpToolSnapshot,
	mcpToolTabs,
	mcpToolType,
	mcpToolWait,
}

type driverStatus struct {
	State     string
	ToolCount int
}

type driver interface {
	Start(ctx context.Context) (driverStatus, error)
	Status(ctx context.Context) (driverStatus, error)
	Stop() error
	Call(
		ctx context.Context,
		toolName string,
		args map[string]any,
	) (any, error)
}

type mcpProfileDriver struct {
	toolSet *mcptool.ToolSet

	mu      sync.RWMutex
	started bool
}

func newMCPProfileDriver(profile resolvedProfile) *mcpProfileDriver {
	options := []mcptool.ToolSetOption{
		mcptool.WithToolFilterFunc(
			tool.NewIncludeToolNamesFilter(supportedMCPTools...),
		),
	}
	if profile.Reconnect != nil && profile.Reconnect.Enabled {
		options = append(
			options,
			mcptool.WithSessionReconnect(
				profile.Reconnect.MaxAttempts,
			),
		)
	}

	return &mcpProfileDriver{
		toolSet: mcptool.NewMCPToolSet(profile.Connection, options...),
	}
}

func (d *mcpProfileDriver) Start(
	ctx context.Context,
) (driverStatus, error) {
	if err := d.toolSet.Init(ctx); err != nil {
		return driverStatus{}, err
	}

	d.mu.Lock()
	d.started = true
	d.mu.Unlock()

	return driverStatus{
		State:     stateReady,
		ToolCount: len(d.toolSet.Tools(ctx)),
	}, nil
}

func (d *mcpProfileDriver) Status(
	ctx context.Context,
) (driverStatus, error) {
	d.mu.RLock()
	started := d.started
	d.mu.RUnlock()

	if !started {
		return driverStatus{State: stateStopped}, nil
	}

	if err := d.toolSet.Init(ctx); err != nil {
		return driverStatus{}, err
	}
	return driverStatus{
		State:     stateReady,
		ToolCount: len(d.toolSet.Tools(ctx)),
	}, nil
}

func (d *mcpProfileDriver) Stop() error {
	if err := d.toolSet.Close(); err != nil {
		return err
	}
	d.mu.Lock()
	d.started = false
	d.mu.Unlock()
	return nil
}

func (d *mcpProfileDriver) Call(
	ctx context.Context,
	toolName string,
	args map[string]any,
) (any, error) {
	if _, err := d.Start(ctx); err != nil {
		return nil, err
	}

	callable, err := lookupTool(d.toolSet.Tools(ctx), toolName)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal browser args: %w", err)
	}
	return callable.Call(ctx, payload)
}

func lookupTool(
	tools []tool.Tool,
	name string,
) (tool.CallableTool, error) {
	for i := range tools {
		decl := tools[i].Declaration()
		if decl == nil || decl.Name != name {
			continue
		}
		callable, ok := tools[i].(tool.CallableTool)
		if !ok {
			return nil, fmt.Errorf(
				"browser backend tool %q is not callable",
				name,
			)
		}
		return callable, nil
	}
	return nil, fmt.Errorf(
		"browser backend does not expose tool %q",
		name,
	)
}
