//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	testToken = "token"

	chatTypePrivate    = "private"
	chatTypeSuperGroup = "supergroup"
)

var cwdMu sync.Mutex

type stubGateway struct {
	mu    sync.Mutex
	reqs  []gwclient.MessageRequest
	rsp   gwclient.MessageResponse
	err   error
	delay time.Duration

	onSend   func()
	onCancel func()

	canceled  []string
	cancelOK  bool
	cancelErr error
}

func (g *stubGateway) SendMessage(
	ctx context.Context,
	req gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	if g.delay > 0 {
		select {
		case <-ctx.Done():
			return gwclient.MessageResponse{}, ctx.Err()
		case <-time.After(g.delay):
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.reqs = append(g.reqs, req)
	if g.onSend != nil {
		g.onSend()
	}
	return g.rsp, g.err
}

func (g *stubGateway) Cancel(
	_ context.Context,
	requestID string,
) (bool, error) {
	g.mu.Lock()
	g.canceled = append(g.canceled, requestID)
	onCancel := g.onCancel
	ok := g.cancelOK
	err := g.cancelErr
	g.mu.Unlock()

	if onCancel != nil {
		onCancel()
	}
	return ok, err
}

type stubBot struct {
	mu           sync.Mutex
	sent         []tgapi.SendMessageParams
	sendErr      error
	sendHook     func(tgapi.SendMessageParams) error
	callbacks    []tgapi.AnswerCallbackQueryParams
	callbackErr  error
	callbackHook func(tgapi.AnswerCallbackQueryParams) error
	docs         []tgapi.SendFileParams
	photos       []tgapi.SendFileParams
	audios       []tgapi.SendFileParams
	voices       []tgapi.SendFileParams
	videos       []tgapi.SendFileParams
	fileErr      error
	fileHook     func(tgapi.SendFileParams) error
	edits        []tgapi.EditMessageTextParams
	editErr      error
	editHook     func(tgapi.EditMessageTextParams) error
	actions      []tgapi.SendChatActionParams
	actionErr    error
	commands     [][]tgapi.BotCommand
	cmdErr       error
	updates      [][]tgapi.Update
	getError     error

	downloads map[string]stubDownload
	dlCalls   []downloadCall

	nextMessageID int
}

type downloadCall struct {
	fileID   string
	maxBytes int64
}

type stubDownload struct {
	file tgapi.File
	data []byte
	err  error
}

func (b *stubBot) GetUpdates(
	_ context.Context,
	_ int,
	_ time.Duration,
) ([]tgapi.Update, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.getError != nil {
		return nil, b.getError
	}
	if len(b.updates) == 0 {
		return nil, nil
	}
	out := b.updates[0]
	b.updates = b.updates[1:]
	return out, nil
}

func (b *stubBot) SendMessage(
	_ context.Context,
	params tgapi.SendMessageParams,
) (tgapi.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, params)
	if b.sendHook != nil {
		if err := b.sendHook(params); err != nil {
			return tgapi.Message{}, err
		}
	}
	if b.sendErr != nil {
		return tgapi.Message{}, b.sendErr
	}

	b.nextMessageID++
	return tgapi.Message{
		MessageID: b.nextMessageID,
		Text:      params.Text,
	}, nil
}

func (b *stubBot) AnswerCallbackQuery(
	_ context.Context,
	params tgapi.AnswerCallbackQueryParams,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callbacks = append(b.callbacks, params)
	if b.callbackHook != nil {
		if err := b.callbackHook(params); err != nil {
			return err
		}
	}
	return b.callbackErr
}

func (b *stubBot) SendDocument(
	_ context.Context,
	params tgapi.SendFileParams,
) (tgapi.Message, error) {
	return b.appendFileSend(&b.docs, params)
}

func (b *stubBot) SendPhoto(
	_ context.Context,
	params tgapi.SendFileParams,
) (tgapi.Message, error) {
	return b.appendFileSend(&b.photos, params)
}

func (b *stubBot) SendAudio(
	_ context.Context,
	params tgapi.SendFileParams,
) (tgapi.Message, error) {
	return b.appendFileSend(&b.audios, params)
}

func (b *stubBot) SendVoice(
	_ context.Context,
	params tgapi.SendFileParams,
) (tgapi.Message, error) {
	return b.appendFileSend(&b.voices, params)
}

func (b *stubBot) SendVideo(
	_ context.Context,
	params tgapi.SendFileParams,
) (tgapi.Message, error) {
	return b.appendFileSend(&b.videos, params)
}

func (b *stubBot) appendFileSend(
	dst *[]tgapi.SendFileParams,
	params tgapi.SendFileParams,
) (tgapi.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*dst = append(*dst, params)
	if b.fileHook != nil {
		if err := b.fileHook(params); err != nil {
			return tgapi.Message{}, err
		}
	}
	if b.fileErr != nil {
		return tgapi.Message{}, b.fileErr
	}

	b.nextMessageID++
	return tgapi.Message{
		MessageID: b.nextMessageID,
	}, nil
}

func (b *stubBot) EditMessageText(
	_ context.Context,
	params tgapi.EditMessageTextParams,
) (tgapi.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.edits = append(b.edits, params)
	if b.editHook != nil {
		if err := b.editHook(params); err != nil {
			return tgapi.Message{}, err
		}
	}
	if b.editErr != nil {
		return tgapi.Message{}, b.editErr
	}
	return tgapi.Message{
		MessageID: params.MessageID,
		Text:      params.Text,
	}, nil
}

func (b *stubBot) SendChatAction(
	_ context.Context,
	params tgapi.SendChatActionParams,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.actions = append(b.actions, params)
	return b.actionErr
}

func (b *stubBot) SetMyCommands(
	_ context.Context,
	params tgapi.SetMyCommandsParams,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commands = append(b.commands, params.Commands)
	return b.cmdErr
}

func (b *stubBot) DownloadFileByID(
	_ context.Context,
	fileID string,
	maxBytes int64,
) (tgapi.File, []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.dlCalls = append(b.dlCalls, downloadCall{
		fileID:   fileID,
		maxBytes: maxBytes,
	})

	if b.downloads == nil {
		return tgapi.File{}, nil, errors.New("download not configured")
	}
	res, ok := b.downloads[fileID]
	if !ok {
		return tgapi.File{}, nil, errors.New("unknown file id")
	}
	return res.file, res.data, res.err
}

type stubOffsetStore struct {
	mu     sync.Mutex
	offset int
	ok     bool
	err    error
	writes []int
}

func (s *stubOffsetStore) Read(
	_ context.Context,
) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset, s.ok, s.err
}

func (s *stubOffsetStore) Write(
	_ context.Context,
	offset int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, offset)
	return nil
}

type stubPairingStore struct {
	approved bool
	code     string

	isApprovedErr error
	requestErr    error

	requested []string
}

func (s *stubPairingStore) IsApproved(
	_ context.Context,
	_ string,
) (bool, error) {
	if s.isApprovedErr != nil {
		return false, s.isApprovedErr
	}
	return s.approved, nil
}

func (s *stubPairingStore) Request(
	_ context.Context,
	userID string,
) (string, bool, error) {
	if s.requestErr != nil {
		return "", false, s.requestErr
	}
	s.requested = append(s.requested, userID)
	return s.code, false, nil
}

func TestNew_Errors(t *testing.T) {
	t.Parallel()

	_, err := New("", BotInfo{}, &stubGateway{})
	require.Error(t, err)

	_, err = New(testToken, BotInfo{}, nil)
	require.Error(t, err)
}

func TestChannel_ID(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
	)
	require.NoError(t, err)
	require.Equal(t, "telegram", ch.ID())
}

func TestChannel_RegisterBotCommands(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.registerBotCommands(context.Background())
	require.NoError(t, err)

	bot.mu.Lock()
	defer bot.mu.Unlock()
	require.Len(t, bot.commands, 1)
	require.Equal(t, defaultBotCommands(), bot.commands[0])
}

func TestChannel_RegisterBotCommands_Disabled(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithRegisterCommands(false),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.registerBotCommands(context.Background())
	require.NoError(t, err)

	bot.mu.Lock()
	defer bot.mu.Unlock()
	require.Empty(t, bot.commands)
}

func TestNew_OptionsApplied(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	dir := t.TempDir()

	pollTimeout := 5 * time.Second
	errorBackoff := 7 * time.Second
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithStartFromLatest(false),
		WithPollTimeout(pollTimeout),
		WithErrorBackoff(errorBackoff),
	)
	require.NoError(t, err)

	require.False(t, ch.startFromLatest)
	require.Equal(t, pollTimeout, ch.pollTimeout)
	require.Equal(t, errorBackoff, ch.errorBackoff)
}

func TestOption_DMSessionResetAndBlockCleanup(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	idle := 7 * time.Second

	WithDMSessionIdleReset(idle)(cfg)
	WithDMSessionDailyReset(true)(cfg)
	WithDMBlockCleanup(dmBlockCleanupForget)(cfg)

	require.Equal(t, idle, cfg.dmResetPolicy.Idle)
	require.True(t, cfg.dmResetPolicy.Daily)
	require.Equal(t, dmBlockCleanupForget, cfg.dmBlockCleanup)
}

func TestChannel_Run_Nil(t *testing.T) {
	t.Parallel()

	var ch *Channel
	err := ch.Run(context.Background())
	require.Error(t, err)
}

func TestChannel_HandleMessage_PrivateChat(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Equal(t, "telegram", req.Channel)
	require.Equal(t, "2", req.From)
	require.Equal(t, "", req.Thread)
	require.Equal(t, "3", req.MessageID)
	require.Equal(t, "hi", req.Text)
	require.Equal(t, "2", req.UserID)
	require.Equal(t, "telegram:1:3", req.RequestID)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	sent := bot.sent[0]
	bot.mu.Unlock()

	require.Equal(t, int64(1), sent.ChatID)
	require.Equal(t, 0, sent.MessageThreadID)
	require.Equal(t, 3, sent.ReplyToMessageID)
	require.Equal(t, "ok", sent.Text)
}

func TestChannel_HandleMessage_CommandReset_RotatesSession(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	botInfo := BotInfo{Username: "bot"}
	ch, err := New(
		testToken,
		botInfo,
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	legacySession := buildLaneKey("2", "")

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	require.Equal(t, legacySession, gw.reqs[0].SessionID)
	gw.mu.Unlock()

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 4,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "/reset",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	gw.mu.Unlock()

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 5,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi2",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 2)
	rotatedSession := gw.reqs[1].SessionID
	gw.mu.Unlock()

	require.True(t, strings.HasPrefix(rotatedSession, legacySession+":"))

	ch2, err := New(
		testToken,
		botInfo,
		&stubGateway{},
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	got, _, err := ch2.dmSessions.EnsureActiveSession(
		context.Background(),
		"2",
		legacySession,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.Equal(t, rotatedSession, got)

	bot.mu.Lock()
	require.Len(t, bot.sent, 3)
	require.Equal(t, "ok", bot.sent[0].Text)
	require.Equal(t, resetOKMessage, bot.sent[1].Text)
	require.Equal(t, "ok", bot.sent[2].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_PhotoCaption_BuildsImagePart(t *testing.T) {
	t.Parallel()

	photoBytes := []byte{0xff, 0xd8, 0xff, 0xd9}

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"p1": {
				file: tgapi.File{FilePath: "photos/file_1.jpg"},
				data: photoBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Caption:   "hi",
		Photo: []tgapi.PhotoSize{
			{FileID: "p1", FileSize: int64(len(photoBytes))},
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Equal(t, "hi", req.Text)
	require.Len(t, req.ContentParts, 2)

	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeImage, part.Type)
	require.NotNil(t, part.Image)
	require.Equal(t, photoBytes, part.Image.Data)
	require.Equal(t, "jpeg", part.Image.Format)

	filePart := req.ContentParts[1]
	require.Equal(t, gwproto.PartTypeFile, filePart.Type)
	require.NotNil(t, filePart.File)
	require.Equal(t, defaultPhotoName+".jpeg", filePart.File.Filename)
	require.Equal(t, mimeImageJPEG, filePart.File.Format)
	require.Equal(t, photoBytes, filePart.File.Data)
}

func TestChannel_HandleMessage_Document_BuildsFilePart(t *testing.T) {
	t.Parallel()

	docBytes := []byte("hello")

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"d1": {
				file: tgapi.File{FilePath: "docs/doc.txt"},
				data: docBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Document: &tgapi.Document{
			FileID:   "d1",
			FileName: "doc.txt",
			MimeType: "text/plain",
			FileSize: int64(len(docBytes)),
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Len(t, req.ContentParts, 1)
	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeFile, part.Type)
	require.NotNil(t, part.File)
	require.Equal(t, "doc.txt", part.File.Filename)
	require.Equal(t, "text/plain", part.File.Format)
	require.Equal(t, docBytes, part.File.Data)
}

func TestChannel_HandleMessage_AudioMP3_BuildsAudioPart(t *testing.T) {
	t.Parallel()

	audioBytes := []byte("mp3")

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"a1": {
				file: tgapi.File{FilePath: "audio/song.mp3"},
				data: audioBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Audio: &tgapi.Audio{
			FileID:   "a1",
			FileName: "song.mp3",
			MimeType: "audio/mpeg",
			FileSize: int64(len(audioBytes)),
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Len(t, req.ContentParts, 2)
	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeAudio, part.Type)
	require.NotNil(t, part.Audio)
	require.Equal(t, audioBytes, part.Audio.Data)
	require.Equal(t, "mp3", part.Audio.Format)

	filePart := req.ContentParts[1]
	require.Equal(t, gwproto.PartTypeFile, filePart.Type)
	require.NotNil(t, filePart.File)
	require.Equal(t, "song.mp3", filePart.File.Filename)
	require.Equal(t, mimeAudioMP3, filePart.File.Format)
	require.Equal(t, audioBytes, filePart.File.Data)
}

func TestChannel_HandleMessage_Video_BuildsVideoPart(t *testing.T) {
	t.Parallel()

	videoBytes := []byte("mp4")

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"v1": {
				file: tgapi.File{FilePath: "video/clip.mp4"},
				data: videoBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Video: &tgapi.Video{
			FileID:   "v1",
			FileName: "clip.mp4",
			MimeType: "video/mp4",
			FileSize: int64(len(videoBytes)),
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Len(t, req.ContentParts, 1)
	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeVideo, part.Type)
	require.NotNil(t, part.File)
	require.Equal(t, "clip.mp4", part.File.Filename)
	require.Equal(t, "video/mp4", part.File.Format)
	require.Equal(t, videoBytes, part.File.Data)
}

func TestChannel_HandleMessage_ReplyToVideo_BuildsVideoPart(
	t *testing.T,
) {
	t.Parallel()

	videoBytes := []byte("mp4")

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"v1": {
				file: tgapi.File{FilePath: "video/clip.mp4"},
				data: videoBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "extract the last two frames",
		ReplyToMessage: &tgapi.Message{
			MessageID: 2,
			Video: &tgapi.Video{
				FileID:   "v1",
				FileName: "clip.mp4",
				MimeType: "video/mp4",
				FileSize: int64(len(videoBytes)),
			},
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Equal(t, "extract the last two frames", req.Text)
	require.Len(t, req.ContentParts, 1)
	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeVideo, part.Type)
	require.NotNil(t, part.File)
	require.Equal(t, "clip.mp4", part.File.Filename)
	require.Equal(t, "video/mp4", part.File.Format)
	require.Equal(t, videoBytes, part.File.Data)
}

func TestChannel_HandleMessage_Animation_BuildsVideoPart(t *testing.T) {
	t.Parallel()

	videoBytes := []byte("webm")

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"a1": {
				file: tgapi.File{FilePath: "video/anim.webm"},
				data: videoBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Animation: &tgapi.Animation{
			FileID:   "a1",
			FileName: "anim.webm",
			MimeType: "video/webm",
			FileSize: int64(len(videoBytes)),
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Len(t, req.ContentParts, 1)
	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeVideo, part.Type)
	require.NotNil(t, part.File)
	require.Equal(t, "anim.webm", part.File.Filename)
	require.Equal(t, "video/webm", part.File.Format)
	require.Equal(t, videoBytes, part.File.Data)
}

func TestChannel_HandleMessage_VideoNote_BuildsVideoPart(t *testing.T) {
	t.Parallel()

	videoBytes := []byte("note")

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{
		downloads: map[string]stubDownload{
			"vn1": {
				file: tgapi.File{FilePath: "video/note.mp4"},
				data: videoBytes,
			},
		},
	}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		VideoNote: &tgapi.VideoNote{
			FileID:   "vn1",
			FileSize: int64(len(videoBytes)),
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Len(t, req.ContentParts, 1)
	part := req.ContentParts[0]
	require.Equal(t, gwproto.PartTypeVideo, part.Type)
	require.NotNil(t, part.File)
	require.Equal(t, "note.mp4", part.File.Filename)
	require.Equal(t, "video/mp4", part.File.Format)
	require.Equal(t, videoBytes, part.File.Data)
}

func TestChannel_HandleMessage_DMPolicyAllowlist_NoAllowUsers(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:        bot,
		gw:         gw,
		dmPolicy:   dmPolicyAllowlist,
		allowUsers: nil,
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, notAllowedMessage, bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_DMPolicyPairing_Unapproved(t *testing.T) {
	t.Parallel()

	p := &stubPairingStore{approved: false, code: "123456"}
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:      bot,
		gw:       gw,
		dmPolicy: dmPolicyPairing,
		pairing:  p,
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "Pairing required")
	require.Contains(t, bot.sent[0].Text, "123456")
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_DMPolicyPairing_Approved(t *testing.T) {
	t.Parallel()

	p := &stubPairingStore{approved: true, code: "123456"}
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:      bot,
		gw:       gw,
		dmPolicy: dmPolicyPairing,
		pairing:  p,
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 3,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 1, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	gw.mu.Unlock()

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	require.Equal(t, "ok", bot.sent[0].Text)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_GroupTopic(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithGroupPolicy(groupPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID:       7,
		MessageThreadID: 99,
		From:            &tgapi.User{ID: 8},
		Chat: &tgapi.Chat{
			ID:   10,
			Type: chatTypeSuperGroup,
		},
		Text: "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	req := gw.reqs[0]
	gw.mu.Unlock()

	require.Equal(t, "10:topic:99", req.Thread)
	require.Equal(t, "telegram:10:99:7", req.RequestID)

	bot.mu.Lock()
	require.Len(t, bot.sent, 1)
	sent := bot.sent[0]
	bot.mu.Unlock()

	require.Equal(t, int64(10), sent.ChatID)
	require.Equal(t, 99, sent.MessageThreadID)
	require.Equal(t, 7, sent.ReplyToMessageID)
}

func TestChannel_HandleMessage_GroupPolicyAllowlist_Drops(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:         bot,
		gw:          gw,
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"11": {},
		},
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID:       7,
		MessageThreadID: 99,
		From:            &tgapi.User{ID: 8},
		Chat: &tgapi.Chat{
			ID:   10,
			Type: chatTypeSuperGroup,
		},
		Text: "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Empty(t, gw.reqs)
	gw.mu.Unlock()
}

func TestChannel_HandleMessage_GroupPolicyAllowlist_AllowsChatID(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:         bot,
		gw:          gw,
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"10": {},
		},
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID:       7,
		MessageThreadID: 99,
		From:            &tgapi.User{ID: 8},
		Chat: &tgapi.Chat{
			ID:   10,
			Type: chatTypeSuperGroup,
		},
		Text: "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	gw.mu.Unlock()
}

func TestChannel_HandleMessage_GroupPolicyAllowlist_AllowsTopic(
	t *testing.T,
) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	bot := &stubBot{}
	ch := &Channel{
		bot:         bot,
		gw:          gw,
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"10:topic:99": {},
		},
	}

	err := ch.handleMessage(context.Background(), tgapi.Message{
		MessageID:       7,
		MessageThreadID: 99,
		From:            &tgapi.User{ID: 8},
		Chat: &tgapi.Chat{
			ID:   10,
			Type: chatTypeSuperGroup,
		},
		Text: "hi",
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Len(t, gw.reqs, 1)
	gw.mu.Unlock()
}

func TestChannel_HandleMessage_Gateway4xx_Drop(t *testing.T) {
	t.Parallel()

	gwErr := errors.New("bad request")
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusBadRequest,
		},
		err: gwErr,
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Empty(t, bot.sent)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_Gateway5xx_Retry(t *testing.T) {
	t.Parallel()

	gwErr := errors.New("server error")
	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusInternalServerError,
		},
		err: gwErr,
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.ErrorIs(t, err, gwErr)
}

func TestChannel_Run_OneMessage(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	gw := &stubGateway{
		cancelOK: true,
		onCancel: cancel,
	}

	bot := &stubBot{
		updates: [][]tgapi.Update{
			{
				{
					UpdateID: 1,
					MyChatMember: &tgapi.ChatMemberEvent{
						Chat: &tgapi.Chat{
							ID:   2,
							Type: chatTypePrivate,
						},
						NewChatMember: &tgapi.ChatMember{
							Status: tgChatMemberStatusLeft,
						},
					},
				},
				{
					UpdateID: 2,
					Message: &tgapi.Message{
						MessageID: 1,
						From:      &tgapi.User{ID: 2},
						Chat: &tgapi.Chat{
							ID:   2,
							Type: chatTypePrivate,
						},
						Text: "/help",
					},
				},
			},
		},
	}

	store := &stubOffsetStore{}
	ch := &Channel{
		bot:             bot,
		gw:              gw,
		store:           store,
		startFromLatest: false,
		pollTimeout:     0,
		errorBackoff:    0,
		dmPolicy:        dmPolicyOpen,
		dmBlockCleanup:  dmBlockCleanupNone,
		inflight:        newInflightRequests(),
	}

	laneKey := buildLaneKey("2", "")
	ch.inflight.Set(laneKey, "req-1")

	require.NoError(t, ch.Run(ctx))

	require.Eventually(t, func() bool {
		gw.mu.Lock()
		defer gw.mu.Unlock()
		return len(gw.canceled) == 1 && gw.canceled[0] == "req-1"
	}, 500*time.Millisecond, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		return len(bot.sent) == 1 &&
			strings.Contains(bot.sent[0].Text, "Commands:")
	}, 500*time.Millisecond, 10*time.Millisecond)
}

func TestChannel_HandleMessage_Ignored(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Ignored:    true,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	bot := &stubBot{}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)

	bot.mu.Lock()
	require.Empty(t, bot.sent)
	bot.mu.Unlock()
}

func TestChannel_HandleMessage_SendError_Drops(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{
		rsp: gwclient.MessageResponse{
			StatusCode: http.StatusOK,
			Reply:      "ok",
		},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
	)
	require.NoError(t, err)

	botErr := errors.New("send failed")
	bot := &stubBot{sendErr: botErr}
	ch.bot = bot

	err = ch.handleMessage(context.Background(), tgapi.Message{
		MessageID: 1,
		From:      &tgapi.User{ID: 2},
		Chat:      &tgapi.Chat{ID: 3, Type: chatTypePrivate},
		Text:      "hi",
	})
	require.NoError(t, err)
}

func TestChannel_HandleMyChatMember_Block_Reset(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{cancelOK: true}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
		WithDMBlockCleanup(dmBlockCleanupReset),
	)
	require.NoError(t, err)

	ctx := context.Background()
	userID := "2"
	laneKey := buildLaneKey(userID, "")

	sid, rotated, err := ch.dmSessions.EnsureActiveSession(
		ctx,
		userID,
		laneKey,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.Equal(t, laneKey, sid)

	ch.inflight.Set(laneKey, "req-1")

	err = ch.handleMyChatMember(ctx, tgapi.ChatMemberEvent{
		Chat: &tgapi.Chat{
			ID:   2,
			Type: chatTypePrivate,
		},
		NewChatMember: &tgapi.ChatMember{
			Status: tgChatMemberStatusKicked,
		},
	})
	require.NoError(t, err)

	got, rotated, err := ch.dmSessions.EnsureActiveSession(
		ctx,
		userID,
		laneKey,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.True(t, strings.HasPrefix(got, laneKey+":"))

	gw.mu.Lock()
	require.Equal(t, []string{"req-1"}, gw.canceled)
	gw.mu.Unlock()
}

func TestChannel_HandleMyChatMember_Block_Forget(t *testing.T) {
	t.Parallel()

	gw := &stubGatewayWithForget{
		stubGateway: &stubGateway{cancelOK: true},
	}
	dir := t.TempDir()
	ch, err := New(
		testToken,
		BotInfo{Username: "bot"},
		gw,
		WithStateDir(dir),
		WithDMPolicy(dmPolicyOpen),
		WithDMBlockCleanup(dmBlockCleanupForget),
	)
	require.NoError(t, err)

	ctx := context.Background()
	userID := "2"
	laneKey := buildLaneKey(userID, "")

	_, err = ch.dmSessions.Rotate(ctx, userID, laneKey)
	require.NoError(t, err)

	ch.inflight.Set(laneKey, "req-1")

	err = ch.handleMyChatMember(ctx, tgapi.ChatMemberEvent{
		Chat: &tgapi.Chat{
			ID:   2,
			Type: chatTypePrivate,
		},
		NewChatMember: &tgapi.ChatMember{
			Status: tgChatMemberStatusLeft,
		},
	})
	require.NoError(t, err)

	gw.mu.Lock()
	require.Equal(t, []string{"2"}, gw.forgetCalls)
	require.Equal(t, []string{"req-1"}, gw.canceled)
	gw.mu.Unlock()

	got, rotated, err := ch.dmSessions.EnsureActiveSession(
		ctx,
		userID,
		laneKey,
		dmSessionResetPolicy{},
	)
	require.NoError(t, err)
	require.False(t, rotated)
	require.Equal(t, laneKey, got)
}

func TestChannelAnswerCallbackQueryAndAccessHelpers(t *testing.T) {
	t.Parallel()

	ch := &Channel{}
	require.NoError(
		t,
		ch.answerCallbackQuery(context.Background(), "", "done", true),
	)
	require.True(t, ch.isUserAllowed("u1"))
	require.True(t, ch.isChatAllowed(false, ""))

	bot := &stubBot{}
	ch = &Channel{
		bot:         bot,
		allowUsers:  map[string]struct{}{"u1": {}},
		groupPolicy: groupPolicyAllowlist,
		allowThreads: map[string]struct{}{
			"100": {},
		},
	}
	require.True(t, ch.isUserAllowed("u1"))
	require.False(t, ch.isUserAllowed("u2"))
	require.True(t, ch.isChatAllowed(true, "100:topic:7"))
	require.False(t, ch.isChatAllowed(true, "200:topic:7"))
	require.NoError(
		t,
		ch.answerCallbackQuery(context.Background(), "cb-1", "done", true),
	)
	require.Len(t, bot.callbacks, 1)
	require.Equal(t, "cb-1", bot.callbacks[0].CallbackQueryID)
	require.True(t, bot.callbacks[0].ShowAlert)
}

func TestChannelIsDMAllowed(t *testing.T) {
	t.Parallel()

	bot := &stubBot{}
	ch := &Channel{
		bot:      bot,
		dmPolicy: dmPolicyAllowlist,
	}

	ok, err := ch.isDMAllowed(context.Background(), 1, "u1")
	require.NoError(t, err)
	require.False(t, ok)
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, notAllowedMessage)

	ch.allowUsers = map[string]struct{}{"u1": {}}
	ok, err = ch.isDMAllowed(context.Background(), 1, "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ch.dmPolicy = dmPolicyDisabled
	ok, err = ch.isDMAllowed(context.Background(), 1, "u1")
	require.NoError(t, err)
	require.False(t, ok)

	ch.dmPolicy = dmPolicyOpen
	ok, err = ch.isDMAllowed(context.Background(), 1, "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ch.dmPolicy = dmPolicyPairing
	ch.pairing = nil
	ok, err = ch.isDMAllowed(context.Background(), 1, "u1")
	require.Error(t, err)
	require.False(t, ok)

	ch.pairing = &stubPairingStore{approved: true}
	ok, err = ch.isDMAllowed(context.Background(), 1, "u1")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestChannelIsDMAllowed_PairingRequestAndUnsupportedPolicy(t *testing.T) {
	t.Parallel()

	bot := &stubBot{}
	pairing := &stubPairingStore{code: "654321"}
	ch := &Channel{
		bot:      bot,
		dmPolicy: dmPolicyPairing,
		pairing:  pairing,
	}

	ok, err := ch.isDMAllowed(context.Background(), 7, "u7")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, []string{"u7"}, pairing.requested)
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "654321")

	ch.dmPolicy = "weird"
	ok, err = ch.isDMAllowed(context.Background(), 7, "u7")
	require.Error(t, err)
	require.False(t, ok)
}

func TestChannelHandleCallbackQuery_GuardsAndFiltering(t *testing.T) {
	t.Parallel()

	bot := &stubBot{}
	ch := &Channel{
		bot:        bot,
		allowUsers: map[string]struct{}{"1": {}},
	}
	ctx := context.Background()

	err := ch.handleCallbackQuery(ctx, tgapi.CallbackQuery{
		ID: "cb-empty",
	})
	require.NoError(t, err)

	err = ch.handleCallbackQuery(ctx, tgapi.CallbackQuery{
		ID:   "cb-group",
		Data: "noop",
		From: &tgapi.User{ID: 1},
		Message: &tgapi.Message{
			Chat: &tgapi.Chat{
				ID:   100,
				Type: chatTypeSuperGroup,
			},
		},
	})
	require.NoError(t, err)

	err = ch.handleCallbackQuery(ctx, tgapi.CallbackQuery{
		ID:   "cb-user",
		Data: "noop",
		From: &tgapi.User{ID: 2},
		Message: &tgapi.Message{
			Chat: &tgapi.Chat{
				ID:   100,
				Type: chatTypeSuperGroup,
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, bot.callbacks, 3)
	require.Equal(t, "cb-empty", bot.callbacks[0].CallbackQueryID)
	require.Empty(t, bot.callbacks[0].Text)
	require.Equal(t, "cb-group", bot.callbacks[1].CallbackQueryID)
	require.Empty(t, bot.callbacks[1].Text)
	require.Equal(t, "cb-user", bot.callbacks[2].CallbackQueryID)
	require.Equal(t, notAllowedMessage, bot.callbacks[2].Text)
	require.True(t, bot.callbacks[2].ShowAlert)
}
