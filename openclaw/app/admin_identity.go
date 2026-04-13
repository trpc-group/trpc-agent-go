//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	adminIdentityFileName = "IDENTITY.md"

	adminIdentityFilePerm = 0o600
	adminIdentityDirPerm  = 0o700

	adminAssistantNameMaxRunes = 32

	adminIdentityFallbackRuntime = "runtime product"

	adminDefaultNameSourceFile = "Default name from IDENTITY.md"
	adminDefaultNameSourceApp  = "Default name from runtime product"

	adminChatsHelpText = "" +
		"This runtime uses one default name across chats. " +
		"The Chats page shows tracked conversation scopes and " +
		"their recent history, but it does not keep a separate " +
		"current name for each chat."

	adminIdentityTrimCutset = "" +
		"\"'“”‘’<>《》「」『』【】()（）[]"

	adminChatKindTracked = "tracked"
	adminChatKindLabel   = "Tracked chat"

	adminChatHistorySessionLimit    = 12
	adminChatHistoryVisibleCount    = 5
	adminChatTranscriptSessionLimit = 8
	adminChatTranscriptVisibleCount = 2
	adminChatTranscriptTurnLimit    = 18
	adminChatTranscriptTurnVisible  = 6
	adminChatTranscriptTextLimit    = 1500
)

type adminIdentityProvider struct {
	filePath       string
	runtimeProduct string
}

type adminChatsProvider struct {
	identity *adminIdentityProvider
	appName  string
	session  session.Service
}

func buildAdminIdentityProvider(
	stateDir string,
	runtimeProduct string,
) *adminIdentityProvider {
	return &adminIdentityProvider{
		filePath: filepath.Join(
			strings.TrimSpace(stateDir),
			adminIdentityFileName,
		),
		runtimeProduct: defaultAdminRuntimeProduct(runtimeProduct),
	}
}

func buildAdminChatsProvider(
	identity *adminIdentityProvider,
	appName string,
	sessionSvc session.Service,
) *adminChatsProvider {
	if identity == nil {
		return nil
	}
	return &adminChatsProvider{
		identity: identity,
		appName:  strings.TrimSpace(appName),
		session:  sessionSvc,
	}
}

func defaultAdminRuntimeProduct(raw string) string {
	product := strings.TrimSpace(raw)
	if product != "" {
		return product
	}
	return appName
}

func normalizeAdminAssistantName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	trimmed = strings.Trim(trimmed, adminIdentityTrimCutset)
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return ""
	}

	fields := strings.Fields(trimmed)
	if len(fields) != 0 {
		trimmed = strings.Join(fields, " ")
	}

	runes := []rune(trimmed)
	if len(runes) > adminAssistantNameMaxRunes {
		runes = runes[:adminAssistantNameMaxRunes]
	}
	return strings.TrimSpace(string(runes))
}

func readAdminAssistantName(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return normalizeAdminAssistantName(string(data)), nil
}

func writeAdminAssistantName(
	path string,
	name string,
) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("admin identity path is unavailable")
	}
	if err := os.MkdirAll(
		filepath.Dir(path),
		adminIdentityDirPerm,
	); err != nil {
		return err
	}

	body := ""
	name = normalizeAdminAssistantName(name)
	if name != "" {
		body = name + "\n"
	}
	return os.WriteFile(
		path,
		[]byte(body),
		adminIdentityFilePerm,
	)
}

func identityDefaultNameSource(
	status admin.IdentityStatus,
) string {
	if strings.TrimSpace(status.ConfiguredName) != "" {
		return adminDefaultNameSourceFile
	}
	return adminDefaultNameSourceApp
}

func (p *adminIdentityProvider) IdentityStatus() (
	admin.IdentityStatus,
	error,
) {
	if p == nil {
		return admin.IdentityStatus{}, nil
	}

	configured, err := readAdminAssistantName(p.filePath)
	if err != nil {
		return admin.IdentityStatus{}, err
	}

	effective := configured
	fallbackSource := ""
	if effective == "" {
		effective = p.runtimeProduct
		fallbackSource = adminIdentityFallbackRuntime
	}

	return admin.IdentityStatus{
		Enabled:        true,
		ConfiguredName: configured,
		EffectiveName:  effective,
		RuntimeProduct: p.runtimeProduct,
		SourcePath:     strings.TrimSpace(p.filePath),
		FallbackSource: fallbackSource,
	}, nil
}

func (p *adminIdentityProvider) SaveAssistantName(name string) error {
	if p == nil {
		return fmt.Errorf("identity provider is unavailable")
	}
	return writeAdminAssistantName(p.filePath, name)
}

func (p *adminChatsProvider) ChatsStatus() (
	admin.ChatsStatus,
	error,
) {
	if p == nil {
		return admin.ChatsStatus{}, nil
	}

	identity, err := p.identity.IdentityStatus()
	if err != nil {
		return admin.ChatsStatus{}, err
	}
	chats, err := p.chatViews(
		strings.TrimSpace(identity.EffectiveName),
		identityDefaultNameSource(identity),
	)
	if err != nil {
		return admin.ChatsStatus{}, err
	}

	return admin.ChatsStatus{
		Enabled:               true,
		GlobalAssistantName:   strings.TrimSpace(identity.EffectiveName),
		RuntimeAssistantName:  strings.TrimSpace(identity.RuntimeProduct),
		GlobalAssistantSource: identityDefaultNameSource(identity),
		ChatOverrideHelp:      adminChatsHelpText,
		Chats:                 chats,
	}, nil
}

func (p *adminChatsProvider) ChatDetail(
	baseSessionID string,
) (admin.ChatView, error) {
	baseSessionID = strings.TrimSpace(baseSessionID)
	if baseSessionID == "" {
		return admin.ChatView{}, fmt.Errorf("chat_id is required")
	}
	if p == nil || p.identity == nil {
		return admin.ChatView{}, fmt.Errorf("chat provider is unavailable")
	}

	identity, err := p.identity.IdentityStatus()
	if err != nil {
		return admin.ChatView{}, err
	}
	sessions, err := p.chatSessions(baseSessionID)
	if err != nil {
		return admin.ChatView{}, err
	}
	if len(sessions) == 0 {
		return admin.ChatView{}, fmt.Errorf("tracked chat not found")
	}

	detail := buildAdminTrackedChatView(
		baseSessionID,
		strings.TrimSpace(identity.EffectiveName),
		identityDefaultNameSource(identity),
		sessions,
	)
	detail.Transcript, detail.TranscriptTruncated, err =
		buildAdminChatTranscript(
			strings.TrimSpace(p.appName),
			p.session,
			baseSessionID,
			sessions,
		)
	if err != nil {
		return admin.ChatView{}, err
	}
	return detail, nil
}

func (p *adminChatsProvider) chatViews(
	defaultName string,
	defaultSource string,
) ([]admin.ChatView, error) {
	scopes, err := conversationscope.ListIndexedStorageScopes(
		context.Background(),
		p.session,
		p.appName,
	)
	if err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		return nil, nil
	}

	chats := make([]admin.ChatView, 0, len(scopes))
	for _, scope := range scopes {
		sessions, err := p.chatSessions(scope)
		if err != nil {
			return nil, err
		}
		if len(sessions) == 0 {
			continue
		}
		chats = append(
			chats,
			buildAdminTrackedChatView(
				scope,
				defaultName,
				defaultSource,
				sessions,
			),
		)
	}
	sort.Slice(chats, func(i int, j int) bool {
		left := chats[i].LastActivity
		right := chats[j].LastActivity
		switch {
		case left.Equal(right):
			return chats[i].BaseSessionID < chats[j].BaseSessionID
		case right.IsZero():
			return true
		case left.IsZero():
			return false
		default:
			return left.After(right)
		}
	})
	if len(chats) == 0 {
		return nil, nil
	}
	return chats, nil
}

func (p *adminChatsProvider) chatSessions(
	baseSessionID string,
) ([]*session.Session, error) {
	baseSessionID = strings.TrimSpace(baseSessionID)
	if baseSessionID == "" || strings.TrimSpace(p.appName) == "" ||
		p.session == nil {
		return nil, nil
	}
	sessions, err := p.session.ListSessions(
		context.Background(),
		session.UserKey{
			AppName: p.appName,
			UserID:  baseSessionID,
		},
	)
	if err != nil {
		return nil, err
	}
	filtered := make([]*session.Session, 0, len(sessions))
	for _, sess := range sessions {
		if sess == nil || strings.TrimSpace(sess.ID) == "" {
			continue
		}
		if cron.IsRunSessionID(sess.ID) {
			continue
		}
		filtered = append(filtered, sess)
	}
	sort.Slice(filtered, func(i int, j int) bool {
		left := sessionActivityTime(filtered[i])
		right := sessionActivityTime(filtered[j])
		switch {
		case left.Equal(right):
			return filtered[i].ID < filtered[j].ID
		case right.IsZero():
			return true
		case left.IsZero():
			return false
		default:
			return left.After(right)
		}
	})
	if len(filtered) == 0 {
		return nil, nil
	}
	return filtered, nil
}

func buildAdminTrackedChatView(
	baseSessionID string,
	defaultName string,
	defaultSource string,
	sessions []*session.Session,
) admin.ChatView {
	totalHistoryCount := len(sessions)
	historyTruncated := false
	if len(sessions) > adminChatHistorySessionLimit {
		sessions = sessions[:adminChatHistorySessionLimit]
		historyTruncated = true
	}
	history := make([]admin.ChatSessionView, 0, len(sessions))
	for i, sess := range sessions {
		history = append(history, admin.ChatSessionView{
			SessionID:    strings.TrimSpace(sess.ID),
			LastActivity: sessionActivityTime(sess),
			Visible:      i < adminChatHistoryVisibleCount,
		})
	}
	currentSessionID := ""
	lastActivity := timeZero()
	if len(sessions) != 0 {
		currentSessionID = strings.TrimSpace(sessions[0].ID)
		lastActivity = sessionActivityTime(sessions[0])
	}
	return admin.ChatView{
		BaseSessionID:      strings.TrimSpace(baseSessionID),
		DisplayLabel:       strings.TrimSpace(baseSessionID),
		Kind:               adminChatKindTracked,
		KindLabel:          adminChatKindLabel,
		CurrentSessionID:   currentSessionID,
		LastActivity:       lastActivity,
		EffectiveAssistant: strings.TrimSpace(defaultName),
		NameSource:         strings.TrimSpace(defaultSource),
		HistoryTotalCount:  totalHistoryCount,
		HistoryTruncated:   historyTruncated,
		History:            history,
	}
}

func buildAdminChatTranscript(
	appName string,
	sessionSvc session.Service,
	baseSessionID string,
	sessions []*session.Session,
) ([]admin.ChatTranscriptView, bool, error) {
	if strings.TrimSpace(appName) == "" || sessionSvc == nil ||
		strings.TrimSpace(baseSessionID) == "" || len(sessions) == 0 {
		return nil, false, nil
	}
	truncated := false
	if len(sessions) > adminChatTranscriptSessionLimit {
		sessions = sessions[:adminChatTranscriptSessionLimit]
		truncated = true
	}
	transcript := make([]admin.ChatTranscriptView, 0, len(sessions))
	currentSessionID := strings.TrimSpace(sessions[0].ID)
	for i, sess := range sessions {
		view, ok, err := buildAdminChatTranscriptView(
			appName,
			sessionSvc,
			baseSessionID,
			currentSessionID,
			sess,
		)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		view.Visible = i < adminChatTranscriptVisibleCount ||
			view.Current || view.Recall
		transcript = append(transcript, view)
	}
	if len(transcript) == 0 {
		return nil, truncated, nil
	}
	return transcript, truncated, nil
}

func buildAdminChatTranscriptView(
	appName string,
	sessionSvc session.Service,
	baseSessionID string,
	currentSessionID string,
	sessionMeta *session.Session,
) (admin.ChatTranscriptView, bool, error) {
	if sessionMeta == nil {
		return admin.ChatTranscriptView{}, false, nil
	}
	sessionID := strings.TrimSpace(sessionMeta.ID)
	if sessionID == "" {
		return admin.ChatTranscriptView{}, false, nil
	}
	sess, err := sessionSvc.GetSession(
		context.Background(),
		session.Key{
			AppName:   strings.TrimSpace(appName),
			UserID:    strings.TrimSpace(baseSessionID),
			SessionID: sessionID,
		},
	)
	if err != nil {
		return admin.ChatTranscriptView{}, false, fmt.Errorf(
			"load chat transcript for %q: %w",
			sessionID,
			err,
		)
	}
	if sess == nil {
		return admin.ChatTranscriptView{}, false, fmt.Errorf(
			"load chat transcript for %q: session not found",
			sessionID,
		)
	}

	turns := conversation.BuildTurns(sess, conversation.TurnOptions{})
	truncated := false
	if len(turns) > adminChatTranscriptTurnLimit {
		turns = turns[len(turns)-adminChatTranscriptTurnLimit:]
		truncated = true
	}
	mapped := make([]admin.ChatTurnView, 0, len(turns))
	for _, turn := range turns {
		text := trimAdminChatTranscriptText(turn.Text)
		quote := trimAdminChatTranscriptText(turn.QuoteText)
		if strings.TrimSpace(text) == "" &&
			strings.TrimSpace(quote) == "" {
			continue
		}
		mapped = append(mapped, admin.ChatTurnView{
			Role:      strings.TrimSpace(turn.Role),
			Speaker:   strings.TrimSpace(turn.Speaker),
			QuoteText: quote,
			Text:      text,
			Timestamp: turn.Timestamp,
		})
	}
	if len(mapped) == 0 {
		return admin.ChatTranscriptView{}, false, nil
	}
	for i := range mapped {
		mapped[i].Visible = i >= len(mapped)-adminChatTranscriptTurnVisible
	}
	return admin.ChatTranscriptView{
		SessionID:    sessionID,
		LastActivity: sessionActivityTime(sessionMeta),
		Current:      sessionID == strings.TrimSpace(currentSessionID),
		Truncated:    truncated,
		Turns:        mapped,
	}, true, nil
}

func trimAdminChatTranscriptText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if utf8.RuneCountInString(text) <= adminChatTranscriptTextLimit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(
		string(runes[:adminChatTranscriptTextLimit]),
	) + "..."
}

func sessionActivityTime(sess *session.Session) time.Time {
	if sess == nil {
		return timeZero()
	}
	if !sess.UpdatedAt.IsZero() {
		return sess.UpdatedAt
	}
	return sess.CreatedAt
}

func timeZero() time.Time {
	return time.Time{}
}
