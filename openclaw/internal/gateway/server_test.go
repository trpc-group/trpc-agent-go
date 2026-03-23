//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	testTimeout   = 2 * time.Second
	testShortWait = 100 * time.Millisecond

	debugEventsFile     = "events.jsonl"
	debugMetaFile       = "meta.json"
	debugResultFile     = "result.json"
	debugAttachmentsDir = "attachments"
)

type stubRunner struct {
	mu        sync.Mutex
	callCount int
}

func (r *stubRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	r.callCount++
	r.mu.Unlock()

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("ok")},
			},
			Done: true,
		},
		RequestID: "req-1",
	}
	close(ch)
	return ch, nil
}

func (r *stubRunner) Close() error {
	return nil
}

func (r *stubRunner) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCount
}

type recordingRunner struct {
	mu    sync.Mutex
	calls int
	last  model.Message
}

func (r *recordingRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	msg model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	r.calls++
	r.last = msg
	r.mu.Unlock()

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("ok")},
			},
			Done: true,
		},
		RequestID: "req-1",
	}
	close(ch)
	return ch, nil
}

func (r *recordingRunner) Close() error {
	return nil
}

func (r *recordingRunner) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingRunner) Last() model.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

type runOptionsRunner struct {
	mu   sync.Mutex
	opts agent.RunOptions
}

func (r *runOptionsRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg := agent.RunOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	r.opts = cfg

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("ok")},
			},
			Done: true,
		},
		RequestID: "req-1",
	}
	close(ch)
	return ch, nil
}

func (r *runOptionsRunner) Close() error {
	return nil
}

func (r *runOptionsRunner) Options() agent.RunOptions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opts
}

type resolvingRunner struct {
	mu    sync.Mutex
	ctx   context.Context
	opts  agent.RunOptions
	msg   model.Message
	user  string
	sess  string
	calls int
}

func (r *resolvingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	msg model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg := agent.RunOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	r.ctx = ctx
	r.opts = cfg
	r.msg = msg
	r.user = userID
	r.sess = sessionID
	r.calls++

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("ok")},
			},
			Done: true,
		},
		RequestID: "req-1",
	}
	close(ch)
	return ch, nil
}

func (r *resolvingRunner) Close() error {
	return nil
}

func strPtr(s string) *string {
	return &s
}

type staticRunner struct {
	events []*event.Event
	err    error
}

func (r *staticRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	if r.err != nil {
		return nil, r.err
	}
	ch := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (r *staticRunner) Close() error {
	return nil
}

type managedRunnerStub struct {
	stubRunner

	cancelMu sync.Mutex
	canceled []string
	status   runner.RunStatus
}

type nonFlushingRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

type failingFlusherRecorder struct {
	header http.Header
	code   int
	err    error
}

func (r *nonFlushingRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *nonFlushingRecorder) WriteHeader(code int) {
	r.code = code
}

func (r *nonFlushingRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(b)
}

func (r *failingFlusherRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *failingFlusherRecorder) WriteHeader(code int) {
	r.code = code
}

func (r *failingFlusherRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	if r.err == nil {
		r.err = errors.New("write failed")
	}
	return 0, r.err
}

func (r *failingFlusherRecorder) Flush() {}

func (m *managedRunnerStub) Cancel(requestID string) bool {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	m.canceled = append(m.canceled, requestID)
	return requestID == m.status.RequestID
}

func (m *managedRunnerStub) RunStatus(
	requestID string,
) (runner.RunStatus, bool) {
	if requestID != m.status.RequestID {
		return runner.RunStatus{}, false
	}
	return m.status, true
}

func TestDefaultSessionID_DM(t *testing.T) {
	t.Parallel()
	id, err := DefaultSessionID(InboundMessage{
		Channel: "http",
		From:    "u1",
	})
	require.NoError(t, err)
	require.Equal(t, "http:dm:u1", id)
}

func TestDefaultSessionID_Thread(t *testing.T) {
	t.Parallel()
	id, err := DefaultSessionID(InboundMessage{
		Channel: "http",
		From:    "u1",
		Thread:  "g1",
	})
	require.NoError(t, err)
	require.Equal(t, "http:thread:g1", id)
}

func TestBuildUploadContextText(t *testing.T) {
	t.Parallel()

	text := buildUploadContextText([]uploads.ListedFile{
		{
			Name: "voice.ogg",
			Path: "/tmp/voice.ogg",
		},
		{
			Name: "clip.mp4",
			Path: "/tmp/clip.mp4",
		},
		{
			Name: "report.pdf",
			Path: "/tmp/report.pdf",
		},
	})
	require.Contains(t, text, "voice.ogg [audio]")
	require.Contains(t, text, "clip.mp4 [video]")
	require.Contains(t, text, "report.pdf [pdf]")
	require.Contains(t, text, recentUploadKindHeader)
	require.Contains(t, text, "- audio: voice.ogg")
	require.Contains(t, text, "- video: clip.mp4")
	require.Contains(t, text, "- pdf: report.pdf")
}

func TestBuildUploadContextText_UsesPersistedMimeType(t *testing.T) {
	t.Parallel()

	text := buildUploadContextText([]uploads.ListedFile{{
		Name:     "video-note",
		Path:     "/tmp/video-note",
		MimeType: "video/mp4",
	}})
	require.Contains(t, text, "video-note [video]")
	require.Contains(t, text, "- video: video-note")
}

func TestBuildUploadContextText_RewritesGeneratedNames(t *testing.T) {
	t.Parallel()

	text := buildUploadContextText([]uploads.ListedFile{
		{
			Name:     "file_10.mp4",
			Path:     "/tmp/file_10.mp4",
			MimeType: "video/mp4",
		},
		{
			Name:     "file_11.ogg",
			Path:     "/tmp/file_11.ogg",
			MimeType: "audio/ogg",
		},
	})
	require.Contains(t, text, "video.mp4 [video]")
	require.Contains(t, text, "audio.ogg [audio]")
	require.Contains(t, text, "- video: video.mp4")
	require.Contains(t, text, "- audio: audio.ogg")
	require.NotContains(t, text, "file_10.mp4 [video]")
	require.NotContains(t, text, "file_11.ogg [audio]")
}

func TestServerUploadContextMessages(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.Save(
		context.Background(),
		scope,
		"clip.mp4",
		[]byte("video"),
	)
	require.NoError(t, err)

	srv := &Server{uploads: store}
	msgs := srv.uploadContextMessages("u1", "telegram:dm:u1:s1")
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Content, recentUploadContextHeader)
	require.Contains(t, msgs[0].Content, "clip.mp4 [video]")
}

func TestServerUploadContextMessages_UsesStoredMimeType(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	store, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: "telegram:dm:u1:s1",
	}
	_, err = store.SaveWithMetadata(
		context.Background(),
		scope,
		"video-note",
		"video/mp4",
		[]byte("video"),
	)
	require.NoError(t, err)

	srv := &Server{uploads: store}
	msgs := srv.uploadContextMessages("u1", "telegram:dm:u1:s1")
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Content, "video-note [video]")
}

func TestServerInjectedContextMessages_IncludePersonaAndUploads(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	uploadStore, err := uploads.NewStore(stateDir)
	require.NoError(t, err)

	personaPath, err := persona.DefaultStorePath(stateDir)
	require.NoError(t, err)
	personaStore, err := persona.NewStore(personaPath)
	require.NoError(t, err)

	sessionID := "telegram:dm:u1:rotated"
	scope := uploads.Scope{
		Channel:   "telegram",
		UserID:    "u1",
		SessionID: sessionID,
	}
	_, err = uploadStore.Save(
		context.Background(),
		scope,
		"clip.mp4",
		[]byte("video"),
	)
	require.NoError(t, err)

	_, err = personaStore.Set(
		context.Background(),
		persona.DMScopeKey("telegram", "u1"),
		persona.PresetGirlfriend,
	)
	require.NoError(t, err)

	srv := &Server{
		uploads:      uploadStore,
		personaStore: personaStore,
	}
	msgs := srv.injectedContextMessages("u1", sessionID)
	require.Len(t, msgs, 2)
	require.Contains(t, msgs[0].Content, personaContextHeader)
	require.Contains(t, msgs[0].Content, persona.PresetGirlfriend)
	require.Contains(t, msgs[1].Content, recentUploadContextHeader)
	require.Contains(t, msgs[1].Content, "clip.mp4 [video]")
}

func TestDefaultSessionID_MissingFromForDM(t *testing.T) {
	t.Parallel()
	_, err := DefaultSessionID(InboundMessage{
		Channel: "http",
	})
	require.Error(t, err)
}

func TestServer_Allowlist(t *testing.T) {
	t.Parallel()

	r := &stubRunner{}
	srv, err := New(r, WithAllowUsers("u1"))
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, r.Calls())

	reqBody, err = json.Marshal(gwproto.MessageRequest{
		From: "u2",
		Text: "hello",
	})
	require.NoError(t, err)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, 1, r.Calls())
}

func TestServer_MentionGating(t *testing.T) {
	t.Parallel()

	r := &stubRunner{}
	srv, err := New(
		r,
		WithRequireMentionInThreads(true),
		WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From:   "u1",
		Thread: "g1",
		Text:   "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 0, r.Calls())

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.True(t, rsp.Ignored)
}

func TestServer_MentionGating_ContentPartsText(t *testing.T) {
	t.Parallel()

	r := &stubRunner{}
	srv, err := New(
		r,
		WithRequireMentionInThreads(true),
		WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From:   "u1",
		Thread: "g1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeText,
				Text: strPtr("hello"),
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 0, r.Calls())

	reqBody, err = json.Marshal(gwproto.MessageRequest{
		From:   "u1",
		Thread: "g1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeText,
				Text: strPtr("hello @bot"),
			},
		},
	})
	require.NoError(t, err)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, r.Calls())
}

func TestServer_Messages_ContentParts_TextOnly(t *testing.T) {
	t.Parallel()

	r := &recordingRunner{}
	srv, err := New(
		r,
		WithAllowPrivateContentPartURLs(true),
	)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeText,
				Text: strPtr("hello"),
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, r.Calls())

	msg := r.Last()
	require.Equal(t, model.RoleUser, msg.Role)
	require.Empty(t, msg.Content)
	require.Len(t, msg.ContentParts, 1)
	require.Equal(t, model.ContentTypeText, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Text)
	require.Equal(t, "hello", *msg.ContentParts[0].Text)
}

func TestServer_Messages_ContentParts_ImageOnly(t *testing.T) {
	t.Parallel()

	r := &recordingRunner{}
	srv, err := New(r)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeImage,
				Image: &gwproto.ImagePart{
					URL: "https://example.com/image.png",
				},
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, r.Calls())

	msg := r.Last()
	require.Equal(t, model.RoleUser, msg.Role)
	require.Empty(t, msg.Content)
	require.Len(t, msg.ContentParts, 1)
	require.Equal(t, model.ContentTypeImage, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Image)
	require.Equal(
		t,
		"https://example.com/image.png",
		msg.ContentParts[0].Image.URL,
	)
	require.Equal(t, "auto", msg.ContentParts[0].Image.Detail)
}

func TestServer_Messages_ContentParts_URLFetch(t *testing.T) {
	t.Parallel()

	const (
		testAudioPath = "/a.wav"
		testFilePath  = "/report.pdf"
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,
		req *http.Request) {
		switch req.URL.Path {
		case testAudioPath:
			w.Header().Set(headerContentType, "audio/wav")
			w.Header().Set(
				"Content-Disposition",
				`attachment; filename="a.wav"`,
			)
			_, _ = w.Write([]byte("wavdata"))
		case testFilePath:
			w.Header().Set(headerContentType, "application/pdf")
			w.Header().Set(
				"Content-Disposition",
				`attachment; filename="report.pdf"`,
			)
			_, _ = w.Write([]byte("%PDF-1.4"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(ts.Close)

	r := &recordingRunner{}
	srv, err := New(
		r,
		WithAllowPrivateContentPartURLs(true),
	)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeAudio,
				Audio: &gwproto.AudioPart{
					URL: ts.URL + testAudioPath,
				},
			},
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					URL: ts.URL + testFilePath,
				},
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, r.Calls())

	msg := r.Last()
	require.Len(t, msg.ContentParts, 2)

	require.Equal(t, model.ContentTypeAudio, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Audio)
	require.Equal(t, "wav", msg.ContentParts[0].Audio.Format)
	require.Equal(t, []byte("wavdata"), msg.ContentParts[0].Audio.Data)

	require.Equal(t, model.ContentTypeFile, msg.ContentParts[1].Type)
	require.NotNil(t, msg.ContentParts[1].File)
	require.Equal(t, "report.pdf", msg.ContentParts[1].File.Name)
	require.Equal(
		t,
		"application/pdf",
		msg.ContentParts[1].File.MimeType,
	)
	require.Equal(t, []byte("%PDF-1.4"), msg.ContentParts[1].File.Data)
}

func TestServer_Messages_ContentParts_URLFetch_BlocksPrivateByDefault(
	t *testing.T,
) {
	t.Parallel()

	const testPath = "/a.wav"

	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		req *http.Request,
	) {
		hits.Add(1)
		switch req.URL.Path {
		case testPath:
			w.Header().Set(headerContentType, "audio/wav")
			_, _ = w.Write([]byte("wavdata"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(ts.Close)

	r := &recordingRunner{}
	srv, err := New(r)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeAudio,
				Audio: &gwproto.AudioPart{
					URL: ts.URL + testPath,
				},
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, 0, r.Calls())
	require.Equal(t, int32(0), hits.Load())

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, "private address")
}

func TestServer_Messages_ContentParts_URLFetch_AllowedDomains_Blocks(
	t *testing.T,
) {
	t.Parallel()

	const testPath = "/docs/a.wav"

	ts := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		req *http.Request,
	) {
		switch req.URL.Path {
		case testPath:
			w.Header().Set(headerContentType, "audio/wav")
			_, _ = w.Write([]byte("wavdata"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(ts.Close)

	r := &recordingRunner{}
	srv, err := New(
		r,
		WithAllowPrivateContentPartURLs(true),
		WithAllowedContentPartDomains("example.com"),
	)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeAudio,
				Audio: &gwproto.AudioPart{
					URL: ts.URL + testPath,
				},
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, 0, r.Calls())

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, "allowed pattern")
}

func TestServer_Messages_ContentParts_URLFetch_AllowedDomains_Allows(
	t *testing.T,
) {
	t.Parallel()

	const (
		allowPattern = "127.0.0.1/docs"
		testPath     = "/docs/a.wav"
	)

	ts := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		req *http.Request,
	) {
		switch req.URL.Path {
		case testPath:
			w.Header().Set(headerContentType, "audio/wav")
			_, _ = w.Write([]byte("wavdata"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(ts.Close)

	r := &recordingRunner{}
	srv, err := New(
		r,
		WithAllowPrivateContentPartURLs(true),
		WithAllowedContentPartDomains(allowPattern),
	)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeAudio,
				Audio: &gwproto.AudioPart{
					URL: ts.URL + testPath,
				},
			},
		},
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, r.Calls())

	msg := r.Last()
	require.Len(t, msg.ContentParts, 1)
	require.Equal(t, model.ContentTypeAudio, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].Audio)
	require.Equal(t, []byte("wavdata"), msg.ContentParts[0].Audio.Data)
}

func TestServer_ProcessMessage_LargeInlineData(t *testing.T) {
	t.Parallel()

	r := &recordingRunner{}
	srv, err := New(r)
	require.NoError(t, err)

	data := bytes.Repeat([]byte("a"), int(defaultMaxBodyBytes))
	req := gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					Filename: "a.txt",
					Data:     data,
				},
			},
		},
	}

	rsp, status := srv.ProcessMessage(context.Background(), req)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", rsp.Reply)
	require.Equal(t, "req-1", rsp.RequestID)

	msg := r.Last()
	require.Len(t, msg.ContentParts, 1)
	require.Equal(t, model.ContentTypeFile, msg.ContentParts[0].Type)
	require.NotNil(t, msg.ContentParts[0].File)
	require.Len(t, msg.ContentParts[0].File.Data, len(data))

	body, err := json.Marshal(req)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(body),
	)
	srv.Handler().ServeHTTP(rr, httpReq)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestServer_ProcessMessage_FileUploadStorePersistsHostRef(
	t *testing.T,
) {
	t.Parallel()

	store, err := uploads.NewStore(t.TempDir())
	require.NoError(t, err)

	r := &recordingRunner{}
	srv, err := New(r, WithUploadStore(store))
	require.NoError(t, err)

	pdfBytes := []byte("%PDF-1.4")
	req := gwproto.MessageRequest{
		Channel:   "telegram",
		From:      "u1",
		SessionID: "telegram:dm:u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					Filename: "report.pdf",
					Data:     pdfBytes,
				},
			},
		},
	}

	rsp, status := srv.ProcessMessage(context.Background(), req)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", rsp.Reply)

	msg := r.Last()
	require.Len(t, msg.ContentParts, 1)
	require.NotNil(t, msg.ContentParts[0].File)
	filePart := msg.ContentParts[0].File
	require.Equal(t, "report.pdf", filePart.Name)
	require.Empty(t, filePart.Data)
	require.NotEmpty(t, filePart.FileID)

	path, ok := uploads.PathFromHostRef(filePart.FileID)
	require.True(t, ok)
	require.Contains(t, path, store.Root())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, pdfBytes, data)
}

func TestServer_ProcessMessage_IncludesRecentUploadContext(t *testing.T) {
	t.Parallel()

	store, err := uploads.NewStore(t.TempDir())
	require.NoError(t, err)

	runner := &runOptionsRunner{}
	srv, err := New(runner, WithUploadStore(store))
	require.NoError(t, err)

	req := gwproto.MessageRequest{
		Channel:   "telegram",
		From:      "u1",
		SessionID: "telegram:dm:u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					Filename: "report.pdf",
					Data:     []byte("%PDF"),
				},
			},
		},
	}

	rsp, status := srv.ProcessMessage(context.Background(), req)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", rsp.Reply)

	opts := runner.Options()
	require.Len(t, opts.InjectedContextMessages, 1)
	require.Contains(
		t,
		opts.InjectedContextMessages[0].Content,
		"report.pdf [pdf]",
	)
	require.Contains(
		t,
		opts.InjectedContextMessages[0].Content,
		"OPENCLAW_RECENT_UPLOADS_JSON",
	)
}

func TestServer_ProcessMessage_RunOptionResolver(t *testing.T) {
	t.Parallel()

	type ctxKey string

	const (
		resolverKey ctxKey = "resolver"
		resolverTag        = "langfuse"
	)

	runner := &resolvingRunner{}
	srv, err := New(
		runner,
		WithRunOptionResolver(func(
			ctx context.Context,
			input RunOptionInput,
		) (context.Context, []agent.RunOption) {
			require.Equal(t, "telegram", input.Inbound.Channel)
			require.Equal(t, "u1", input.Inbound.From)
			require.Equal(t, "msg-1", input.Inbound.MessageID)
			require.Equal(t, "u1", input.UserID)
			require.Equal(t, "telegram:dm:u1", input.SessionID)
			require.Equal(t, "req-in", input.RequestID)
			require.Equal(t, "hello", input.Message.Content)
			return context.WithValue(
					ctx,
					resolverKey,
					resolverTag,
				), []agent.RunOption{
					agent.WithInstruction("resolver"),
				}
		}),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			Channel:   "telegram",
			From:      "u1",
			MessageID: "msg-1",
			RequestID: "req-in",
			Text:      "hello",
		},
	)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "req-1", rsp.RequestID)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, resolverTag, runner.ctx.Value(resolverKey))
	require.Equal(t, "resolver", runner.opts.Instruction)
	require.Equal(t, "req-in", runner.opts.RequestID)
	require.Equal(t, "u1", runner.user)
	require.Equal(t, "telegram:dm:u1", runner.sess)
	require.Equal(t, "hello", runner.msg.Content)
	require.Equal(t, 1, runner.calls)
}

func TestServer_ProcessMessage_RunOptionResolver_NoExtraOptions(
	t *testing.T,
) {
	t.Parallel()

	type ctxKey string

	const resolverKey ctxKey = "resolver"

	runner := &resolvingRunner{}
	srv, err := New(
		runner,
		WithRunOptionResolver(func(
			ctx context.Context,
			_ RunOptionInput,
		) (context.Context, []agent.RunOption) {
			return context.WithValue(ctx, resolverKey, "ok"), nil
		}),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			Channel: "telegram",
			From:    "u1",
			Text:    "hello",
		},
	)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "req-1", rsp.RequestID)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, "ok", runner.ctx.Value(resolverKey))
	require.Empty(t, runner.opts.Instruction)
}

func TestServer_ProcessMessage_RunOptionResolver_Composes(t *testing.T) {
	t.Parallel()

	type ctxKey string

	const (
		firstKey  ctxKey = "first"
		secondKey ctxKey = "second"
		firstTag         = "langfuse"
		secondTag        = "audit"
	)

	runner := &resolvingRunner{}
	srv, err := New(
		runner,
		WithRunOptionResolver(func(
			ctx context.Context,
			_ RunOptionInput,
		) (context.Context, []agent.RunOption) {
			return context.WithValue(
					ctx,
					firstKey,
					firstTag,
				), []agent.RunOption{
					agent.WithTraceStartedCallback(
						func(oteltrace.SpanContext) {},
					),
				}
		}),
		WithRunOptionResolver(func(
			ctx context.Context,
			_ RunOptionInput,
		) (context.Context, []agent.RunOption) {
			require.Equal(t, firstTag, ctx.Value(firstKey))
			return context.WithValue(
					ctx,
					secondKey,
					secondTag,
				), []agent.RunOption{
					agent.WithTraceStartedCallback(
						func(oteltrace.SpanContext) {},
					),
				}
		}),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			Channel: "telegram",
			From:    "u1",
			Text:    "hello",
		},
	)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "req-1", rsp.RequestID)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, firstTag, runner.ctx.Value(firstKey))
	require.Equal(t, secondTag, runner.ctx.Value(secondKey))
	require.Len(t, runner.opts.TraceStartedCallbacks, 2)
}

func TestServer_ProcessMessage_DebugRecorderWritesTrace(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("full")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	r := &recordingRunner{}
	srv, err := New(r, WithDebugRecorder(rec))
	require.NoError(t, err)

	data := []byte("hello")
	req := gwproto.MessageRequest{
		From: "u1",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					Filename: "a.txt",
					Data:     data,
				},
			},
		},
	}

	rsp, status := srv.ProcessMessage(context.Background(), req)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", rsp.Reply)

	matches, err := filepath.Glob(
		filepath.Join(dir, "*", "*", debugEventsFile),
	)
	require.NoError(t, err)
	require.Len(t, matches, 1)

	root := filepath.Dir(matches[0])
	_, err = os.Stat(filepath.Join(root, debugMetaFile))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, debugResultFile))
	require.NoError(t, err)

	sum := sha256.Sum256(data)
	shaHex := hex.EncodeToString(sum[:])
	dst := filepath.Join(root, debugAttachmentsDir, shaHex)
	_, err = os.Stat(dst)
	require.NoError(t, err)
}

func TestServer_ProcessMessage_DebugRecorder_IgnoresStatus(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	srv, err := New(
		&stubRunner{},
		WithDebugRecorder(rec),
		WithRequireMentionInThreads(true),
		WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			From:   "u1",
			Thread: "g1",
			Text:   "hello",
		},
	)
	require.Equal(t, http.StatusOK, status)
	require.True(t, rsp.Ignored)
}

func TestServer_ProcessMessage_DebugRecorder_MissingText(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	srv, err := New(&stubRunner{}, WithDebugRecorder(rec))
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1"},
	)
	require.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, "missing text")
}

func TestServer_ProcessMessage_DebugRecorder_Unauthorized(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	srv, err := New(
		&stubRunner{},
		WithAllowUsers("u2"),
		WithDebugRecorder(rec),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1", Text: "hello"},
	)
	require.Equal(t, http.StatusForbidden, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeUnauthorized, rsp.Error.Type)
}

func TestServer_ProcessMessage_DebugRecorder_RunError(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	srv, err := New(
		&staticRunner{err: errors.New("runner boom")},
		WithDebugRecorder(rec),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1", Text: "hello"},
	)
	require.Equal(t, http.StatusInternalServerError, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInternal, rsp.Error.Type)
}

func TestServer_ProcessMessage_MissingUserIDAndFrom(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{Text: "hello"},
	)
	require.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, "missing user_id")
}

func TestServer_ProcessMessage_RequireMention_Ignores(t *testing.T) {
	t.Parallel()

	srv, err := New(
		&stubRunner{},
		WithRequireMentionInThreads(true),
		WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			From:   "u1",
			Thread: "g1",
			Text:   "hello",
		},
	)
	require.Equal(t, http.StatusOK, status)
	require.True(t, rsp.Ignored)
	require.Nil(t, rsp.Error)
}

func TestServer_ProcessMessage_SessionIDFuncError(t *testing.T) {
	t.Parallel()

	srv, err := New(
		&stubRunner{},
		WithSessionIDFunc(func(_ InboundMessage) (string, error) {
			return "", errors.New("bad sid")
		}),
	)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			From: "u1",
			Text: "hello",
		},
	)
	require.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, "bad sid")
}

func TestServer_ProcessMessage_RunError(t *testing.T) {
	t.Parallel()

	r := &staticRunner{err: errors.New("runner boom")}
	srv, err := New(r)
	require.NoError(t, err)

	rsp, status := srv.ProcessMessage(
		context.Background(),
		gwproto.MessageRequest{
			From: "u1",
			Text: "hello",
		},
	)
	require.Equal(t, http.StatusInternalServerError, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInternal, rsp.Error.Type)
	require.Contains(t, rsp.Error.Message, "runner boom")
}

func TestServer_New_RequireMentionWithoutPatterns(t *testing.T) {
	t.Parallel()

	_, err := New(
		&stubRunner{},
		WithRequireMentionInThreads(true),
	)
	require.Error(t, err)
}

func TestServer_Health(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	require.Equal(t, defaultBasePath, srv.BasePath())

	statusPath, err := joinURLPath(defaultBasePath, defaultStatusPath)
	require.NoError(t, err)
	require.Equal(t, statusPath, srv.StatusPath())

	cancelPath, err := joinURLPath(defaultBasePath, defaultCancelPath)
	require.NoError(t, err)
	require.Equal(t, cancelPath, srv.CancelPath())

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.HealthPath(), nil)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var payload map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &payload))
	require.Equal(t, "ok", payload["status"])

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, srv.HealthPath(), nil)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, http.MethodGet, rr.Header().Get(headerAllow))
}

func TestServer_New_InvalidPaths(t *testing.T) {
	t.Parallel()

	_, err := New(&stubRunner{}, WithBasePath("http://[::1"))
	require.Error(t, err)
}

type blockingRunner struct {
	t *testing.T

	started chan int
	release chan struct{}

	mu     sync.Mutex
	active map[string]int
	calls  int
}

func newBlockingRunner(t *testing.T) *blockingRunner {
	t.Helper()
	return &blockingRunner{
		t:       t,
		started: make(chan int, 2),
		release: make(chan struct{}),
		active:  make(map[string]int),
	}
}

func (r *blockingRunner) Run(
	_ context.Context,
	_ string,
	sessionID string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.mu.Lock()
	r.active[sessionID]++
	if r.active[sessionID] > 1 {
		r.t.Errorf("concurrent run for session %q", sessionID)
	}
	idx := r.calls
	r.calls++
	r.mu.Unlock()

	r.started <- idx
	if idx == 0 {
		<-r.release
	}

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("ok")},
			},
			Done: true,
		},
	}
	close(ch)

	r.mu.Lock()
	r.active[sessionID]--
	r.mu.Unlock()
	return ch, nil
}

func (r *blockingRunner) Close() error {
	return nil
}

func TestServer_SerializesRunsPerSession(t *testing.T) {
	t.Parallel()

	r := newBlockingRunner(t)
	srv, err := New(r)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	req1 := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	req2 := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)

	var wg sync.WaitGroup
	wg.Add(2)

	rr1 := httptest.NewRecorder()
	go func() {
		defer wg.Done()
		srv.Handler().ServeHTTP(rr1, req1)
	}()

	select {
	case idx := <-r.started:
		require.Equal(t, 0, idx)
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting for first run start")
	}

	rr2 := httptest.NewRecorder()
	go func() {
		defer wg.Done()
		srv.Handler().ServeHTTP(rr2, req2)
	}()

	select {
	case idx := <-r.started:
		t.Fatalf("unexpected second run start: %d", idx)
	case <-time.After(testShortWait):
	}

	close(r.release)

	select {
	case idx := <-r.started:
		require.Equal(t, 1, idx)
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting for second run start")
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting for handlers to finish")
	}

	require.Equal(t, http.StatusOK, rr1.Code)
	require.Equal(t, http.StatusOK, rr2.Code)
}

func TestServer_StatusAndCancel_WhenUnsupported(t *testing.T) {
	t.Parallel()

	r := &stubRunner{}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.statusPath, nil)
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotImplemented, rr.Code)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, srv.cancelPath, nil)
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotImplemented, rr.Code)
}

func TestServer_StatusAndCancel(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		srv.statusPath+"?request_id=req-1",
		nil,
	)
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	rr = httptest.NewRecorder()
	body, err := json.Marshal(cancelRequest{RequestID: "req-1"})
	require.NoError(t, err)
	req = httptest.NewRequest(
		http.MethodPost,
		srv.cancelPath,
		bytes.NewReader(body),
	)
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestServer_Status_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, srv.statusPath, nil)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, http.MethodGet, rr.Header().Get(headerAllow))
}

func TestServer_Status_NotFound(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		srv.statusPath+"?request_id=missing",
		nil,
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestServer_Messages_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.MessagesPath(), nil)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, http.MethodPost, rr.Header().Get(headerAllow))
}

func TestServer_Messages_InvalidJSON(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader([]byte("{not json")),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
}

func TestServer_Messages_MissingText(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{From: "u1"})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
}

func TestServer_Messages_MissingUserIDAndFrom(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{Text: "hello"})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
}

func TestServer_Messages_SessionIDDerivationError(t *testing.T) {
	t.Parallel()

	srv, err := New(&stubRunner{})
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		UserID: "u1",
		Text:   "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
}

func TestServer_Messages_RunnerError(t *testing.T) {
	t.Parallel()

	r := &staticRunner{err: errors.New("boom")}
	srv, err := New(r)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInternal, rsp.Error.Type)
}

func TestServer_Messages_EventError(t *testing.T) {
	t.Parallel()

	r := &staticRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Object: model.ObjectTypeError,
					Error: &model.ResponseError{
						Message: "api failed",
					},
					Done: true,
				},
				RequestID: "req-err",
			},
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInternal, rsp.Error.Type)
}

func TestServer_ProcessMessage_NilServer(t *testing.T) {
	t.Parallel()

	var srv *Server
	rsp, status := srv.ProcessMessage(nil, gwproto.MessageRequest{})
	require.Equal(t, http.StatusInternalServerError, status)
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInternal, rsp.Error.Type)
	require.Equal(t, "nil server", rsp.Error.Message)
}

func TestServer_Messages_EmptyReply(t *testing.T) {
	t.Parallel()

	r := &staticRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletion,
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("")},
					},
					Done: true,
				},
			},
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.Nil(t, rsp.Error)
	require.Equal(t, emptyReplyFallbackText, rsp.Reply)
}

func TestServer_Status_MissingRequestID(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.statusPath, nil)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestServer_CancelRequest_NilContext(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	canceled, apiErr, status := srv.CancelRequest(nil, "req-1")
	require.True(t, canceled)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)
}

func TestServer_CancelRequest_NoMatchReturnsFalse(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	canceled, apiErr, status := srv.CancelRequest(
		context.Background(),
		"missing",
	)
	require.False(t, canceled)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)
}

func TestServer_Cancel_MissingRequestID(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	body, err := json.Marshal(cancelRequest{})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.cancelPath,
		bytes.NewReader(body),
	)
	srv.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestServer_Cancel_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, srv.cancelPath, nil)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, http.MethodPost, rr.Header().Get(headerAllow))
}

func TestServer_Cancel_InvalidJSON(t *testing.T) {
	t.Parallel()

	r := &managedRunnerStub{
		status: runner.RunStatus{
			RequestID: "req-1",
			AgentName: "agent",
		},
	}
	srv, err := New(r)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.cancelPath,
		bytes.NewReader([]byte("{not json")),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInvalidRequest, rsp.Error.Type)
}

func TestWithMentionPatterns(t *testing.T) {
	t.Parallel()

	o := newOptions(WithMentionPatterns())
	require.Nil(t, o.mentionPatterns)

	o = newOptions(WithMentionPatterns("", "  ", "@bot"))
	require.Equal(t, []string{"@bot"}, o.mentionPatterns)
}

func TestContainsAny(t *testing.T) {
	t.Parallel()

	require.True(t, containsAny("hi @bot", []string{"", "@bot"}))
	require.False(t, containsAny("hi", []string{"@bot"}))
}

func TestReplyAccumulator_ChunksAndFull(t *testing.T) {
	t.Parallel()

	acc := newReplyAccumulator()
	acc.Consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{
				{Delta: model.Message{Content: "a"}},
				{Delta: model.Message{Content: "b"}},
			},
		},
		RequestID: "req-1",
	})
	acc.Consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{
				{Delta: model.Message{Content: "c"}},
			},
		},
	})
	require.Equal(t, "abc", acc.Text)
	require.Equal(t, "req-1", acc.RequestID)

	acc.Consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("full")},
			},
		},
	})
	acc.Consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{
				{Delta: model.Message{Content: "x"}},
			},
		},
	})
	require.Equal(t, "full", acc.Text)
}

func TestReplyAccumulator_IgnoresNilAndUnsupported(t *testing.T) {
	t.Parallel()

	acc := newReplyAccumulator()
	acc.Consume(nil)
	acc.Consume(&event.Event{})

	acc.Consume(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
		},
	})
	acc.consumeFull(nil)
	acc.consumeDelta(nil)
}

func TestServer_StreamMessage_Success(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletionChunk,
					Choices: []model.Choice{
						{
							Delta: model.Message{Content: "help"},
						},
					},
				},
				RequestID: "req-1",
			},
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletionChunk,
					Choices: []model.Choice{
						{
							Delta: model.Message{Content: " me"},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		testTimeout,
	)
	defer cancel()

	stream, apiErr, status := srv.StreamMessage(ctx, gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)

	events := collectGatewayStreamEvents(t, stream)
	require.Len(t, events, 6)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunStarted,
		events[0].Type,
	)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunProgress,
		events[1].Type,
	)
	require.Equal(
		t,
		gwproto.StreamProgressStagePreparing,
		events[1].Stage,
	)
	require.Equal(t, progressSummaryPrepare, events[1].Summary)
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageDelta,
		events[2].Type,
	)
	require.Equal(t, "help", events[2].Delta)
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageDelta,
		events[3].Type,
	)
	require.Equal(t, " me", events[3].Delta)
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageCompleted,
		events[4].Type,
	)
	require.Equal(t, "help me", events[4].Reply)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunCompleted,
		events[5].Type,
	)
	require.Equal(t, "req-1", events[5].RequestID)
}

func TestServer_StreamMessage_RunError(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{err: errors.New("boom")})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		testTimeout,
	)
	defer cancel()

	stream, apiErr, status := srv.StreamMessage(ctx, gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)

	events := collectGatewayStreamEvents(t, stream)
	require.Len(t, events, 3)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunStarted,
		events[0].Type,
	)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunProgress,
		events[1].Type,
	)
	require.Equal(
		t,
		gwproto.StreamProgressStagePreparing,
		events[1].Stage,
	)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunError,
		events[2].Type,
	)
	require.NotNil(t, events[2].Error)
	require.Equal(t, "boom", events[2].Error.Message)
}

func TestServer_StreamMessage_EarlyResponses(t *testing.T) {
	t.Parallel()

	var nilSrv *Server
	stream, apiErr, status := nilSrv.StreamMessage(
		nil,
		gwproto.MessageRequest{},
	)
	require.Nil(t, stream)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, "nil server", apiErr.Message)

	srv, err := New(
		&staticRunner{},
		WithRequireMentionInThreads(true),
		WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	stream, apiErr, status = srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{
			From:   "u1",
			Thread: "thread-1",
			Text:   "hello",
		},
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)
	require.Equal(
		t,
		[]gwproto.StreamEvent{
			{
				Type:    gwproto.StreamEventTypeRunIgnored,
				Ignored: true,
			},
			{
				Type: gwproto.StreamEventTypeRunCompleted,
			},
		},
		collectGatewayStreamEvents(t, stream),
	)

	stream, apiErr, status = srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{Text: "hello"},
	)
	require.Nil(t, stream)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, apiErr.Message, "from")
}

func TestServer_StreamMessage_ProgressStages(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletion,
					Choices: []model.Choice{{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name: "read_document",
									Arguments: []byte(
										`{"page":2}`,
									),
								},
							}},
						},
					}},
				},
			},
			{
				Response: &model.Response{
					Object: model.ObjectTypeToolResponse,
				},
			},
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletion,
					Choices: []model.Choice{
						{
							Message: model.NewAssistantMessage(
								"done",
							),
						},
					},
					Done: true,
				},
				RequestID: "req-1",
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		testTimeout,
	)
	defer cancel()

	stream, apiErr, status := srv.StreamMessage(ctx, gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)

	events := collectGatewayStreamEvents(t, stream)
	require.Len(t, events, 7)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunProgress,
		events[1].Type,
	)
	require.Equal(
		t,
		gwproto.StreamProgressStagePreparing,
		events[1].Stage,
	)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunProgress,
		events[2].Type,
	)
	require.Equal(
		t,
		gwproto.StreamProgressStageReadingDocument,
		events[2].Stage,
	)
	require.Equal(t, "Reading document page 2", events[2].Summary)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunProgress,
		events[3].Type,
	)
	require.Equal(
		t,
		gwproto.StreamProgressStageSummarizing,
		events[3].Stage,
	)
	require.Equal(t, progressSummaryAnswering, events[3].Summary)
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageDelta,
		events[4].Type,
	)
	require.Equal(t, "done", events[4].Delta)
	require.Equal(
		t,
		gwproto.StreamEventTypeMessageCompleted,
		events[5].Type,
	)
	require.Equal(t, "done", events[5].Reply)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunCompleted,
		events[6].Type,
	)
}

func TestStreamProgressHelpers(t *testing.T) {
	t.Parallel()

	update, ok := progressUpdateFromRunnerEvent(nil)
	require.False(t, ok)
	require.Equal(t, progressUpdate{}, update)

	update, ok = progressUpdateFromRunnerEvent(&event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					ToolCalls: []model.ToolCall{{
						Function: model.FunctionDefinitionParam{
							Name:      streamToolReadDocument,
							Arguments: []byte(`{"page":2}`),
						},
					}},
				},
			}},
		},
	})
	require.True(t, ok)
	require.Equal(
		t,
		gwproto.StreamProgressStageReadingDocument,
		update.stage,
	)
	require.Equal(t, "Reading document page 2", update.summary)

	update, ok = progressUpdateFromRunnerEvent(&event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
		},
	})
	require.True(t, ok)
	require.Equal(
		t,
		gwproto.StreamProgressStageSummarizing,
		update.stage,
	)
	require.Equal(t, progressSummaryAnswering, update.summary)
}

func TestToolCallProgressSummaries(t *testing.T) {
	t.Parallel()

	update, ok := progressFromToolCall(model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name:      streamToolReadSheet,
			Arguments: []byte(`{"start_row":2,"end_row":4}`),
		},
	})
	require.True(t, ok)
	require.Equal(
		t,
		gwproto.StreamProgressStageReadingSpreadsheet,
		update.stage,
	)
	require.Equal(t, "Reading spreadsheet rows 2-4", update.summary)

	update, ok = progressFromToolCall(model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name:      streamToolExecCommand,
			Arguments: []byte(`{"command":"go test ./..."}`),
		},
	})
	require.True(t, ok)
	require.Equal(
		t,
		gwproto.StreamProgressStageRunningTool,
		update.stage,
	)
	require.Equal(t, progressSummaryGoTest, update.summary)

	update, ok = progressFromToolCall(model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name:      streamToolExecCommand,
			Arguments: []byte(`{"command":"git status"}`),
		},
	})
	require.True(t, ok)
	require.Equal(t, progressSummaryGit, update.summary)

	update, ok = progressFromToolCall(model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name:      streamToolExecCommand,
			Arguments: []byte(`{"command":"rg TODO ."}`),
		},
	})
	require.True(t, ok)
	require.Equal(t, progressSummaryInspect, update.summary)

	update, ok = progressFromToolCall(model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name: "custom_tool",
		},
	})
	require.True(t, ok)
	require.Equal(t, "Running custom_tool", update.summary)

	_, ok = progressFromToolCall(model.ToolCall{})
	require.False(t, ok)

	require.Equal(
		t,
		progressSummaryDoc,
		readDocumentProgressSummary(model.ToolCall{
			Function: model.FunctionDefinitionParam{
				Arguments: []byte("{"),
			},
		}),
	)
	require.Equal(
		t,
		"Reading spreadsheet sheet Data",
		readSpreadsheetProgressSummary(model.ToolCall{
			Function: model.FunctionDefinitionParam{
				Arguments: []byte(`{"sheet":"Data"}`),
			},
		}),
	)
}

func TestSendProgressUpdateAndHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	out := make(chan gwproto.StreamEvent, 4)
	state := &progressState{startedAt: time.Now()}
	run := preparedMessageRun{
		sessionID: "s1",
		requestID: "r1",
	}

	require.True(t, sendProgressUpdate(
		ctx,
		out,
		run,
		state,
		gwproto.StreamProgressStagePreparing,
		progressSummaryPrepare,
	))
	require.True(t, sendProgressUpdate(
		ctx,
		out,
		run,
		state,
		gwproto.StreamProgressStagePreparing,
		progressSummaryPrepare,
	))
	require.Len(t, out, 1)
	evt := <-out
	require.Equal(t, gwproto.StreamEventTypeRunProgress, evt.Type)
	require.Equal(t, progressSummaryPrepare, evt.Summary)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	require.False(t, sendStreamEvent(
		canceledCtx,
		make(chan gwproto.StreamEvent),
		gwproto.StreamEvent{},
	))
	require.False(t, sendProgressUpdate(
		canceledCtx,
		make(chan gwproto.StreamEvent),
		run,
		&progressState{startedAt: time.Now()},
		gwproto.StreamProgressStagePreparing,
		progressSummaryPrepare,
	))

	collected := collectGatewayStreamEvents(
		t,
		singleStreamEvents(
			gwproto.StreamEvent{Type: gwproto.StreamEventTypeRunStarted},
			gwproto.StreamEvent{Type: gwproto.StreamEventTypeRunCompleted},
		),
	)
	require.Len(t, collected, 2)
	require.Equal(t, "stream canceled", contextErrMessage(nil))
	require.Equal(
		t,
		context.Canceled.Error(),
		contextErrMessage(canceledCtx),
	)
	require.Equal(t, "req", resolvedStreamRequestID(" req ", "fb"))
	require.Equal(t, "fb", resolvedStreamRequestID(" ", " fb "))
}

func TestStreamResponseHelpers(t *testing.T) {
	t.Parallel()

	require.Nil(t, apiErrorFromEvent(nil))
	require.Nil(t, apiErrorFromEvent(&event.Event{
		Response: &model.Response{},
	}))

	apiErr := apiErrorFromEvent(&event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "boom"},
		},
	})
	require.NotNil(t, apiErr)
	require.Equal(t, errTypeInternal, apiErr.Type)
	require.Equal(t, "boom", apiErr.Message)

	deltaEvt := &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{
				{Delta: model.Message{Content: "a"}},
				{Delta: model.Message{Content: "b"}},
			},
		},
	}
	require.Equal(t, "ab", streamDeltaText(deltaEvt, false))

	fullEvt := &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("full")},
			},
		},
	}
	require.Equal(t, "full", streamDeltaText(fullEvt, false))
	require.Empty(t, streamDeltaText(fullEvt, true))
	require.Empty(t, streamDeltaText(nil, false))

	require.Equal(
		t,
		model.ToolCall{
			Function: model.FunctionDefinitionParam{
				Name: "from-delta",
			},
		},
		func() model.ToolCall {
			call, ok := firstToolCall(&model.Response{
				Choices: []model.Choice{{
					Delta: model.Message{
						ToolCalls: []model.ToolCall{{
							Function: model.FunctionDefinitionParam{
								Name: "from-delta",
							},
						}},
					},
				}},
			})
			require.True(t, ok)
			return call
		}(),
	)
	call, ok := firstToolCall(&model.Response{})
	require.False(t, ok)
	require.Equal(t, model.ToolCall{}, call)

	require.Empty(t, fullTextFromResponse(&model.Response{}))
	require.Equal(t, "x", fullTextFromResponse(&model.Response{
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage("x")},
		},
	}))
	require.Empty(t, deltaTextFromResponse(nil))
}

func TestServer_HandleMessagesStream_Success(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{
		events: []*event.Event{
			{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletionChunk,
					Choices: []model.Choice{
						{
							Delta: model.Message{Content: "ok"},
						},
					},
				},
				RequestID: "req-1",
			},
		},
	})
	require.NoError(t, err)

	reqBody, err := json.Marshal(gwproto.MessageRequest{
		From: "u1",
		Text: "hello",
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesStreamPath(),
		bytes.NewReader(reqBody),
	)
	srv.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(
		t,
		gwproto.SSEContentType,
		rr.Header().Get(headerContentType),
	)
	require.Contains(t, rr.Body.String(), "event: message.delta")
	require.Contains(t, rr.Body.String(), `"reply":"ok"`)
}

func TestServer_HandleMessagesStream_ErrorPaths(t *testing.T) {
	t.Parallel()

	srv, err := New(
		&staticRunner{},
		WithAllowUsers("telegram:u1"),
	)
	require.NoError(t, err)

	methodRR := httptest.NewRecorder()
	methodReq := httptest.NewRequest(
		http.MethodGet,
		srv.MessagesStreamPath(),
		nil,
	)
	srv.Handler().ServeHTTP(methodRR, methodReq)
	require.Equal(t, http.StatusMethodNotAllowed, methodRR.Code)

	invalidRR := httptest.NewRecorder()
	invalidReq := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesStreamPath(),
		bytes.NewBufferString("{"),
	)
	srv.Handler().ServeHTTP(invalidRR, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRR.Code)
	require.Contains(t, invalidRR.Body.String(), errTypeInvalidRequest)

	noFlush := &nonFlushingRecorder{}
	srv.handleMessagesStream(
		noFlush,
		httptest.NewRequest(
			http.MethodPost,
			srv.MessagesStreamPath(),
			bytes.NewBufferString(`{"from":"u1","text":"hi"}`),
		),
	)
	require.Equal(t, http.StatusInternalServerError, noFlush.code)
	require.Contains(t, noFlush.body.String(), "streaming not supported")

	authRR := httptest.NewRecorder()
	authReq := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesStreamPath(),
		bytes.NewBufferString(
			`{"from":"u1","user_id":"telegram:u2","text":"hi"}`,
		),
	)
	srv.Handler().ServeHTTP(authRR, authReq)
	require.Equal(t, http.StatusForbidden, authRR.Code)
	require.Contains(t, authRR.Body.String(), errTypeUnauthorized)
}

func TestServer_StreamMessage_EmptyReplyFallback(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{})
	require.NoError(t, err)

	stream, apiErr, status := srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{
			From: "u1",
			Text: "hello",
		},
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)

	events := collectGatewayStreamEvents(t, stream)
	require.Len(t, events, 4)
	require.Equal(t, emptyReplyFallbackText, events[2].Reply)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunCompleted,
		events[3].Type,
	)
}

func TestServer_StreamMessage_CanceledRequest(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{})
	require.NoError(t, err)
	srv.canceled.Mark("req-1")

	stream, apiErr, status := srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{
			From:      "u1",
			Text:      "hello",
			RequestID: "req-1",
		},
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)

	events := collectGatewayStreamEvents(t, stream)
	require.Len(t, events, 3)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunStarted,
		events[0].Type,
	)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunProgress,
		events[1].Type,
	)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunCanceled,
		events[2].Type,
	)
	require.Empty(t, events[2].Reply)
}

func TestServer_StreamMessage_DebugRecorderPaths(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	srv, err := New(
		&staticRunner{
			events: []*event.Event{{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletionChunk,
					Choices: []model.Choice{{
						Delta: model.Message{Content: "ok"},
					}},
				},
				RequestID: "req-1",
			}},
		},
		WithDebugRecorder(rec),
	)
	require.NoError(t, err)

	stream, apiErr, status := srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1", Text: "hello"},
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, collectGatewayStreamEvents(t, stream), 5)

	matches, err := filepath.Glob(
		filepath.Join(dir, "*", "*", debugEventsFile),
	)
	require.NoError(t, err)
	require.Len(t, matches, 1)

	dir = t.TempDir()
	rec, err = debugrecorder.New(dir, mode)
	require.NoError(t, err)

	srv, err = New(
		&staticRunner{},
		WithDebugRecorder(rec),
		WithRequireMentionInThreads(true),
		WithMentionPatterns("@bot"),
	)
	require.NoError(t, err)

	stream, apiErr, status = srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{
			From:   "u1",
			Thread: "thread-1",
			Text:   "hello",
		},
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)
	require.Len(t, collectGatewayStreamEvents(t, stream), 2)

	matches, err = filepath.Glob(
		filepath.Join(dir, "*", "*", debugEventsFile),
	)
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

func TestServer_StreamMessage_APIErrorEvent(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{
		events: []*event.Event{{
			Response: &model.Response{
				Error: &model.ResponseError{
					Type:    errTypeUnauthorized,
					Message: "no access",
				},
			},
			RequestID: "req-1",
		}},
	})
	require.NoError(t, err)

	stream, apiErr, status := srv.StreamMessage(
		context.Background(),
		gwproto.MessageRequest{From: "u1", Text: "hello"},
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, status)

	events := collectGatewayStreamEvents(t, stream)
	require.Len(t, events, 3)
	require.Equal(
		t,
		gwproto.StreamEventTypeRunError,
		events[2].Type,
	)
	require.NotNil(t, events[2].Error)
	require.Equal(t, errTypeUnauthorized, events[2].Error.Type)
	require.Equal(t, "no access", events[2].Error.Message)
}

func TestServer_StreamLocked_SendFailures(t *testing.T) {
	t.Parallel()

	run := preparedMessageRun{
		userID:    "u1",
		sessionID: "s1",
		requestID: "r1",
		userMsg:   model.NewUserMessage("hello"),
	}

	t.Run("run started", func(t *testing.T) {
		t.Parallel()

		srv, err := New(&staticRunner{})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		outcome := srv.streamLocked(
			ctx,
			run,
			nil,
			make(chan gwproto.StreamEvent),
		)
		require.Equal(t, traceStatusError, outcome.status)
		require.Equal(t, context.Canceled.Error(), outcome.errMsg)
	})

	t.Run("preparing progress", func(t *testing.T) {
		t.Parallel()

		srv, err := New(&staticRunner{})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan gwproto.StreamEvent, 1)
		cancelWhenChannelLen(t, out, 1, cancel)

		outcome := srv.streamLocked(ctx, run, nil, out)
		require.Equal(t, traceStatusError, outcome.status)
		require.Equal(t, context.Canceled.Error(), outcome.errMsg)
	})

	t.Run("tool progress", func(t *testing.T) {
		t.Parallel()

		srv, err := New(&staticRunner{
			events: []*event.Event{{
				Response: &model.Response{
					Choices: []model.Choice{{
						Message: model.Message{
							ToolCalls: []model.ToolCall{{
								Function: model.FunctionDefinitionParam{
									Name: streamToolReadDocument,
									Arguments: []byte(
										`{"page":1}`,
									),
								},
							}},
						},
					}},
				},
			}},
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan gwproto.StreamEvent, 2)
		cancelWhenChannelLen(t, out, 2, cancel)

		outcome := srv.streamLocked(ctx, run, nil, out)
		require.Equal(t, traceStatusError, outcome.status)
		require.Equal(t, context.Canceled.Error(), outcome.errMsg)
	})

	t.Run("message delta", func(t *testing.T) {
		t.Parallel()

		srv, err := New(&staticRunner{
			events: []*event.Event{{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletionChunk,
					Choices: []model.Choice{{
						Delta: model.Message{Content: "ok"},
					}},
				},
			}},
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan gwproto.StreamEvent, 2)
		cancelWhenChannelLen(t, out, 2, cancel)

		outcome := srv.streamLocked(ctx, run, nil, out)
		require.Equal(t, traceStatusError, outcome.status)
		require.Equal(t, context.Canceled.Error(), outcome.errMsg)
	})

	t.Run("message completed", func(t *testing.T) {
		t.Parallel()

		srv, err := New(&staticRunner{
			events: []*event.Event{{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletion,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("ok"),
					}},
				},
			}},
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan gwproto.StreamEvent, 3)
		cancelWhenChannelLen(t, out, 3, cancel)

		outcome := srv.streamLocked(ctx, run, nil, out)
		require.Equal(t, traceStatusError, outcome.status)
		require.Equal(t, context.Canceled.Error(), outcome.errMsg)
	})

	t.Run("run completed", func(t *testing.T) {
		t.Parallel()

		srv, err := New(&staticRunner{
			events: []*event.Event{{
				Response: &model.Response{
					Object: model.ObjectTypeChatCompletion,
					Choices: []model.Choice{{
						Message: model.NewAssistantMessage("ok"),
					}},
				},
			}},
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		out := make(chan gwproto.StreamEvent, 4)
		cancelWhenChannelLen(t, out, 4, cancel)

		outcome := srv.streamLocked(ctx, run, nil, out)
		require.Equal(t, traceStatusError, outcome.status)
		require.Equal(t, context.Canceled.Error(), outcome.errMsg)
	})
}

func TestServer_HandleMessagesStream_WriteError(t *testing.T) {
	t.Parallel()

	srv, err := New(&staticRunner{
		events: []*event.Event{{
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletionChunk,
				Choices: []model.Choice{{
					Delta: model.Message{Content: "ok"},
				}},
			},
		}},
	})
	require.NoError(t, err)

	rr := &failingFlusherRecorder{}
	req := httptest.NewRequest(
		http.MethodPost,
		srv.MessagesStreamPath(),
		bytes.NewBufferString(`{"from":"u1","text":"hi"}`),
	)

	srv.handleMessagesStream(rr, req)
	require.Equal(t, http.StatusOK, rr.code)
}

func TestLaneLocker_ReclaimsEntries(t *testing.T) {
	t.Parallel()

	l := newLaneLocker()
	l.withLock("s1", func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		_, ok := l.lanes["s1"]
		require.True(t, ok)
	})

	l.mu.Lock()
	defer l.mu.Unlock()
	require.Empty(t, l.lanes)
}

func collectGatewayStreamEvents(
	t *testing.T,
	stream <-chan gwproto.StreamEvent,
) []gwproto.StreamEvent {
	t.Helper()

	timer := time.NewTimer(testTimeout)
	defer timer.Stop()

	var events []gwproto.StreamEvent
	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-timer.C:
			t.Fatal("timeout waiting for gateway stream")
		}
	}
}

func cancelWhenChannelLen(
	t *testing.T,
	ch chan gwproto.StreamEvent,
	want int,
	cancel context.CancelFunc,
) {
	t.Helper()

	go func() {
		timer := time.NewTimer(testTimeout)
		defer timer.Stop()

		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()

		for {
			select {
			case <-timer.C:
				cancel()
				return
			case <-tick.C:
				if len(ch) >= want {
					cancel()
					return
				}
			}
		}
	}()
}

func TestNewOptions_DefaultsAndNormalization(t *testing.T) {
	t.Parallel()

	sessionFn := func(InboundMessage) (string, error) {
		return "s", nil
	}
	fetcher := &staticFetcher{}
	const maxPartBytes int64 = 123

	o := newOptions(
		WithBasePath(" "),
		WithMessagesPath(" "),
		WithStatusPath(" "),
		WithCancelPath(" "),
		WithHealthPath(" "),
		WithMaxBodyBytes(0),
		WithMaxContentPartBytes(maxPartBytes),
		WithContentPartFetcher(fetcher),
		WithAllowPrivateContentPartURLs(true),
		WithAllowedContentPartDomains(" example.com ", "", " "),
		WithSessionIDFunc(sessionFn),
		WithAllowUsers(" a ", "", "b"),
		WithRequireMentionInThreads(true),
		WithMentionPatterns(" @bot ", "", "/agent"),
	)

	require.Equal(t, defaultBasePath, o.basePath)
	require.Equal(t, defaultMessagesPath, o.messagesPath)
	require.Equal(t, defaultMessagesStreamPath, o.streamPath)
	require.Equal(t, defaultStatusPath, o.statusPath)
	require.Equal(t, defaultCancelPath, o.cancelPath)
	require.Equal(t, defaultHealthPath, o.healthPath)
	require.Equal(t, defaultMaxBodyBytes, o.maxBodyBytes)
	require.Equal(t, maxPartBytes, o.maxPartBytes)
	typedFetcher, ok := o.partFetcher.(*staticFetcher)
	require.True(t, ok)
	require.Same(t, fetcher, typedFetcher)
	require.True(t, o.allowPrivatePartURLs)
	require.Equal(t, []string{"example.com"}, o.allowedPartPatterns)
	require.NotNil(t, o.sessionIDFunc)

	require.NotNil(t, o.allowUsers)
	_, ok = o.allowUsers["a"]
	require.True(t, ok)
	_, ok = o.allowUsers["b"]
	require.True(t, ok)

	require.True(t, o.requireMention)
	require.Equal(t, []string{"@bot", "/agent"}, o.mentionPatterns)
}

func TestNewOptions_DerivesStreamPathFromMessagesPath(t *testing.T) {
	t.Parallel()

	o := newOptions(WithMessagesPath("/custom/messages"))

	require.Equal(t, "/custom/messages", o.messagesPath)
	require.Equal(
		t,
		"/custom/messages"+gwproto.MessagesStreamSuffix,
		o.streamPath,
	)
}

func TestNewOptions_ExplicitStreamAndStores(t *testing.T) {
	t.Parallel()

	store := &persona.Store{}
	transcriber := &stubAudioTranscriber{}

	o := newOptions(
		WithMessagesStreamPath(" /custom/stream "),
		WithPersonaStore(store),
		WithAudioTranscriber(transcriber),
	)

	require.Equal(t, " /custom/stream ", o.streamPath)
	require.Same(t, store, o.personaStore)
	require.Same(t, transcriber, o.audioTranscriber)
}

func TestWithMentionPatterns_EmptyResets(t *testing.T) {
	t.Parallel()

	o := newOptions(
		WithMentionPatterns("@bot"),
		WithMentionPatterns(),
	)
	require.Nil(t, o.mentionPatterns)
}
