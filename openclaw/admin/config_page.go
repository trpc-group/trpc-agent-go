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
	"strings"
)

const (
	routeConfigPage  = "/config"
	routeConfigJSON  = "/api/config"
	routeConfigSave  = "/api/config/save"
	routeConfigReset = "/api/config/reset"

	formConfigFieldKey = "field_key"
	formConfigValue    = "value"

	configInputText     = "text"
	configInputNumber   = "number"
	configInputSelect   = "select"
	configInputReadOnly = "readonly"

	configApplyHot        = "hot"
	configApplyNextTurn   = "next_turn"
	configApplyRestart    = "restart"
	configSourceExplicit  = "explicit"
	configSourceInherited = "inherited"
)

const pageSummaryConfig = "" +
	"Edit major runtime config fields, compare the saved config with " +
	"the current runtime, and reset fields back to inheritance."

const (
	viewConfig adminView = "config"
)

type RuntimeConfigProvider interface {
	RuntimeConfigStatus() (RuntimeConfigStatus, error)
	SaveRuntimeConfigValue(key string, value string) error
	ResetRuntimeConfigValue(key string) error
}

type RuntimeConfigStatus struct {
	Enabled    bool                   `json:"enabled"`
	Error      string                 `json:"error,omitempty"`
	ConfigPath string                 `json:"config_path,omitempty"`
	Sections   []RuntimeConfigSection `json:"sections,omitempty"`
}

type RuntimeConfigSection struct {
	Key     string               `json:"key,omitempty"`
	Title   string               `json:"title,omitempty"`
	Summary string               `json:"summary,omitempty"`
	Fields  []RuntimeConfigField `json:"fields,omitempty"`
}

type RuntimeConfigField struct {
	Key                   string                `json:"key,omitempty"`
	Title                 string                `json:"title,omitempty"`
	Summary               string                `json:"summary,omitempty"`
	InputType             string                `json:"input_type,omitempty"`
	Placeholder           string                `json:"placeholder,omitempty"`
	ApplyMode             string                `json:"apply_mode,omitempty"`
	EditorValue           string                `json:"editor_value,omitempty"`
	ConfiguredValue       string                `json:"configured_value,omitempty"`
	ConfiguredSourceLabel string                `json:"configured_source_label,omitempty"`
	ConfiguredSource      string                `json:"configured_source,omitempty"`
	RuntimeValue          string                `json:"runtime_value,omitempty"`
	RuntimeSourceLabel    string                `json:"runtime_source_label,omitempty"`
	PendingRestart        bool                  `json:"pending_restart"`
	Resettable            bool                  `json:"resettable"`
	Options               []RuntimeConfigOption `json:"options,omitempty"`
}

type RuntimeConfigOption struct {
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
}

func (s *Service) runtimeConfigStatus() RuntimeConfigStatus {
	provider := s.runtimeConfigProvider()
	if provider == nil {
		return RuntimeConfigStatus{}
	}
	status, err := provider.RuntimeConfigStatus()
	if err != nil {
		return RuntimeConfigStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}
	status.Enabled = true
	return status
}

func (s *Service) runtimeConfigProvider() RuntimeConfigProvider {
	if s == nil {
		return nil
	}
	return s.runtimeConfig
}

func (s *Service) hasRuntimeConfigProvider() bool {
	return s.runtimeConfigProvider() != nil
}

func (s *Service) handleConfigPage(
	w http.ResponseWriter,
	r *http.Request,
) {
	s.renderPage(w, r, viewConfig)
}

func (s *Service) handleConfigJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.runtimeConfigStatus())
}

func (s *Service) handleSaveRuntimeConfig(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	returnTo := strings.TrimSpace(r.FormValue(formReturnTo))
	provider := s.runtimeConfigProvider()
	if provider == nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"runtime config provider is not available",
			returnTo,
		)
		return
	}
	key := strings.TrimSpace(r.FormValue(formConfigFieldKey))
	if key == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"config field is required",
			returnTo,
		)
		return
	}
	value := strings.TrimSpace(r.FormValue(formConfigValue))
	if err := provider.SaveRuntimeConfigValue(key, value); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Saved config field. Restart the runtime to apply "+
			"restart-bound changes.",
		returnTo,
	)
}

func (s *Service) handleResetRuntimeConfig(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	returnTo := strings.TrimSpace(r.FormValue(formReturnTo))
	provider := s.runtimeConfigProvider()
	if provider == nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"runtime config provider is not available",
			returnTo,
		)
		return
	}
	key := strings.TrimSpace(r.FormValue(formConfigFieldKey))
	if key == "" {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			"config field is required",
			returnTo,
		)
		return
	}
	if err := provider.ResetRuntimeConfigValue(key); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		"Reset config field to inherited behavior.",
		returnTo,
	)
}
