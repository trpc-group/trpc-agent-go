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
          Set the assistant's global default name. This is the fallback
          name for new chats and for any existing chat that does not
          have its own chat-level override.
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
        This is the global fallback name. It can affect other users'
        private chats and other groups, but only when those chats do
        not already have their own chat-level override.
      </p>
      <div class="notice" style="margin-top: 14px;">
        <strong>How it works</strong><br>
        1. If one chat already renamed the bot inside that chat, that
        chat keeps using its own name.<br>
        2. If another user opens a new private chat and has not renamed
        the bot there yet, they will see this global name.<br>
        3. If another group never set its own override, it also falls
        back to this global name.
      </div>
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

    {{if .Chats.Enabled}}
    <section class="card" style="margin-top: 24px;" id="identity-chats">
      <h2>Chat Overrides</h2>
      <p class="subtle">
        A chat override only changes the bot's name inside one tracked
        chat. It wins over the global fallback, and it stays there until
        cleared, even if the user starts a new session epoch with
        <code>/new</code>.
      </p>
      <dl class="meta" style="margin-top: 14px;">
        <dt>Active Overrides</dt>
        <dd>{{.Chats.OverrideCount}}</dd>
        <dt>Tracked Chats</dt>
        <dd>{{.Chats.TotalCount}}</dd>
        <dt>Open</dt>
        <dd><a href="/chats">Chats</a></dd>
      </dl>
      {{if chatOverrideSample .Chats 6}}
      <table style="margin-top: 16px;">
        <thead>
          <tr>
            <th>Chat</th>
            <th>Current Name</th>
            <th>Override</th>
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
