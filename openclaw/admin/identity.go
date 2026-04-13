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
	message := "Saved default name."
	if strings.TrimSpace(name) == "" {
		message = "Cleared default name."
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
        <h2>Default Name</h2>
        <p class="subtle">
          This is the default name used by new chats and by any existing
          chat that has not picked its own current name yet.
        </p>
        <dl class="meta">
          <dt>Default Name</dt>
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
      <h2>How Naming Works</h2>
      <p class="subtle">
        There are only two rules:
      </p>
      <div class="notice" style="margin-top: 14px;">
        <strong>1. Current chat name wins.</strong><br>
        If one chat already renamed the bot, that chat keeps using its
        own name.<br><br>
        <strong>2. Default name fills the gaps.</strong><br>
        New chats, and old chats that never picked their own name, fall
        back to the default name shown here.<br><br>
        <strong>Commands.</strong><br>
        Use <code>/name &lt;name&gt;</code> to rename the bot inside one
        chat. Use <code>/name global &lt;name&gt;</code> to change the
        default name.
      </div>
      {{if .Identity.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Identity.Error}}
      </div>
      {{end}}
      <dl class="meta" style="margin-top: 14px;">
        <dt>Current Default Name</dt>
        <dd>
          {{if .Identity.EffectiveName}}
            {{.Identity.EffectiveName}}
          {{else}}
            -
          {{end}}
        </dd>
        <dt>Configured Default Name</dt>
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
        <label for="assistant-name">Default Name</label>
        <input
          id="assistant-name"
          type="text"
          name="assistant_name"
          value="{{.Identity.ConfiguredName}}"
          placeholder="Leave empty to fall back"
          style="width: 100%; margin-top: 8px;"
        >
        <div class="actions" style="margin-top: 12px;">
          <button type="submit">Save Default Name</button>
        </div>
      </form>
    </section>

    {{if .Chats.Enabled}}
    <section class="card" style="margin-top: 24px;" id="identity-chats">
      <h2>Chats Using Their Own Name</h2>
      <p class="subtle">
        These chats are not using the default name. Each one already has
        its own current chat name, so changing the default name will not
        replace it.
      </p>
      <dl class="meta" style="margin-top: 14px;">
        <dt>Chats Using Their Own Name</dt>
        <dd>{{.Chats.OverrideCount}}</dd>
        <dt>Tracked Chats</dt>
        <dd>{{.Chats.TotalCount}}</dd>
        <dt>Open</dt>
        <dd><a href="/chats">Chats</a></dd>
      </dl>
      {{if .Chats.Error}}
      <div class="notice err" style="margin-top: 12px;">
        {{.Chats.Error}}
      </div>
      {{else if chatOverrideSample .Chats 6}}
      <table style="margin-top: 16px;">
        <thead>
          <tr>
            <th>Chat</th>
            <th>Current Name</th>
            <th>Current Chat Name</th>
            <th>Last Activity</th>
          </tr>
        </thead>
        <tbody>
          {{range chatOverrideSample .Chats 6}}
          <tr>
            <td>
              <a href="/chats?chat_id={{.BaseSessionID}}">
                {{chatDisplayLabel .}}
              </a>
            </td>
            <td>
              {{if .EffectiveAssistant}}
                {{.EffectiveAssistant}}
              {{else}}
                -
              {{end}}
            </td>
            <td>
              {{if .ChatAssistantOverride}}
                {{.ChatAssistantOverride}}
              {{else}}
                -
              {{end}}
            </td>
            <td>{{formatTime .LastActivity}}</td>
          </tr>
          {{end}}
        </tbody>
      </table>
      {{else}}
      <p class="empty">No chat-specific name overrides are active.</p>
      {{end}}
    </section>
    {{end}}
    {{else}}
    <section class="card" style="margin-top: 24px;">
      <p class="empty">Identity management is not available for this runtime.</p>
    </section>
    {{end}}
{{end}}
`
