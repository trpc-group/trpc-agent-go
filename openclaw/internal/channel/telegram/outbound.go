//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telegram

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	sessionDMPrefix     = channelID + ":dm:"
	sessionThreadPrefix = channelID + ":thread:"

	fileURLPrefix = "file://"

	mimePrefixImage = "image/"
	mimePrefixAudio = "audio/"
	mimePrefixVideo = "video/"
	mimePrefixText  = "text/"

	mimeVoiceOGG    = "audio/ogg"
	mimeOctetStream = "application/octet-stream"

	uploadModeDocument = "document"
	uploadModePhoto    = "photo"
	uploadModeAudio    = "audio"
	uploadModeVoice    = "voice"
	uploadModeVideo    = "video"

	workspaceFileFallback = "workspace-output"

	maxTelegramCaptionRunes = 1024
	maxTelegramExpandedDir  = 32
)

type outboundFilePayload struct {
	Name       string
	Data       []byte
	SourcePath string
}

// ResolveTextTargetFromSessionID converts a Telegram session id into the
// channel-specific outbound target used by SendText.
func ResolveTextTargetFromSessionID(sessionID string) (string, bool) {
	raw := strings.TrimSpace(sessionID)
	switch {
	case strings.HasPrefix(raw, sessionDMPrefix):
		return parseDMSessionTarget(
			strings.TrimPrefix(raw, sessionDMPrefix),
		)
	case strings.HasPrefix(raw, sessionThreadPrefix):
		return parseThreadSessionTarget(
			strings.TrimPrefix(raw, sessionThreadPrefix),
		)
	default:
		if target, ok := parseThreadSessionTarget(raw); ok {
			return target, true
		}
		if target, ok := parseLegacyDMSessionTarget(raw); ok {
			return target, true
		}
		return "", false
	}
}

// SendText implements channel.TextSender for Telegram.
func (c *Channel) SendText(
	ctx context.Context,
	target string,
	text string,
) error {
	if c == nil || c.bot == nil {
		return fmt.Errorf("telegram: sender unavailable")
	}

	chatID, threadID, err := parseTextTarget(target)
	if err != nil {
		return err
	}

	for _, part := range splitRunes(text, maxReplyRunes) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		_, err := c.sendTextMessage(ctx, tgapi.SendMessageParams{
			ChatID:          chatID,
			MessageThreadID: threadID,
			Text:            part,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// SendMessage implements channel.MessageSender for Telegram.
func (c *Channel) SendMessage(
	ctx context.Context,
	target string,
	msg channel.OutboundMessage,
) error {
	if c == nil || c.bot == nil {
		return fmt.Errorf("telegram: sender unavailable")
	}

	chatID, threadID, err := parseTextTarget(target)
	if err != nil {
		return err
	}
	forceVoice := hasAudioAsVoiceTag(msg.Text)
	files, err := c.expandTelegramOutboundFiles(ctx, msg.Files)
	if err != nil {
		return err
	}
	msg.Files = files
	scope := outboundUploadScopeFromContext(ctx)

	if len(msg.Files) == 1 {
		if caption, plain, parseMode := telegramCaptionParts(
			msg.Text,
			c.state,
		); caption != "" {
			return c.sendFile(
				ctx,
				chatID,
				threadID,
				msg.Files[0],
				caption,
				plain,
				parseMode,
				scope,
				msg.Files[0].AsVoice || forceVoice,
			)
		}
	}

	if strings.TrimSpace(msg.Text) != "" {
		if err := c.SendText(ctx, target, msg.Text); err != nil {
			return err
		}
	}

	for _, file := range msg.Files {
		if err := c.sendFile(
			ctx,
			chatID,
			threadID,
			file,
			"",
			"",
			"",
			scope,
			file.AsVoice || forceVoice,
		); err != nil {
			return err
		}
	}
	return nil
}

func (c *Channel) expandTelegramOutboundFiles(
	ctx context.Context,
	files []channel.OutboundFile,
) ([]channel.OutboundFile, error) {
	if len(files) == 0 {
		return nil, nil
	}
	out := make([]channel.OutboundFile, 0, len(files))
	for _, file := range files {
		expanded, err := c.expandTelegramOutboundFile(ctx, file)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

func (c *Channel) expandTelegramOutboundFile(
	ctx context.Context,
	file channel.OutboundFile,
) ([]channel.OutboundFile, error) {
	raw := strings.TrimSpace(file.Path)
	if raw == "" || isOpaqueOutboundRef(raw) {
		return []channel.OutboundFile{file}, nil
	}
	path, info, ok := c.resolveTelegramOutboundExistingPath(ctx, raw)
	if !ok || info == nil || !info.IsDir() {
		return []channel.OutboundFile{file}, nil
	}
	items := listReplyDirectoryFiles(path, maxTelegramExpandedDir)
	if len(items) == 0 {
		return nil, fmt.Errorf(
			"telegram: outbound directory %q is empty",
			raw,
		)
	}
	out := make([]channel.OutboundFile, 0, len(items))
	for _, item := range items {
		out = append(out, channel.OutboundFile{Path: item})
	}
	return out, nil
}

func isOpaqueOutboundRef(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, fileURLPrefix) {
		return false
	}
	if _, ok := uploads.PathFromHostRef(trimmed); ok {
		return false
	}
	return strings.Contains(trimmed, "://")
}

func (c *Channel) resolveTelegramOutboundExistingPath(
	ctx context.Context,
	raw string,
) (string, os.FileInfo, bool) {
	sessionRoot := outboundSessionUploadsRoot(ctx, c.state)
	if sessionRoot != "" && canJoinReplyRoots(raw) {
		candidate := filepath.Join(sessionRoot, raw)
		if abs, info, err := statReplyPath(candidate); err == nil &&
			pathUnderRoot(abs, filepath.Clean(sessionRoot)) {
			return abs, info, true
		}
	}

	resolved, err := resolveOutboundFilePath(ctx, c.state, raw)
	if err == nil {
		if abs, info, statErr := statReplyPath(resolved); statErr == nil {
			return abs, info, true
		}
	}
	if sessionRoot == "" || !canJoinReplyRoots(raw) {
		return "", nil, false
	}
	candidate := filepath.Join(sessionRoot, raw)
	abs, info, err := statReplyPath(candidate)
	if err != nil || !pathUnderRoot(abs, filepath.Clean(sessionRoot)) {
		return "", nil, false
	}
	return abs, info, true
}

func outboundSessionUploadsRoot(
	ctx context.Context,
	stateRoot string,
) string {
	scope := outboundUploadScopeFromContext(ctx)
	if !isValidUploadScope(scope) {
		return ""
	}
	return sessionUploadsRoot(
		stateRoot,
		scope.UserID,
		scope.SessionID,
	)
}

func parseTextTarget(target string) (int64, int, error) {
	raw := strings.TrimSpace(target)
	if raw == "" {
		return 0, 0, fmt.Errorf("telegram: empty target")
	}

	if resolved, ok := ResolveTextTargetFromSessionID(raw); ok {
		raw = resolved
	}

	chatPart := raw
	threadID := 0

	if idx := strings.Index(raw, threadTopicSep); idx >= 0 {
		chatPart = strings.TrimSpace(raw[:idx])
		topicPart := leadingSessionToken(
			raw[idx+len(threadTopicSep):],
		)
		if topicPart == "" {
			return 0, 0, fmt.Errorf("telegram: empty topic target")
		}
		parsed, err := parseTopicID(topicPart)
		if err != nil {
			return 0, 0, err
		}
		threadID = parsed
	}

	chatID, err := parseChatID(chatPart)
	if err != nil {
		if fallback, ok := parseLegacyDMSessionTarget(chatPart); ok {
			chatID, err = parseChatID(fallback)
		}
		if err != nil {
			return 0, 0, err
		}
	}
	return chatID, threadID, nil
}

func parseDMSessionTarget(raw string) (string, bool) {
	chatPart := leadingSessionToken(raw)
	if chatPart == "" {
		return "", false
	}
	return chatPart, true
}

func parseThreadSessionTarget(raw string) (string, bool) {
	idx := strings.Index(raw, threadTopicSep)
	if idx < 0 {
		return "", false
	}
	chatPart := leadingSessionToken(raw[:idx])
	topicPart := leadingSessionToken(
		raw[idx+len(threadTopicSep):],
	)
	if chatPart == "" || topicPart == "" {
		return "", false
	}
	return chatPart + threadTopicSep + topicPart, true
}

func parseLegacyDMSessionTarget(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	chatPart := leadingSessionToken(trimmed)
	if chatPart == "" || chatPart == trimmed {
		return "", false
	}
	suffix := strings.TrimSpace(strings.TrimPrefix(trimmed, chatPart))
	if !strings.HasPrefix(suffix, ":") {
		return "", false
	}
	suffix = strings.TrimSpace(strings.TrimPrefix(suffix, ":"))
	if suffix == "" || strings.Contains(suffix, ":") {
		return "", false
	}
	if isDigitsOnly(suffix) {
		return "", false
	}
	if _, err := strconvParseInt(chatPart); err != nil {
		return "", false
	}
	return chatPart, true
}

func leadingSessionToken(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if idx := strings.Index(trimmed, ":"); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func parseChatID(raw string) (int64, error) {
	chatID, err := strconvParseInt(raw)
	if err != nil {
		return 0, fmt.Errorf(
			"telegram: invalid chat target: %w",
			err,
		)
	}
	return chatID, nil
}

func parseTopicID(raw string) (int, error) {
	topicID, err := strconvAtoi(raw)
	if err != nil {
		return 0, fmt.Errorf(
			"telegram: invalid topic target: %w",
			err,
		)
	}
	return topicID, nil
}

func (c *Channel) sendFile(
	ctx context.Context,
	chatID int64,
	threadID int,
	file channel.OutboundFile,
	caption string,
	plainCaption string,
	parseMode string,
	scope uploads.Scope,
	asVoice bool,
) error {
	payload, err := resolveOutboundFile(ctx, c.state, file)
	if err != nil {
		return err
	}

	params := tgapi.SendFileParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
		Caption:         caption,
		ParseMode:       parseMode,
		FileName:        payload.Name,
		Data:            payload.Data,
	}

	mode := detectUploadMode(payload.Name, payload.Data, asVoice)
	err = c.sendFileByMode(ctx, mode, params)
	if err == nil {
		savedPath := c.persistDerivedOutboundFile(ctx, payload, scope)
		c.recordSentFiles(ctx, chatID, threadID, payload, savedPath)
		return nil
	}
	if parseMode == "" || !tgapi.IsEntityParseError(err) {
		return err
	}

	fallback := params
	fallback.Caption = plainCaption
	fallback.ParseMode = ""
	if err := c.sendFileByMode(ctx, mode, fallback); err != nil {
		return err
	}
	savedPath := c.persistDerivedOutboundFile(ctx, payload, scope)
	c.recordSentFiles(ctx, chatID, threadID, payload, savedPath)
	return nil
}

func (c *Channel) recordSentFiles(
	ctx context.Context,
	chatID int64,
	threadID int,
	payload outboundFilePayload,
	savedPath string,
) {
	if c == nil || c.sentFiles == nil {
		return
	}
	requestID := currentRequestIDFromContext(ctx)
	if strings.TrimSpace(requestID) == "" {
		return
	}
	c.sentFiles.Record(
		requestID,
		chatID,
		threadID,
		payload.SourcePath,
		savedPath,
	)
}

func (c *Channel) sendFileByMode(
	ctx context.Context,
	mode string,
	params tgapi.SendFileParams,
) error {
	var err error
	switch mode {
	case uploadModePhoto:
		_, err = c.bot.SendPhoto(ctx, params)
	case uploadModeAudio:
		_, err = c.bot.SendAudio(ctx, params)
	case uploadModeVoice:
		_, err = c.bot.SendVoice(ctx, params)
	case uploadModeVideo:
		_, err = c.bot.SendVideo(ctx, params)
	default:
		_, err = c.bot.SendDocument(ctx, params)
	}
	return err
}

func (c *Channel) persistDerivedOutboundFile(
	ctx context.Context,
	payload outboundFilePayload,
	scope uploads.Scope,
) string {
	if c == nil || strings.TrimSpace(c.state) == "" {
		return ""
	}
	if !isValidUploadScope(scope) {
		return ""
	}
	store, err := uploads.NewStore(c.state)
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: create uploads store for outbound file: %v",
			err,
		)
		return ""
	}
	if existing := c.annotateExistingOutboundFile(
		ctx,
		store,
		payload,
		scope,
	); existing != "" {
		return existing
	}
	saved, err := store.SaveWithInfo(
		ctx,
		scope,
		payload.Name,
		uploads.FileMetadata{
			MimeType: detectMediaType(payload.Name, payload.Data),
			Source:   uploads.SourceDerived,
		},
		payload.Data,
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: persist outbound file %q: %v",
			payload.Name,
			err,
		)
		return ""
	}
	return strings.TrimSpace(saved.Path)
}

func (c *Channel) annotateExistingOutboundFile(
	ctx context.Context,
	store *uploads.Store,
	payload outboundFilePayload,
	scope uploads.Scope,
) string {
	if store == nil {
		return ""
	}
	source := filepath.Clean(strings.TrimSpace(payload.SourcePath))
	if source == "" || !filepath.IsAbs(source) {
		return ""
	}
	root := filepath.Clean(store.ScopeDir(scope))
	if root == "" || !pathUnderRoot(source, root) {
		return ""
	}

	err := store.Annotate(
		source,
		uploads.FileMetadata{
			MimeType: detectMediaType(payload.Name, payload.Data),
			Source:   uploads.SourceDerived,
		},
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: annotate outbound file %q: %v",
			payload.Name,
			err,
		)
		return ""
	}
	return source
}

func outboundUploadScopeFromContext(
	ctx context.Context,
) uploads.Scope {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return uploads.Scope{}
	}
	return uploads.Scope{
		Channel:   channelID,
		UserID:    strings.TrimSpace(inv.Session.UserID),
		SessionID: strings.TrimSpace(inv.Session.ID),
	}
}

func isValidUploadScope(scope uploads.Scope) bool {
	return strings.TrimSpace(scope.Channel) != "" &&
		strings.TrimSpace(scope.UserID) != "" &&
		strings.TrimSpace(scope.SessionID) != ""
}

func telegramCaptionParts(
	text string,
	stateDir string,
) (string, string, string) {
	plain := sanitizeTelegramText(text, stateDir)
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", "", ""
	}
	if len([]rune(plain)) > maxTelegramCaptionRunes {
		return "", "", ""
	}
	formatted, ok := renderTelegramHTMLText(plain)
	if !ok {
		return plain, plain, ""
	}
	return formatted, plain, tgapi.ParseModeHTML
}

func resolveOutboundFile(
	ctx context.Context,
	stateRoot string,
	file channel.OutboundFile,
) (outboundFilePayload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw := strings.TrimSpace(file.Path)
	if raw == "" {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: empty file path",
		)
	}

	switch {
	case strings.HasPrefix(raw, fileref.ArtifactPrefix):
		return resolveArtifactOutboundFile(ctx, raw, file.Name)
	case strings.HasPrefix(raw, fileref.WorkspacePrefix):
		return resolveWorkspaceOutboundFile(ctx, raw, file.Name)
	default:
		return resolveHostOutboundFile(ctx, stateRoot, raw, file.Name)
	}
}

func resolveArtifactOutboundFile(
	ctx context.Context,
	raw string,
	nameHint string,
) (outboundFilePayload, error) {
	ref, err := fileref.Parse(raw)
	if err != nil {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: parse artifact ref: %w",
			err,
		)
	}
	data, _, _, err := codeexecutor.LoadArtifactHelper(
		withArtifactContext(ctx),
		ref.ArtifactName,
		ref.ArtifactVersion,
	)
	if err != nil {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: load artifact: %w",
			err,
		)
	}

	name := strings.TrimSpace(nameHint)
	if name == "" {
		name = path.Base(strings.TrimSpace(ref.ArtifactName))
	}
	if name == "" || name == "." || name == "/" {
		name = defaultAttachmentName
	}
	return outboundFilePayload{
		Name: name,
		Data: data,
	}, nil
}

func resolveWorkspaceOutboundFile(
	ctx context.Context,
	raw string,
	nameHint string,
) (outboundFilePayload, error) {
	content, _, handled, err := fileref.TryRead(ctx, raw)
	if err != nil {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: load workspace ref: %w",
			err,
		)
	}
	if !handled {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: unsupported workspace ref: %s",
			raw,
		)
	}

	ref, err := fileref.Parse(raw)
	if err != nil {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: parse workspace ref: %w",
			err,
		)
	}

	name := strings.TrimSpace(nameHint)
	if name == "" {
		name = path.Base(strings.TrimSpace(ref.Path))
	}
	if name == "" || name == "." || name == "/" {
		name = workspaceFileFallback
	}
	return outboundFilePayload{
		Name: name,
		Data: []byte(content),
	}, nil
}

func resolveHostOutboundFile(
	ctx context.Context,
	stateRoot string,
	raw string,
	nameHint string,
) (outboundFilePayload, error) {
	resolved, err := resolveOutboundFilePath(ctx, stateRoot, raw)
	if err != nil {
		return outboundFilePayload{}, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return outboundFilePayload{}, fmt.Errorf(
			"telegram: read file: %w",
			err,
		)
	}

	mimeType := detectMediaType(filepath.Base(resolved), data)
	name := strings.TrimSpace(nameHint)
	if name == "" {
		name = filepath.Base(resolved)
	}
	name = uploads.PreferredName(name, mimeType)
	return outboundFilePayload{
		Name:       name,
		Data:       data,
		SourcePath: resolved,
	}, nil
}

func resolveOutboundFilePath(
	ctx context.Context,
	stateRoot string,
	raw string,
) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("telegram: empty file path")
	}
	if path, ok := uploads.PathFromHostRef(trimmed); ok {
		return path, nil
	}
	if strings.HasPrefix(trimmed, fileURLPrefix) {
		u, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("telegram: invalid file url: %w", err)
		}
		path := strings.TrimSpace(u.Path)
		if path == "" {
			return "", fmt.Errorf("telegram: empty file url path")
		}
		return filepath.Clean(path), nil
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") {
		expanded, err := expandHomePath(trimmed)
		if err != nil {
			return "", err
		}
		return filepath.Clean(expanded), nil
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), nil
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("telegram: resolve file path: %w", err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		return filepath.Clean(abs), nil
	}
	if resolved, ok := resolveSessionScopedOutboundPath(
		ctx,
		stateRoot,
		trimmed,
	); ok {
		return resolved, nil
	}
	return abs, nil
}

func resolveSessionScopedOutboundPath(
	ctx context.Context,
	stateRoot string,
	raw string,
) (string, bool) {
	if ctx == nil {
		return "", false
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return "", false
	}
	root := sessionUploadsRoot(
		stateRoot,
		inv.Session.UserID,
		inv.Session.ID,
	)
	if root == "" {
		return "", false
	}
	if joined, ok := resolveSessionScopedJoinedPath(root, raw); ok {
		return joined, true
	}
	if !looksLikeReplyFileName(raw) {
		return "", false
	}
	matches := findReplyNamedFiles(
		root,
		filepath.Base(strings.TrimSpace(raw)),
		maxReplySearchDepth,
		1,
	)
	if len(matches) == 0 {
		return "", false
	}
	return filepath.Clean(matches[0]), true
}

func resolveSessionScopedJoinedPath(
	root string,
	raw string,
) (string, bool) {
	if !canJoinReplyRoots(raw) {
		return "", false
	}
	candidate := filepath.Join(root, raw)
	abs, info, err := statReplyPath(candidate)
	if err != nil || info == nil || info.IsDir() {
		return "", false
	}
	if !pathUnderRoot(abs, filepath.Clean(root)) {
		return "", false
	}
	return abs, true
}

func expandHomePath(raw string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("telegram: resolve home dir: %w", err)
	}
	if strings.TrimSpace(raw) == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(raw, "~/")), nil
}

func withArtifactContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if svc, ok := codeexecutor.ArtifactServiceFromContext(ctx); ok &&
		svc != nil {
		return ctx
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.ArtifactService == nil ||
		inv.Session == nil {
		return ctx
	}

	ctx = codeexecutor.WithArtifactService(ctx, inv.ArtifactService)
	return codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	})
}

func detectUploadMode(
	name string,
	data []byte,
	asVoice bool,
) string {
	contentType := detectMediaType(name, data)
	switch {
	case strings.HasPrefix(contentType, mimePrefixImage) &&
		contentType != mimeImageGIF:
		return uploadModePhoto
	case asVoice && isVoiceCompatibleMedia(contentType, name):
		return uploadModeVoice
	case isVoiceMedia(contentType, name):
		return uploadModeVoice
	case strings.HasPrefix(contentType, mimePrefixAudio):
		return uploadModeAudio
	case strings.HasPrefix(contentType, mimePrefixVideo):
		return uploadModeVideo
	default:
		return uploadModeDocument
	}
}

func detectMediaType(name string, data []byte) string {
	contentType := strings.TrimSpace(http.DetectContentType(data))
	if contentType == mimePrefixImage ||
		contentType == mimePrefixAudio ||
		contentType == mimePrefixVideo {
		contentType = ""
	}
	extType := typeFromExtension(filepath.Ext(name))
	if extType == "" {
		return contentType
	}
	if strings.TrimSpace(contentType) == "" ||
		contentType == mimeOctetStream ||
		strings.HasPrefix(contentType, mimePrefixText) {
		return extType
	}
	return contentType
}

func typeFromExtension(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return mimeImageGIF
	case ".pdf":
		return "application/pdf"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".oga":
		return mimeVoiceOGG
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	default:
		return ""
	}
}

func isVoiceMedia(contentType string, name string) bool {
	if contentType == mimeVoiceOGG {
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ogg", ".oga":
		return true
	default:
		return false
	}
}

func isVoiceCompatibleMedia(contentType string, name string) bool {
	if isVoiceMedia(contentType, name) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "audio/mpeg", "audio/mp3", "audio/mp4", "audio/x-m4a":
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".m4a":
		return true
	default:
		return false
	}
}

func strconvParseInt(raw string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
}

func strconvAtoi(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func isDigitsOnly(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
