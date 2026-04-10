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
	routeIdentityJSON = "/api/identity"
	routeIdentitySave = "/api/identity/save"

	formAssistantName = "assistant_name"
)

type IdentityProvider interface {
	IdentityStatus() (IdentityStatus, error)
	SaveAssistantName(name string) error
}

type IdentityStatus struct {
	Enabled        bool   `json:"enabled"`
	Error          string `json:"error,omitempty"`
	ConfiguredName string `json:"configured_name,omitempty"`
	EffectiveName  string `json:"effective_name,omitempty"`
	RuntimeProduct string `json:"runtime_product,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
	FallbackSource string `json:"fallback_source,omitempty"`
}

func (s *Service) identityStatus() IdentityStatus {
	if s == nil || s.cfg.Identity == nil {
		return IdentityStatus{}
	}
	status, err := s.cfg.Identity.IdentityStatus()
	if err != nil {
		return IdentityStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}
	status.Enabled = true
	return status
}

func (s *Service) handleIdentityJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.identityStatus())
}

func (s *Service) handleSaveIdentity(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, returnTo, ok := s.requireIdentityPOST(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Identity.SaveAssistantName(name); err != nil {
		s.redirectWithMessageAt(
			w,
			r,
			queryError,
			err.Error(),
			returnTo,
		)
		return
	}
	message := "Saved assistant name."
	if strings.TrimSpace(name) == "" {
		message = "Cleared assistant name."
	}
	s.redirectWithMessageAt(
		w,
		r,
		queryNotice,
		message,
		returnTo,
	)
}

func (s *Service) requireIdentityPOST(
	w http.ResponseWriter,
	r *http.Request,
) (string, string, bool) {
	if s == nil || s.cfg.Identity == nil {
		http.Error(w, "identity is not enabled", http.StatusNotFound)
		return "", "", false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", false
	}
	return r.FormValue(formAssistantName),
		strings.TrimSpace(r.FormValue(formReturnTo)),
		true
}

const identityPageTemplateHTML = `
{{define "identityPage"}}
    <section class="panels">
      <article class="card">
        <h2>Identity</h2>
        <p class="subtle">
          Set the assistant's global default name. This affects new
          chats unless a chat-specific name override is active.
        </p>
        <dl class="meta">
          <dt>Assistant Name</dt>
          <dd>
            {{if .Identity.EffectiveName}}
              {{.Identity.EffectiveName}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Runtime Product</dt>
          <dd>
            {{if .Identity.RuntimeProduct}}
              {{.Identity.RuntimeProduct}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>JSON</dt>
          <dd><a href="/api/identity">/api/identity</a></dd>
        </dl>
      </article>
    </section>

    {{if .Identity.Enabled}}
    <section class="card" style="margin-top: 24px;" id="identity-global">
      <h2>Assistant Name</h2>
      <p class="subtle">
        This is the global default name. Chat-scoped overrides can still
        replace it for one active conversation.
      </p>
      {{if .Identity.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Identity.Error}}
      </div>
      {{end}}
      <dl class="meta" style="margin-top: 14px;">
        <dt>Effective Name</dt>
        <dd>
          {{if .Identity.EffectiveName}}
            {{.Identity.EffectiveName}}
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Configured Global Name</dt>
        <dd>
          {{if .Identity.ConfiguredName}}
            {{.Identity.ConfiguredName}}
          {{else}}
            (unset)
          {{end}}
        </dd>
        <dt>Fallback Source</dt>
        <dd>
          {{if .Identity.FallbackSource}}
            {{.Identity.FallbackSource}}
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Identity File</dt>
        <dd>
          {{if .Identity.SourcePath}}
            <code>{{.Identity.SourcePath}}</code>
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Runtime Product</dt>
        <dd>
          {{if .Identity.RuntimeProduct}}
            {{.Identity.RuntimeProduct}}
          {{else}}
            -
          {{end}}
        </dd>
      </dl>
      <form method="post" action="/api/identity/save" style="margin-top: 16px;">
        <input type="hidden" name="return_path" value="/identity">
        <input type="hidden" name="return_to" value="identity-global">
        <label for="assistant-name">Assistant Name</label>
        <input
          id="assistant-name"
          type="text"
          name="assistant_name"
          value="{{.Identity.ConfiguredName}}"
          placeholder="Leave empty to fall back"
          style="width: 100%; margin-top: 8px;"
        >
        <div class="actions" style="margin-top: 12px;">
          <button type="submit">Save Assistant Name</button>
        </div>
      </form>
    </section>
    {{else}}
    <section class="card" style="margin-top: 24px;">
      <p class="empty">Identity management is not available for this runtime.</p>
    </section>
    {{end}}
{{end}}
`
