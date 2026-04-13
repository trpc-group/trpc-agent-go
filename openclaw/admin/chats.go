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
	"sort"
	"strings"
	"time"
)

const routeChatsJSON = "/api/chats"

type ChatsProvider interface {
	ChatsStatus() (ChatsStatus, error)
}

type ChatsStatus struct {
	Enabled               bool       `json:"enabled"`
	Error                 string     `json:"error,omitempty"`
	TotalCount            int        `json:"total_count"`
	OverrideCount         int        `json:"override_count"`
	GlobalAssistantName   string     `json:"global_assistant_name,omitempty"`
	RuntimeAssistantName  string     `json:"runtime_assistant_name,omitempty"`
	ChatOverrideHelp      string     `json:"chat_override_help,omitempty"`
	GlobalAssistantSource string     `json:"global_assistant_source,omitempty"`
	Chats                 []ChatView `json:"chats,omitempty"`
}

type ChatView struct {
	BaseSessionID         string            `json:"base_session_id,omitempty"`
	DisplayLabel          string            `json:"display_label,omitempty"`
	Kind                  string            `json:"kind,omitempty"`
	KindLabel             string            `json:"kind_label,omitempty"`
	CurrentSessionID      string            `json:"current_session_id,omitempty"`
	RecallSessionID       string            `json:"recall_session_id,omitempty"`
	LastActivity          time.Time         `json:"last_activity,omitempty"`
	Epoch                 int64             `json:"epoch,omitempty"`
	EffectiveAssistant    string            `json:"effective_assistant,omitempty"`
	ChatAssistantOverride string            `json:"chat_assistant_override,omitempty"`
	NameSource            string            `json:"name_source,omitempty"`
	OverridesGlobal       bool              `json:"overrides_global"`
	PersonaID             string            `json:"persona_id,omitempty"`
	PersonaLabel          string            `json:"persona_label,omitempty"`
	PersonaPinned         bool              `json:"persona_pinned"`
	WorkspacePath         string            `json:"workspace_path,omitempty"`
	KnownUserIDs          []string          `json:"known_user_ids,omitempty"`
	History               []ChatSessionView `json:"history,omitempty"`
}

type ChatSessionView struct {
	SessionID    string    `json:"session_id,omitempty"`
	LastActivity time.Time `json:"last_activity,omitempty"`
}

func (s *Service) chatsStatus() ChatsStatus {
	if s == nil || s.cfg.Chats == nil {
		return ChatsStatus{}
	}
	status, err := s.cfg.Chats.ChatsStatus()
	if err != nil {
		return ChatsStatus{
			Enabled: true,
			Error:   strings.TrimSpace(err.Error()),
		}
	}
	status.Enabled = true
	status.TotalCount = len(status.Chats)
	status.OverrideCount = chatOverrideCount(status.Chats)
	return status
}

func (s *Service) handleChatsJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.chatsStatus())
}

func selectedChatID(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get(queryChatID))
}

func selectChatView(
	status ChatsStatus,
	selectedID string,
) *ChatView {
	selectedID = strings.TrimSpace(selectedID)
	if selectedID == "" {
		if len(status.Chats) == 0 {
			return nil
		}
		selectedID = strings.TrimSpace(status.Chats[0].BaseSessionID)
	}
	for i := range status.Chats {
		chatID := strings.TrimSpace(status.Chats[i].BaseSessionID)
		if chatID != selectedID {
			continue
		}
		selected := status.Chats[i]
		return &selected
	}
	return nil
}

func chatDisplayLabel(chat ChatView) string {
	label := strings.TrimSpace(chat.DisplayLabel)
	if label != "" {
		return label
	}
	return strings.TrimSpace(chat.BaseSessionID)
}

func chatKnownUsers(chat ChatView) string {
	if len(chat.KnownUserIDs) == 0 {
		return "-"
	}
	return strings.Join(chat.KnownUserIDs, ", ")
}

func chatNameSourceLabel(chat ChatView) string {
	source := strings.TrimSpace(chat.NameSource)
	if source != "" {
		return source
	}
	if strings.TrimSpace(chat.ChatAssistantOverride) != "" {
		return "chat override"
	}
	return "global default"
}

func chatOverrideSample(
	status ChatsStatus,
	limit int,
) []ChatView {
	overrides := make([]ChatView, 0, len(status.Chats))
	for _, chat := range status.Chats {
		if !chat.OverridesGlobal {
			continue
		}
		overrides = append(overrides, chat)
	}
	sort.SliceStable(overrides, func(i, j int) bool {
		return overrides[i].LastActivity.After(overrides[j].LastActivity)
	})
	if limit <= 0 || len(overrides) <= limit {
		return overrides
	}
	return overrides[:limit]
}

func chatOverrideCount(chats []ChatView) int {
	count := 0
	for _, chat := range chats {
		if chat.OverridesGlobal {
			count++
		}
	}
	return count
}

const chatsPageTemplateHTML = `
{{define "chatsPage"}}
    <section class="stats">
      <article class="card">
        <span class="stat-label">Tracked Chats</span>
        <span class="stat-value">{{.Chats.TotalCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Name Overrides</span>
        <span class="stat-value">{{.Chats.OverrideCount}}</span>
      </article>
      <article class="card">
        <span class="stat-label">Global Name</span>
        <span class="stat-value">
          {{if .Identity.EffectiveName}}
            {{.Identity.EffectiveName}}
          {{else}}
            -
          {{end}}
        </span>
      </article>
    </section>

    {{if .Chats.Enabled}}
    <section class="panels">
      <article class="card">
        <h2>Tracked Chats</h2>
        <p class="subtle">
          This view shows tracked chat state from the runtime. It is not
          a full transcript browser. Use it to inspect per-chat names,
          personas, workspaces, and recent session-line history.
        </p>
        {{if .Chats.Error}}
        <div class="notice err" style="margin-top: 12px;">
          {{.Chats.Error}}
        </div>
        {{end}}
        {{if .Chats.Chats}}
        <table>
          <thead>
            <tr>
              <th>Chat</th>
              <th>Assistant</th>
              <th>Persona</th>
              <th>Last Activity</th>
              <th>Open</th>
            </tr>
          </thead>
          <tbody>
            {{range .Chats.Chats}}
            <tr>
              <td>
                <strong>{{chatDisplayLabel .}}</strong>
                {{if .KindLabel}}
                  <br><span class="subtle">{{.KindLabel}}</span>
                {{end}}
              </td>
              <td>
                {{if .EffectiveAssistant}}
                  {{.EffectiveAssistant}}
                {{else}}
                  -
                {{end}}
                <br><span class="subtle">{{chatNameSourceLabel .}}</span>
              </td>
              <td>
                {{if .PersonaLabel}}
                  {{.PersonaLabel}}
                {{else if .PersonaID}}
                  {{.PersonaID}}
                {{else}}
                  -
                {{end}}
              </td>
              <td>{{formatTime .LastActivity}}</td>
              <td>
                <a
                  href="/chats?chat_id={{.BaseSessionID}}"
                >
                  inspect
                </a>
              </td>
            </tr>
            {{end}}
          </tbody>
        </table>
        {{else}}
        <p class="empty">No tracked chats are available yet.</p>
        {{end}}
      </article>

      <article class="card">
        <h2>Chat Detail</h2>
        {{if .SelectedChat}}
        <dl class="meta">
          <dt>Chat</dt>
          <dd><code>{{.SelectedChat.BaseSessionID}}</code></dd>
          <dt>Kind</dt>
          <dd>
            {{if .SelectedChat.KindLabel}}
              {{.SelectedChat.KindLabel}}
            {{else if .SelectedChat.Kind}}
              {{.SelectedChat.Kind}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Current Name</dt>
          <dd>
            {{if .SelectedChat.EffectiveAssistant}}
              {{.SelectedChat.EffectiveAssistant}}
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Name Source</dt>
          <dd>{{chatNameSourceLabel .SelectedChat}}</dd>
          <dt>Chat Override</dt>
          <dd>
            {{if .SelectedChat.ChatAssistantOverride}}
              {{.SelectedChat.ChatAssistantOverride}}
            {{else}}
              (unset)
            {{end}}
          </dd>
          <dt>Overrides Global</dt>
          <dd>{{.SelectedChat.OverridesGlobal}}</dd>
          <dt>Current Session</dt>
          <dd><code>{{.SelectedChat.CurrentSessionID}}</code></dd>
          <dt>Recall Session</dt>
          <dd>
            {{if .SelectedChat.RecallSessionID}}
              <code>{{.SelectedChat.RecallSessionID}}</code>
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Epoch</dt>
          <dd>{{.SelectedChat.Epoch}}</dd>
          <dt>Persona</dt>
          <dd>
            {{if .SelectedChat.PersonaLabel}}
              {{.SelectedChat.PersonaLabel}}
            {{else if .SelectedChat.PersonaID}}
              {{.SelectedChat.PersonaID}}
            {{else}}
              -
            {{end}}
            {{if .SelectedChat.PersonaPinned}}
              <br><span class="subtle">pinned for this chat</span>
            {{end}}
          </dd>
          <dt>Workspace</dt>
          <dd>
            {{if .SelectedChat.WorkspacePath}}
              <code>{{.SelectedChat.WorkspacePath}}</code>
            {{else}}
              -
            {{end}}
          </dd>
          <dt>Known Users</dt>
          <dd>{{chatKnownUsers .SelectedChat}}</dd>
          <dt>Last Activity</dt>
          <dd>{{formatTime .SelectedChat.LastActivity}}</dd>
          <dt>JSON</dt>
          <dd><a href="/api/chats">/api/chats</a></dd>
        </dl>

        <h3 style="margin: 18px 0 8px;">Tracked Session Lines</h3>
        {{if .SelectedChat.History}}
        <table>
          <thead>
            <tr>
              <th>Session ID</th>
              <th>Last Activity</th>
            </tr>
          </thead>
          <tbody>
            {{range .SelectedChat.History}}
            <tr>
              <td><code>{{.SessionID}}</code></td>
              <td>{{formatTime .LastActivity}}</td>
            </tr>
            {{end}}
          </tbody>
        </table>
        {{else}}
        <p class="empty">No session-line history has been tracked yet.</p>
        {{end}}
        {{else}}
        <p class="empty">
          Choose a tracked chat from the list to inspect its current
          state.
        </p>
        {{end}}
      </article>
    </section>
    {{else}}
    <section class="card" style="margin-top: 24px;">
      <p class="empty">Chat tracking is not available for this runtime.</p>
    </section>
    {{end}}
{{end}}
`
