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
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	routeRuntimeControlPage   = "/runtime-control"
	routeRuntimeControlAction = "/api/runtime/control/action"

	queryRuntimeVersion = "version"

	formRuntimeActionKind    = "kind"
	formRuntimeActionMode    = "mode"
	formRuntimeTargetVersion = "target_version"

	runtimeActionRestart = "restart"
	runtimeActionUpgrade = "upgrade"

	runtimeModeGraceful = "graceful"
	runtimeModeForce    = "force"
)

const pageSummaryRuntimeControl = "" +
	"Request graceful or forced restarts, switch versions, " +
	"and inspect release notes for the current runtime."

const (
	viewRuntimeControl adminView = "runtime_control"
)

type RuntimeLifecycleProvider interface {
	RuntimeLifecycleStatus() (RuntimeLifecycleStatus, error)
	RuntimeLifecycleVersions() (RuntimeLifecycleVersionIndex, error)
	RuntimeLifecycleChangelog(string) (
		RuntimeLifecycleChangelog,
		error,
	)
	RequestRuntimeLifecycleAction(
		RuntimeLifecycleActionRequest,
	) (RuntimeLifecycleActionResult, error)
}

type RuntimeLifecycleStatus struct {
	State           string                         `json:"state,omitempty"`
	CurrentVersion  string                         `json:"current_version,omitempty"`
	ActiveRequests  int                            `json:"active_requests"`
	RunningRequests int                            `json:"running_requests"`
	QueuedRequests  int                            `json:"queued_requests"`
	Pending         *RuntimeLifecyclePendingAction `json:"pending,omitempty"`
	UpdatedAt       time.Time                      `json:"updated_at,omitempty"`
	ExitCode        int                            `json:"exit_code"`
}

type RuntimeLifecyclePendingAction struct {
	ID             string    `json:"id,omitempty"`
	Kind           string    `json:"kind,omitempty"`
	Mode           string    `json:"mode,omitempty"`
	TargetVersion  string    `json:"target_version,omitempty"`
	Actor          string    `json:"actor,omitempty"`
	Source         string    `json:"source,omitempty"`
	RequestedAt    time.Time `json:"requested_at,omitempty"`
	CurrentVersion string    `json:"current_version,omitempty"`
	Summary        []string  `json:"summary,omitempty"`
}

type RuntimeLifecycleVersionIndex struct {
	LatestVersion      string                    `json:"latest_version,omitempty"`
	MinSupportedTarget string                    `json:"min_supported_target,omitempty"`
	Versions           []RuntimeLifecycleVersion `json:"versions,omitempty"`
}

type RuntimeLifecycleVersion struct {
	Version      string    `json:"version,omitempty"`
	PublishedAt  time.Time `json:"published_at,omitempty"`
	InstallURL   string    `json:"install_url,omitempty"`
	ChangelogURL string    `json:"changelog_url,omitempty"`
	Notes        []string  `json:"notes,omitempty"`
}

type RuntimeLifecycleChangelog struct {
	Version   string   `json:"version,omitempty"`
	Summary   []string `json:"summary,omitempty"`
	Changelog string   `json:"changelog,omitempty"`
}

type RuntimeLifecycleActionRequest struct {
	Kind          string `json:"kind,omitempty"`
	Mode          string `json:"mode,omitempty"`
	TargetVersion string `json:"target_version,omitempty"`
}

type RuntimeLifecycleActionResult struct {
	Status  RuntimeLifecycleStatus `json:"status"`
	Started bool                   `json:"started"`
}

type RuntimeLifecyclePageStatus struct {
	Enabled         bool                         `json:"enabled"`
	Error           string                       `json:"error,omitempty"`
	Status          RuntimeLifecycleStatus       `json:"status"`
	Index           RuntimeLifecycleVersionIndex `json:"index"`
	SelectedVersion string                       `json:"selected_version,omitempty"`
	Changelog       RuntimeLifecycleChangelog    `json:"changelog"`
}

func (s *Service) runtimeLifecycleProvider() RuntimeLifecycleProvider {
	if s == nil {
		return nil
	}
	return s.runtimeLifecycle
}

func (s *Service) hasRuntimeLifecycleProvider() bool {
	return s.runtimeLifecycleProvider() != nil
}

func (s *Service) runtimeLifecycleStatus(
	r *http.Request,
) RuntimeLifecyclePageStatus {
	provider := s.runtimeLifecycleProvider()
	if provider == nil {
		return RuntimeLifecyclePageStatus{}
	}

	status, err := provider.RuntimeLifecycleStatus()
	if err != nil {
		return RuntimeLifecyclePageStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}

	state := RuntimeLifecyclePageStatus{
		Enabled: true,
		Status:  status,
	}

	index, err := provider.RuntimeLifecycleVersions()
	if err != nil {
		state.Error = strings.TrimSpace(err.Error())
		return state
	}
	state.Index = index

	selected := resolveRuntimeLifecycleSelectedVersion(r, state)
	state.SelectedVersion = selected
	if selected == "" {
		return state
	}

	changelog, err := provider.RuntimeLifecycleChangelog(selected)
	if err != nil {
		state.Error = strings.TrimSpace(err.Error())
		return state
	}
	state.Changelog = changelog
	return state
}

func (s *Service) runtimeLifecycleRefreshStatus(
	r *http.Request,
) RuntimeLifecyclePageStatus {
	provider := s.runtimeLifecycleProvider()
	if provider == nil {
		return RuntimeLifecyclePageStatus{}
	}

	status, err := provider.RuntimeLifecycleStatus()
	if err != nil {
		return RuntimeLifecyclePageStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}

	return RuntimeLifecyclePageStatus{
		Enabled:         true,
		Status:          status,
		SelectedVersion: resolveRuntimeLifecycleRefreshVersion(r, status),
	}
}

func resolveRuntimeLifecycleSelectedVersion(
	r *http.Request,
	state RuntimeLifecyclePageStatus,
) string {
	if r != nil && r.URL != nil {
		version := strings.TrimSpace(
			r.URL.Query().Get(queryRuntimeVersion),
		)
		if version != "" {
			return version
		}
	}
	if state.Status.Pending != nil {
		version := strings.TrimSpace(
			state.Status.Pending.TargetVersion,
		)
		if version != "" {
			return version
		}
	}
	version := strings.TrimSpace(state.Index.LatestVersion)
	if version != "" {
		return version
	}
	return strings.TrimSpace(state.Status.CurrentVersion)
}

func resolveRuntimeLifecycleRefreshVersion(
	r *http.Request,
	status RuntimeLifecycleStatus,
) string {
	if r != nil && r.URL != nil {
		version := strings.TrimSpace(
			r.URL.Query().Get(queryRuntimeVersion),
		)
		if version != "" {
			return version
		}
	}
	if status.Pending != nil {
		version := strings.TrimSpace(
			status.Pending.TargetVersion,
		)
		if version != "" {
			return version
		}
	}
	return strings.TrimSpace(status.CurrentVersion)
}

func (s *Service) handleRuntimeControlPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewRuntimeControl)
}

func (s *Service) handleRuntimeControlAction(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	returnTo := strings.TrimSpace(r.FormValue(formReturnTo))
	provider := s.runtimeLifecycleProvider()
	if provider == nil {
		s.redirectRuntimeControlWithMessage(
			w,
			r,
			queryError,
			"runtime lifecycle provider is not available",
			"",
			returnTo,
		)
		return
	}

	req, err := runtimeLifecycleActionRequestFromForm(r)
	if err != nil {
		s.redirectRuntimeControlWithMessage(
			w,
			r,
			queryError,
			err.Error(),
			"",
			returnTo,
		)
		return
	}

	if _, err := provider.RequestRuntimeLifecycleAction(req); err != nil {
		s.redirectRuntimeControlWithMessage(
			w,
			r,
			queryError,
			err.Error(),
			req.TargetVersion,
			returnTo,
		)
		return
	}

	s.redirectRuntimeControlWithMessage(
		w,
		r,
		queryNotice,
		runtimeLifecycleActionNotice(req),
		req.TargetVersion,
		returnTo,
	)
}

func (s *Service) redirectRuntimeControlWithMessage(
	w http.ResponseWriter,
	r *http.Request,
	key string,
	message string,
	version string,
	fragment string,
) {
	target := &url.URL{
		Path:     routeRuntimeControlPage,
		Fragment: strings.TrimSpace(fragment),
	}
	values := url.Values{}
	values.Set(key, message)
	version = strings.TrimSpace(version)
	if version != "" {
		values.Set(queryRuntimeVersion, version)
	}
	target.RawQuery = values.Encode()
	http.Redirect(
		w,
		r,
		target.String(),
		http.StatusSeeOther,
	)
}

func runtimeLifecycleActionRequestFromForm(
	r *http.Request,
) (RuntimeLifecycleActionRequest, error) {
	if r == nil {
		return RuntimeLifecycleActionRequest{}, errRuntimeActionRequired(
			"request is required",
		)
	}
	if err := r.ParseForm(); err != nil {
		return RuntimeLifecycleActionRequest{}, errRuntimeActionRequired(
			err.Error(),
		)
	}

	kind := normalizeRuntimeLifecycleAction(
		r.FormValue(formRuntimeActionKind),
	)
	switch kind {
	case runtimeActionRestart, runtimeActionUpgrade:
	default:
		return RuntimeLifecycleActionRequest{}, errRuntimeActionRequired(
			"runtime action kind is required",
		)
	}

	mode := normalizeRuntimeLifecycleMode(
		r.FormValue(formRuntimeActionMode),
	)
	switch mode {
	case runtimeModeGraceful, runtimeModeForce:
	default:
		return RuntimeLifecycleActionRequest{}, errRuntimeActionRequired(
			"runtime action mode is required",
		)
	}

	req := RuntimeLifecycleActionRequest{
		Kind: kind,
		Mode: mode,
	}
	if kind == runtimeActionUpgrade {
		req.TargetVersion = strings.TrimSpace(
			r.FormValue(formRuntimeTargetVersion),
		)
	}
	return req, nil
}

func errRuntimeActionRequired(message string) error {
	return &runtimeLifecycleFormError{
		Message: strings.TrimSpace(message),
	}
}

type runtimeLifecycleFormError struct {
	Message string
}

func (e *runtimeLifecycleFormError) Error() string {
	if e == nil {
		return "runtime action is invalid"
	}
	return e.Message
}

func normalizeRuntimeLifecycleAction(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case runtimeActionRestart:
		return runtimeActionRestart
	case runtimeActionUpgrade:
		return runtimeActionUpgrade
	default:
		return ""
	}
}

func normalizeRuntimeLifecycleMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case runtimeModeGraceful:
		return runtimeModeGraceful
	case runtimeModeForce:
		return runtimeModeForce
	default:
		return ""
	}
}

func runtimeLifecycleActionNotice(
	req RuntimeLifecycleActionRequest,
) string {
	mode := "graceful"
	if req.Mode == runtimeModeForce {
		mode = "force"
	}
	switch req.Kind {
	case runtimeActionRestart:
		return "Requested " + mode + " restart."
	case runtimeActionUpgrade:
		target := strings.TrimSpace(req.TargetVersion)
		if target == "" {
			return "Requested " + mode + " upgrade to latest."
		}
		return "Requested " + mode + " switch to " + target + "."
	default:
		return "Requested runtime action."
	}
}
