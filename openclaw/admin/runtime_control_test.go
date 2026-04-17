//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubRuntimeLifecycleProvider struct {
	status    RuntimeLifecycleStatus
	statusErr error

	index    RuntimeLifecycleVersionIndex
	indexErr error

	changelog    RuntimeLifecycleChangelog
	changelogErr error
	changelogFor string

	actionErr   error
	actionReq   RuntimeLifecycleActionRequest
	actionCount int
}

func (p *stubRuntimeLifecycleProvider) RuntimeLifecycleStatus() (
	RuntimeLifecycleStatus,
	error,
) {
	if p == nil {
		return RuntimeLifecycleStatus{}, nil
	}
	return p.status, p.statusErr
}

func (p *stubRuntimeLifecycleProvider) RuntimeLifecycleVersions() (
	RuntimeLifecycleVersionIndex,
	error,
) {
	if p == nil {
		return RuntimeLifecycleVersionIndex{}, nil
	}
	return p.index, p.indexErr
}

func (p *stubRuntimeLifecycleProvider) RuntimeLifecycleChangelog(
	version string,
) (RuntimeLifecycleChangelog, error) {
	if p == nil {
		return RuntimeLifecycleChangelog{}, nil
	}
	p.changelogFor = strings.TrimSpace(version)
	return p.changelog, p.changelogErr
}

func (p *stubRuntimeLifecycleProvider) RequestRuntimeLifecycleAction(
	req RuntimeLifecycleActionRequest,
) (RuntimeLifecycleActionResult, error) {
	if p == nil {
		return RuntimeLifecycleActionResult{}, nil
	}
	p.actionCount++
	p.actionReq = req
	return RuntimeLifecycleActionResult{
		Status:  p.status,
		Started: p.actionErr == nil,
	}, p.actionErr
}

func TestServiceHandlerRendersRuntimeControlPage(t *testing.T) {
	t.Parallel()

	provider := &stubRuntimeLifecycleProvider{
		status: RuntimeLifecycleStatus{
			State:           "idle",
			CurrentVersion:  "v0.0.70",
			RunningRequests: 1,
			QueuedRequests:  2,
			ExitCode:        75,
			Pending: &RuntimeLifecyclePendingAction{
				Kind:          runtimeActionUpgrade,
				Mode:          runtimeModeGraceful,
				TargetVersion: "v0.0.71",
				Summary: []string{
					"ready to drain",
				},
			},
		},
		index: RuntimeLifecycleVersionIndex{
			LatestVersion:      "v0.0.71",
			MinSupportedTarget: "v0.0.48",
			Versions: []RuntimeLifecycleVersion{
				{Version: "v0.0.70"},
				{Version: "v0.0.71"},
			},
		},
		changelog: RuntimeLifecycleChangelog{
			Version: "v0.0.71",
			Summary: []string{
				"release summary",
			},
			Changelog: "line one\nline two",
		},
	}

	svc := New(
		Config{},
		WithRuntimeLifecycleProvider(provider),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		routeRuntimeControlPage+"?version=v0.0.71",
		nil,
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "Runtime Control")
	require.Contains(t, body, "Quick Actions")
	require.Contains(t, body, "Target Version")
	require.Contains(t, body, "Release Notes")
	require.Contains(t, body, "Force Upgrade to Latest")
	require.Contains(t, body, `action="api/runtime/control/action"`)
	require.Contains(t, body, `href="runtime-control"`)
	require.Contains(t, body, "v0.0.71")
	require.Contains(t, body, "release summary")
	require.Equal(t, "v0.0.71", provider.changelogFor)
}

func TestServiceOverviewRuntimeControlNavVisibility(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, routeOverview, nil)

	hidden := New(Config{})
	hiddenRec := httptest.NewRecorder()
	hidden.Handler().ServeHTTP(hiddenRec, req)
	require.Equal(t, http.StatusOK, hiddenRec.Code)
	require.NotContains(
		t,
		hiddenRec.Body.String(),
		`href="runtime-control"`,
	)

	visible := New(
		Config{},
		WithRuntimeLifecycleProvider(
			&stubRuntimeLifecycleProvider{},
		),
	)
	visibleRec := httptest.NewRecorder()
	visible.Handler().ServeHTTP(visibleRec, req)
	require.Equal(t, http.StatusOK, visibleRec.Code)
	require.Contains(
		t,
		visibleRec.Body.String(),
		`href="runtime-control"`,
	)
}

func TestHandleRuntimeControlAction(t *testing.T) {
	t.Parallel()

	provider := &stubRuntimeLifecycleProvider{}
	svc := New(
		Config{},
		WithRuntimeLifecycleProvider(provider),
	)

	values := url.Values{
		formRuntimeActionKind:    {runtimeActionUpgrade},
		formRuntimeActionMode:    {runtimeModeForce},
		formRuntimeTargetVersion: {"v0.0.71"},
		formReturnPath:           {routeRuntimeControlPage},
		formReturnTo:             {"runtime-control-version"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeRuntimeControlAction,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, 1, provider.actionCount)
	require.Equal(t, RuntimeLifecycleActionRequest{
		Kind:          runtimeActionUpgrade,
		Mode:          runtimeModeForce,
		TargetVersion: "v0.0.71",
	}, provider.actionReq)
	location := rr.Header().Get("Location")
	require.Contains(
		t,
		location,
		"runtime-control?notice=Requested+force+switch+to+v0.0.71.",
	)
	require.True(
		t,
		strings.HasSuffix(location, "#runtime-control-version"),
	)
}

func TestHandleRuntimeControlActionValidationError(t *testing.T) {
	t.Parallel()

	svc := New(
		Config{},
		WithRuntimeLifecycleProvider(
			&stubRuntimeLifecycleProvider{},
		),
	)

	values := url.Values{
		formRuntimeActionKind: {"restart"},
		formReturnPath:        {routeRuntimeControlPage},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		routeRuntimeControlAction,
		strings.NewReader(values.Encode()),
	)
	req.Header.Set(
		"Content-Type",
		"application/x-www-form-urlencoded",
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Contains(
		t,
		rr.Header().Get("Location"),
		"error=runtime+action+mode+is+required",
	)
}

func TestHandleRuntimeControlPageStateJSONUsesLightweightStatus(t *testing.T) {
	t.Parallel()

	provider := &stubRuntimeLifecycleProvider{
		status: RuntimeLifecycleStatus{
			CurrentVersion: "v0.0.70",
		},
		index: RuntimeLifecycleVersionIndex{
			LatestVersion: "v0.0.71",
		},
		changelogErr: errors.New("should not fetch changelog"),
	}
	svc := New(
		Config{},
		WithClock(func() time.Time {
			return time.Unix(1700000000, 0)
		}),
		WithRuntimeLifecycleProvider(provider),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		routePageStateJSON+"?view="+string(viewRuntimeControl),
		nil,
	)
	rr := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Empty(t, provider.changelogFor)

	var state pageStateStatus
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))
	require.NotEmpty(t, state.Token)
}
