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

type ChatDetailProvider interface {
	ChatDetail(baseSessionID string) (ChatView, error)
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
	BaseSessionID         string               `json:"base_session_id,omitempty"`
	DisplayLabel          string               `json:"display_label,omitempty"`
	Kind                  string               `json:"kind,omitempty"`
	KindLabel             string               `json:"kind_label,omitempty"`
	CurrentSessionID      string               `json:"current_session_id,omitempty"`
	RecallSessionID       string               `json:"recall_session_id,omitempty"`
	LastActivity          time.Time            `json:"last_activity,omitempty"`
	Epoch                 int64                `json:"epoch,omitempty"`
	EffectiveAssistant    string               `json:"effective_assistant,omitempty"`
	ChatAssistantOverride string               `json:"chat_assistant_override,omitempty"`
	NameSource            string               `json:"name_source,omitempty"`
	OverridesGlobal       bool                 `json:"overrides_global"`
	PersonaID             string               `json:"persona_id,omitempty"`
	PersonaLabel          string               `json:"persona_label,omitempty"`
	PersonaPinned         bool                 `json:"persona_pinned"`
	WorkspacePath         string               `json:"workspace_path,omitempty"`
	KnownUserIDs          []string             `json:"known_user_ids,omitempty"`
	KnownUsers            []KnownUserView      `json:"known_users,omitempty"`
	History               []ChatSessionView    `json:"history,omitempty"`
	Transcript            []ChatTranscriptView `json:"transcript,omitempty"`
}

type KnownUserView struct {
	UserID string `json:"user_id,omitempty"`
	Label  string `json:"label,omitempty"`
}

type ChatSessionView struct {
	SessionID    string    `json:"session_id,omitempty"`
	LastActivity time.Time `json:"last_activity,omitempty"`
}

type ChatTranscriptView struct {
	SessionID    string         `json:"session_id,omitempty"`
	LastActivity time.Time      `json:"last_activity,omitempty"`
	Current      bool           `json:"current"`
	Recall       bool           `json:"recall"`
	Truncated    bool           `json:"truncated"`
	Turns        []ChatTurnView `json:"turns,omitempty"`
}

type ChatTurnView struct {
	Role      string    `json:"role,omitempty"`
	Speaker   string    `json:"speaker,omitempty"`
	QuoteText string    `json:"quote_text,omitempty"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
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

func chatHasTranscript(chat ChatView) bool {
	return len(chat.Transcript) != 0
}

func chatTranscriptLabel(view ChatTranscriptView) string {
	switch {
	case view.Current:
		return "Current session"
	case view.Recall:
		return "Recall session"
	default:
		return "Recent session"
	}
}

func chatTurnSpeaker(view ChatTurnView) string {
	speaker := strings.TrimSpace(view.Speaker)
	if speaker != "" {
		return speaker
	}
	switch strings.TrimSpace(view.Role) {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "system":
		return "System"
	default:
		return "Turn"
	}
}

func hasTime(value time.Time) bool {
	return !value.IsZero()
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
        <h2>Selected Chat</h2>
        {{if .SelectedChat}}
        <section class="chat-detail-section" id="chat-overview">
          <div class="chat-detail-head">
            <h3>Overview</h3>
            <p class="subtle">
              Current state for this chat, including the name it is
              actually using right now.
            </p>
          </div>
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
                (using the default name)
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

          <h4 style="margin: 18px 0 8px;">Recent Sessions</h4>
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
        </section>

        <section class="chat-detail-section" id="chat-history">
          <div class="chat-detail-head">
            <h3>History</h3>
            <p class="subtle">
              Recent visible turns from this chat's tracked session
              lines. Large transcripts are intentionally bounded here.
            </p>
          </div>
          {{if .SelectedChatError}}
          <div class="notice err">{{.SelectedChatError}}</div>
          {{else if chatHasTranscript .SelectedChat}}
          <div class="chat-transcript-list">
            {{range .SelectedChat.Transcript}}
            <article class="chat-transcript-card">
              <div class="chat-transcript-head">
                <div>
                  <div class="chat-transcript-title">
                    {{chatTranscriptLabel .}}
                  </div>
                  <div class="subtle">
                    <code>{{.SessionID}}</code>
                  </div>
                </div>
                <div class="subtle">{{formatTime .LastActivity}}</div>
              </div>
              <div class="chat-turn-list">
                {{range .Turns}}
                <article class="chat-turn">
                  <div class="chat-turn-head">
                    <span class="chat-turn-speaker">
                      {{chatTurnSpeaker .}}
                    </span>
                    {{if hasTime .Timestamp}}
                    <span class="subtle">{{formatTime .Timestamp}}</span>
                    {{end}}
                  </div>
                  {{if .QuoteText}}
                  <blockquote class="chat-turn-quote">
                    {{.QuoteText}}
                  </blockquote>
                  {{end}}
                  <div class="chat-turn-text">{{.Text}}</div>
                </article>
                {{end}}
              </div>
              {{if .Truncated}}
              <p class="subtle" style="margin-top: 10px;">
                Showing the most recent turns for this session line.
              </p>
              {{end}}
            </article>
            {{end}}
          </div>
          {{else}}
          <p class="empty">
            No recent transcript is available for this chat yet.
          </p>
          {{end}}
        </section>

        <section class="chat-detail-section" id="chat-actions">
          <div class="chat-detail-head">
            <h3>Actions</h3>
            <p class="subtle">
              Use the correct surface for the kind of name change you
              want to make.
            </p>
          </div>
          <div class="chat-action-grid">
            <article class="chat-action-card">
              <h4>Default Name</h4>
              <p class="subtle">
                Change the default name used by new chats and by chats
                that do not keep their own current-chat name.
              </p>
              <p><a href="/identity#identity-global">Open Identity</a></p>
            </article>
            <article class="chat-action-card">
              <h4>Current Chat Name</h4>
              {{if .SelectedChat.OverridesGlobal}}
              <p class="subtle">
                This chat is using its own current-chat name. Change or
                clear it from the chat itself if you want this chat to
                fall back to the default name again.
              </p>
              {{else}}
              <p class="subtle">
                This chat is already using the default name. Rename it
                from the chat itself if you want this chat to use its
                own name.
              </p>
              {{end}}
            </article>
            <article class="chat-action-card">
              <h4>Persona</h4>
              <p class="subtle">
                Persona defaults and templates live in the Personas
                admin view. Chat-specific persona state is shown in the
                overview above.
              </p>
              <p><a href="/personas">Open Personas</a></p>
            </article>
          </div>
        </section>
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
