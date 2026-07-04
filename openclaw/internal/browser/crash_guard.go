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
	"fmt"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const (
	browserCrashGuardStateKey = "__openclaw_browser_crash_guard__"
	browserCrashThreshold     = 2
	stateDegraded             = "degraded"
)

var browserCrashGuardInitMu sync.Mutex

type browserCrashGuard struct {
	mu       sync.Mutex
	profiles map[string]browserCrashState
}

type browserCrashState struct {
	Consecutive int
	Reason      string
}

type crashGuardedDriver struct {
	inner   driver
	profile string
}

func newCrashGuardedDriver(
	ctx context.Context,
	profile string,
	drv driver,
) driver {
	if drv == nil {
		return nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return drv
	}
	return &crashGuardedDriver{
		inner:   drv,
		profile: profile,
	}
}

func (d *crashGuardedDriver) Start(
	ctx context.Context,
) (driverStatus, error) {
	return d.inner.Start(ctx)
}

func (d *crashGuardedDriver) Status(
	ctx context.Context,
) (driverStatus, error) {
	return d.inner.Status(ctx)
}

func (d *crashGuardedDriver) Stop() error {
	return d.inner.Stop()
}

func (d *crashGuardedDriver) Call(
	ctx context.Context,
	toolName string,
	args map[string]any,
) (any, error) {
	if state, ok := browserCrashStateForContext(ctx, d.profile); ok &&
		state.Consecutive >= browserCrashThreshold {
		return browserCrashBlockedContent(d.profile, state.Reason), nil
	}

	raw, err := d.inner.Call(ctx, toolName, args)
	if err != nil {
		if ok, reason := detectBrowserBackendCrash(err.Error()); ok {
			recordBrowserCrash(ctx, d.profile, reason)
		}
		return nil, err
	}

	text := extractText(raw)
	if ok, reason := detectBrowserBackendCrash(text); ok {
		recordBrowserCrash(ctx, d.profile, reason)
		return raw, nil
	}
	resetBrowserCrash(ctx, d.profile)
	return raw, nil
}

func browserCrashBlockedResult(
	ctx context.Context,
	action string,
	profile string,
	driverType string,
	evaluateEnabled bool,
) (Result, bool) {
	if browserCrashBypassAction(action) {
		return Result{}, false
	}
	state, ok := browserCrashStateForContext(ctx, profile)
	if !ok || state.Consecutive < browserCrashThreshold {
		return Result{}, false
	}
	result := newBaseResult(action, profile, driverType, evaluateEnabled)
	result.State = stateDegraded
	result.Warning = browserCrashBlockedMessage(profile, state.Reason)
	result.Text = result.Warning
	return result, true
}

func browserCrashBypassAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case strings.ToLower(actionStart),
		strings.ToLower(actionStop),
		strings.ToLower(actionStatus),
		strings.ToLower(actionProfiles):
		return true
	default:
		return false
	}
}

func browserCrashStateForContext(
	ctx context.Context,
	profile string,
) (browserCrashState, bool) {
	guard := browserCrashGuardFromContext(ctx, false)
	if guard == nil {
		return browserCrashState{}, false
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	state, ok := guard.profiles[normalizeBrowserCrashProfile(profile)]
	return state, ok
}

func recordBrowserCrash(ctx context.Context, profile string, reason string) {
	guard := browserCrashGuardFromContext(ctx, true)
	if guard == nil {
		return
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	key := normalizeBrowserCrashProfile(profile)
	state := guard.profiles[key]
	state.Consecutive++
	state.Reason = strings.TrimSpace(reason)
	guard.profiles[key] = state
}

func resetBrowserCrash(ctx context.Context, profile string) {
	guard := browserCrashGuardFromContext(ctx, false)
	if guard == nil {
		return
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	delete(guard.profiles, normalizeBrowserCrashProfile(profile))
}

func browserCrashGuardFromContext(
	ctx context.Context,
	create bool,
) *browserCrashGuard {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	guard, ok := agent.GetStateValue[*browserCrashGuard](
		inv,
		browserCrashGuardStateKey,
	)
	if ok && guard != nil {
		return guard
	}
	if !create {
		return nil
	}
	browserCrashGuardInitMu.Lock()
	defer browserCrashGuardInitMu.Unlock()
	guard, ok = agent.GetStateValue[*browserCrashGuard](
		inv,
		browserCrashGuardStateKey,
	)
	if ok && guard != nil {
		return guard
	}
	guard = &browserCrashGuard{
		profiles: make(map[string]browserCrashState),
	}
	inv.SetState(browserCrashGuardStateKey, guard)
	return guard
}

func normalizeBrowserCrashProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return defaultProfileName
	}
	return profile
}

func detectBrowserBackendCrash(text string) (bool, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false, ""
	}
	lower := strings.ToLower(trimmed)
	hasErrorContext := strings.Contains(lower, "### error") ||
		strings.Contains(lower, "error:") ||
		strings.Contains(lower, "browser logs") ||
		strings.Contains(lower, "call log") ||
		strings.Contains(lower, "async initializeserver")
	hasProcessExitLog := strings.Contains(lower, "<process did exit") ||
		(strings.Contains(lower, "[pid=") &&
			strings.Contains(lower, "process did exit"))
	hasSIGTRAP := strings.Contains(lower, "signal=sigtrap") ||
		strings.Contains(lower, "sigtrap")
	switch {
	case hasErrorContext &&
		strings.Contains(lower, "target page, context or browser has been closed"):
		return true, "target page, context or browser has been closed"
	case hasErrorContext && strings.Contains(lower, "browser has been closed"):
		return true, "browser has been closed"
	case hasErrorContext && strings.Contains(lower, "browser has disconnected"):
		return true, "browser has disconnected"
	case hasErrorContext && strings.Contains(lower, "browser is closed"):
		return true, "browser is closed"
	case (hasErrorContext || hasProcessExitLog) &&
		strings.Contains(lower, "process did exit") &&
		!hasSIGTRAP:
		return true, "browser process exited"
	case (hasErrorContext || hasProcessExitLog) && hasSIGTRAP:
		return true, "browser process exited with SIGTRAP"
	case strings.Contains(lower, "connection closed") &&
		strings.Contains(lower, "browser"):
		return true, "browser connection closed"
	default:
		return false, ""
	}
}

func browserCrashBlockedContent(profile string, reason string) []map[string]any {
	return []map[string]any{{
		"type": "text",
		"text": browserCrashBlockedMessage(profile, reason),
	}}
}

func browserCrashBlockedMessage(profile string, reason string) string {
	profile = normalizeBrowserCrashProfile(profile)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "browser backend crashed repeatedly"
	}
	return fmt.Sprintf(
		"Browser backend is degraded for profile %q in this agent run "+
			"after repeated backend crashes; last error: %s. "+
			"This is a browser automation blocker, not a page-specific "+
			"navigation error. Do not retry browser automation in this "+
			"run unless the runtime configuration changes. Use "+
			"web_fetch, search, exec, or document tools instead.",
		profile,
		reason,
	)
}
