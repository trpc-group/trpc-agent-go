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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	testTimeout   = 2 * time.Second
	testShortWait = 100 * time.Millisecond
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

	require.Equal(t, http.StatusInternalServerError, rr.Code)

	var rsp gwproto.MessageResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rsp))
	require.NotNil(t, rsp.Error)
	require.Equal(t, errTypeInternal, rsp.Error.Type)
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

func TestWithMentionPatterns_EmptyResets(t *testing.T) {
	t.Parallel()

	o := newOptions(
		WithMentionPatterns("@bot"),
		WithMentionPatterns(),
	)
	require.Nil(t, o.mentionPatterns)
}
