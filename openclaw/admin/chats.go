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
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	routeChatsJSON       = "/api/chats"
	routeChatHistoryJSON = "/api/chats/history"

	knownUserSeparator = ", "

	chatHistoryItemKindSession = "session"
	chatHistoryItemKindTurn    = "turn"
)

type ChatsProvider interface {
	ChatsStatus() (ChatsStatus, error)
}

type ChatDetailProvider interface {
	ChatDetail(baseSessionID string) (ChatView, error)
}

type ChatHistoryProvider interface {
	ChatHistory(
		baseSessionID string,
		cursor string,
	) (ChatHistoryPage, error)
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
	HistoryTotalCount     int                  `json:"history_total_count"`
	HistoryTruncated      bool                 `json:"history_truncated"`
	History               []ChatSessionView    `json:"history,omitempty"`
	TranscriptTruncated   bool                 `json:"transcript_truncated"`
	Transcript            []ChatTranscriptView `json:"transcript,omitempty"`
}

type ChatHistoryPage struct {
	BaseSessionID     string            `json:"base_session_id,omitempty"`
	SessionLineCount  int               `json:"session_line_count"`
	TurnCount         int               `json:"turn_count"`
	ReturnedTurnCount int               `json:"returned_turn_count"`
	NextCursor        string            `json:"next_cursor,omitempty"`
	Bounded           bool              `json:"bounded"`
	Items             []ChatHistoryItem `json:"items,omitempty"`
}

type ChatHistoryItem struct {
	Kind         string    `json:"kind,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	SessionLabel string    `json:"session_label,omitempty"`
	LastActivity time.Time `json:"last_activity,omitempty"`
	Current      bool      `json:"current"`
	Recall       bool      `json:"recall"`
	Role         string    `json:"role,omitempty"`
	Speaker      string    `json:"speaker,omitempty"`
	QuoteText    string    `json:"quote_text,omitempty"`
	Text         string    `json:"text,omitempty"`
	Timestamp    time.Time `json:"timestamp,omitempty"`
}

type KnownUserView struct {
	UserID string `json:"user_id,omitempty"`
	Label  string `json:"label,omitempty"`
}

type ChatSessionView struct {
	SessionID    string    `json:"session_id,omitempty"`
	LastActivity time.Time `json:"last_activity,omitempty"`
	Visible      bool      `json:"visible"`
}

type ChatTranscriptView struct {
	SessionID    string         `json:"session_id,omitempty"`
	LastActivity time.Time      `json:"last_activity,omitempty"`
	Current      bool           `json:"current"`
	Recall       bool           `json:"recall"`
	Visible      bool           `json:"visible"`
	Truncated    bool           `json:"truncated"`
	Turns        []ChatTurnView `json:"turns,omitempty"`
}

type ChatTurnView struct {
	Role      string    `json:"role,omitempty"`
	Speaker   string    `json:"speaker,omitempty"`
	QuoteText string    `json:"quote_text,omitempty"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Visible   bool      `json:"visible"`
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

func (s *Service) handleChatHistoryJSON(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.cfg.Chats == nil {
		http.Error(w, "chat history not available", http.StatusNotFound)
		return
	}
	provider, ok := s.cfg.Chats.(ChatHistoryProvider)
	if !ok {
		http.Error(w, "chat history not available", http.StatusNotFound)
		return
	}
	baseSessionID := selectedChatID(r)
	if baseSessionID == "" {
		http.Error(w, "chat_id is required", http.StatusBadRequest)
		return
	}
	cursor := strings.TrimSpace(r.URL.Query().Get(queryCursor))
	page, err := provider.ChatHistory(baseSessionID, cursor)
	if err != nil {
		http.Error(
			w,
			strings.TrimSpace(err.Error()),
			http.StatusInternalServerError,
		)
		return
	}
	writeJSON(w, http.StatusOK, page)
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

func chatHistoryAPIPath() string {
	return routeChatHistoryJSON
}

func chatVisibleHistory(chat ChatView) []ChatSessionView {
	return visibleChatSessions(chat.History, true)
}

func chatHiddenHistory(chat ChatView) []ChatSessionView {
	return visibleChatSessions(chat.History, false)
}

func visibleChatSessions(
	sessions []ChatSessionView,
	visible bool,
) []ChatSessionView {
	if len(sessions) == 0 {
		return nil
	}
	result := make([]ChatSessionView, 0, len(sessions))
	for _, session := range sessions {
		if session.Visible != visible {
			continue
		}
		result = append(result, session)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func chatVisibleTranscript(chat ChatView) []ChatTranscriptView {
	return visibleChatTranscript(chat.Transcript, true)
}

func chatHiddenTranscript(chat ChatView) []ChatTranscriptView {
	return visibleChatTranscript(chat.Transcript, false)
}

func visibleChatTranscript(
	transcript []ChatTranscriptView,
	visible bool,
) []ChatTranscriptView {
	if len(transcript) == 0 {
		return nil
	}
	result := make([]ChatTranscriptView, 0, len(transcript))
	for _, view := range transcript {
		if view.Visible != visible {
			continue
		}
		result = append(result, view)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func chatVisibleTurns(view ChatTranscriptView) []ChatTurnView {
	return visibleChatTurns(view.Turns, true)
}

func chatHiddenTurns(view ChatTranscriptView) []ChatTurnView {
	return visibleChatTurns(view.Turns, false)
}

func visibleChatTurns(
	turns []ChatTurnView,
	visible bool,
) []ChatTurnView {
	if len(turns) == 0 {
		return nil
	}
	result := make([]ChatTurnView, 0, len(turns))
	for _, turn := range turns {
		if turn.Visible != visible {
			continue
		}
		result = append(result, turn)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func chatTranscriptSummary(chat ChatView) string {
	sessionCount := len(chat.Transcript)
	turnCount := 0
	for _, transcript := range chat.Transcript {
		turnCount += len(transcript.Turns)
	}
	switch {
	case sessionCount == 0:
		return "No recent transcript is currently available."
	case turnCount == 0:
		return fmt.Sprintf("%d recent session lines", sessionCount)
	default:
		return fmt.Sprintf(
			"%d recent session lines · %d visible turns",
			sessionCount,
			turnCount,
		)
	}
}

func chatHistorySummary(chat ChatView) string {
	count := chat.HistoryTotalCount
	switch {
	case count <= 0:
		return "No tracked sessions are currently available."
	case count == 1:
		return "1 tracked session line"
	default:
		return fmt.Sprintf("%d tracked session lines", count)
	}
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
          {{$visibleHistory := chatVisibleHistory .SelectedChat}}
          {{$hiddenHistory := chatHiddenHistory .SelectedChat}}
          <table>
            <thead>
              <tr>
                <th>Session ID</th>
                <th>Last Activity</th>
              </tr>
            </thead>
            <tbody>
              {{range $visibleHistory}}
              <tr>
                <td><code>{{.SessionID}}</code></td>
                <td>{{formatTime .LastActivity}}</td>
              </tr>
              {{end}}
            </tbody>
          </table>
          {{if $hiddenHistory}}
          <details class="chat-disclosure-more">
            <summary>
              Show {{len $hiddenHistory}} older tracked sessions
            </summary>
            <div class="chat-disclosure-body">
              <table>
                <thead>
                  <tr>
                    <th>Session ID</th>
                    <th>Last Activity</th>
                  </tr>
                </thead>
                <tbody>
                  {{range $hiddenHistory}}
                  <tr>
                    <td><code>{{.SessionID}}</code></td>
                    <td>{{formatTime .LastActivity}}</td>
                  </tr>
                  {{end}}
                </tbody>
              </table>
            </div>
          </details>
          {{end}}
          {{if .SelectedChat.HistoryTruncated}}
          <p class="subtle" style="margin-top: 10px;">
            Showing the most recent tracked sessions in this admin view.
          </p>
          {{end}}
          {{else}}
          <p class="empty">No recent sessions have been tracked yet.</p>
          {{end}}
        </section>

        <section class="chat-detail-section" id="chat-history">
          <details class="chat-disclosure">
            <summary>
              <h3>History</h3>
              <p class="subtle">
                Load the newest visible messages for this chat, then
                keep scrolling upward into older conversation history.
              </p>
              <p class="subtle chat-disclosure-meta">
                {{chatHistorySummary .SelectedChat}}
              </p>
            </summary>
            <div class="chat-disclosure-body">
              {{if .SelectedChatError}}
              <div class="notice err">{{.SelectedChatError}}</div>
              {{else}}
              <div
                class="chat-history-shell"
                data-chat-history-root
                data-chat-history-path="{{chatHistoryAPIPath}}"
                data-chat-id="{{.SelectedChat.BaseSessionID}}"
              >
                <p class="subtle chat-history-status"
                  data-chat-history-meta>
                  Expand this panel to load the newest visible
                  messages for this chat.
                </p>
                <div class="chat-history-toolbar"
                  data-chat-history-toolbar hidden>
                  <button type="button" class="chat-history-more"
                    data-chat-history-more hidden>
                    Load older messages
                  </button>
                </div>
                <div class="notice err"
                  data-chat-history-error hidden></div>
                <p class="empty"
                  data-chat-history-empty hidden></p>
                <p class="subtle chat-history-bounded"
                  data-chat-history-bounded hidden></p>
                <div class="chat-timeline"
                  data-chat-history-items></div>
              </div>
              {{end}}
            </div>
          </details>
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
