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

const (
	routeChatsJSON = "/api/chats"

	knownUserSeparator = ", "
)

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
	KnownUsers            []KnownUserView   `json:"known_users,omitempty"`
	History               []ChatSessionView `json:"history,omitempty"`
}

type KnownUserView struct {
	UserID string `json:"user_id,omitempty"`
	Label  string `json:"label,omitempty"`
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
	if len(chat.KnownUsers) != 0 {
		parts := make([]string, 0, len(chat.KnownUsers))
		for _, user := range chat.KnownUsers {
			label := chatKnownUserLabel(user)
			if label == "" {
				continue
			}
			parts = append(parts, label)
		}
		if len(parts) != 0 {
			return strings.Join(parts, knownUserSeparator)
		}
	}
	if len(chat.KnownUserIDs) == 0 {
		return "-"
	}
	return strings.Join(chat.KnownUserIDs, knownUserSeparator)
}

func chatKnownUserLabel(user KnownUserView) string {
	userID := strings.TrimSpace(user.UserID)
	label := strings.TrimSpace(user.Label)
	switch {
	case label != "" && userID != "" && label != userID:
		return label + " (" + userID + ")"
	case label != "":
		return label
	default:
		return userID
	}
}

func chatNameSourceLabel(chat ChatView) string {
	source := strings.TrimSpace(chat.NameSource)
	if source != "" {
		return source
	}
	if strings.TrimSpace(chat.ChatAssistantOverride) != "" {
		return "Current chat name"
	}
	return "Default name"
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
            <a href="/identity#identity-global">
              {{.Identity.EffectiveName}}
            </a>
          {{else}}
            <a href="/identity#identity-global">-</a>
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
          a full transcript browser. Use it to inspect each chat's
          current name, whether that chat is using its own name or the
          default name, plus persona, workspace, and recent sessions.
        </p>
        {{if .Chats.ChatOverrideHelp}}
        <div class="notice" style="margin-top: 14px;">
          {{.Chats.ChatOverrideHelp}}
        </div>
        {{end}}
        {{if .Chats.Error}}
        <div class="notice err" style="margin-top: 12px;">
          {{.Chats.Error}}
        </div>
        {{end}}
        {{if .Chats.Chats}}
        <div class="chat-list">
          {{range .Chats.Chats}}
          <article class="chat-card">
            <div class="chat-card-head">
              <div class="chat-card-copy">
                <div class="chat-card-title">{{chatDisplayLabel .}}</div>
                <div class="chat-card-kind">
                  {{if .KindLabel}}
                    {{.KindLabel}}
                  {{else if .Kind}}
                    {{.Kind}}
                  {{else}}
                    Tracked chat
                  {{end}}
                </div>
              </div>
              <div class="chat-card-link">
                <a href="/chats?chat_id={{.BaseSessionID}}">inspect</a>
              </div>
            </div>
            <div class="chat-card-grid">
              <div class="chat-card-meta">
                <div class="chat-card-label">Current Name</div>
                <div class="chat-card-value">
                  {{if .EffectiveAssistant}}
                    {{.EffectiveAssistant}}
                  {{else}}
                    -
                  {{end}}
                </div>
              </div>
              <div class="chat-card-meta">
                <div class="chat-card-label">Name Source</div>
                <div class="chat-card-value">
                  {{chatNameSourceLabel .}}
                </div>
              </div>
              <div class="chat-card-meta">
                <div class="chat-card-label">Persona</div>
                <div class="chat-card-value">
                  {{if .PersonaLabel}}
                    {{.PersonaLabel}}
                  {{else if .PersonaID}}
                    {{.PersonaID}}
                  {{else}}
                    -
                  {{end}}
                </div>
              </div>
              <div class="chat-card-meta">
                <div class="chat-card-label">Last Activity</div>
                <div class="chat-card-value">
                  {{formatTime .LastActivity}}
                </div>
              </div>
            </div>
          </article>
          {{end}}
        </div>
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
          <dt>Why This Name</dt>
          <dd>{{chatNameSourceLabel .SelectedChat}}</dd>
          <dt>Current Chat Name</dt>
          <dd>
            {{if .SelectedChat.ChatAssistantOverride}}
              {{.SelectedChat.ChatAssistantOverride}}
            {{else}}
              (using default name)
            {{end}}
          </dd>
          <dt>Using Its Own Name</dt>
          <dd>
            {{if .SelectedChat.OverridesGlobal}}
              yes
            {{else}}
              no
            {{end}}
          </dd>
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

        <h3 style="margin: 18px 0 8px;">Recent Sessions</h3>
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
        <p class="empty">No recent sessions have been tracked yet.</p>
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
