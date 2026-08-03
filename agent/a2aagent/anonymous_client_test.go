//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

const anonymousClientTestTimeout = 10 * time.Second

func TestAnonymousA2AClientSerializesFirstRequests(t *testing.T) {
	const contextID = "direct-client-context"

	var (
		mu              sync.Mutex
		nextCookieID    int
		receivedCookies []string
	)
	firstRequestStarted := make(chan struct{})
	secondRequestObserved := make(chan struct{})
	releaseFirstRequest := make(chan struct{})
	var releaseFirstOnce sync.Once
	releaseFirst := func() {
		releaseFirstOnce.Do(func() { close(releaseFirstRequest) })
	}
	var (
		firstStartedOnce   sync.Once
		secondObservedOnce sync.Once
	)
	handlerErrs := make(chan error, 1)
	reportHandlerError := func(err error) {
		select {
		case handlerErrs <- err:
		default:
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpcRequest struct {
			ID any `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpcRequest); err != nil {
			reportHandlerError(fmt.Errorf("decode RPC request: %w", err))
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		cookieValue := ""
		if cookie, err := r.Cookie(anonymousUserIDCookieName); err == nil {
			cookieValue = cookie.Value
		}
		mu.Lock()
		receivedCookies = append(receivedCookies, cookieValue)
		if cookieValue == "" {
			nextCookieID++
		}
		requestNumber := len(receivedCookies)
		shouldBlock := requestNumber == 1
		mu.Unlock()

		if requestNumber == 1 {
			firstStartedOnce.Do(func() { close(firstRequestStarted) })
		}
		if requestNumber == 2 && cookieValue == "" {
			secondObservedOnce.Do(func() { close(secondRequestObserved) })
		}
		if shouldBlock {
			select {
			case <-releaseFirstRequest:
			case <-r.Context().Done():
				return
			}
		}

		responseCookieValue := cookieValue
		if responseCookieValue == "" {
			mu.Lock()
			responseCookieValue = anonymousTestCookieValue(nextCookieID)
			mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: responseCookieValue,
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			ID:      rpcRequest.ID,
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: fmt.Sprintf("response-%d", requestNumber),
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		}); err != nil {
			reportHandlerError(fmt.Errorf("encode RPC response: %w", err))
		}
	}))
	defer srv.Close()
	defer releaseFirst()

	middleware := newAnonymousA2AClientInitMiddleware()
	secondWaitingForInit := make(chan struct{})
	var secondWaitingOnce sync.Once
	middleware.waitHook = func() {
		secondWaitingOnce.Do(func() { close(secondWaitingForInit) })
	}
	httpClient := &http.Client{}
	directClient, err := client.NewA2AClient(
		srv.URL,
		client.WithHTTPClient(httpClient),
		client.WithMiddleware(middleware),
	)
	require.NoError(t, err)

	send := func(invocationID string) error {
		ctx, cancel := context.WithTimeout(context.Background(), anonymousClientTestTimeout)
		defer cancel()
		message := protocol.NewMessage(
			protocol.MessageRoleUser,
			[]protocol.Part{protocol.NewTextPart(invocationID)},
		)
		ctxID := contextID
		message.ContextID = &ctxID
		_, err := directClient.SendMessage(
			ctx,
			protocol.SendMessageParams{Message: message},
		)
		return err
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- send("first") }()
	select {
	case <-firstRequestStarted:
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("first anonymous request did not start")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- send("second") }()
	select {
	case <-secondWaitingForInit:
	case <-secondRequestObserved:
		t.Fatal("second anonymous request bypassed initialization gate")
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("second anonymous request did not wait for initialization gate")
	}
	select {
	case <-secondRequestObserved:
		t.Fatal("second anonymous request reached the server before the first completed")
	default:
	}

	releaseFirst()
	select {
	case err := <-firstDone:
		require.NoError(t, err)
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("first anonymous request did not finish")
	}
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("second anonymous request did not finish")
	}

	require.NoError(t, send("third"))
	select {
	case handlerErr := <-handlerErrs:
		require.NoError(t, handlerErr)
	default:
	}

	mu.Lock()
	received := append([]string(nil), receivedCookies...)
	mu.Unlock()
	require.Equal(t, []string{
		"",
		anonymousTestCookieValue(1),
		anonymousTestCookieValue(1),
	}, received)
	parsedURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	require.Nil(t, httpClient.Jar)
	require.NotNil(t, middleware.jar)
	cookies := middleware.jar.Cookies(parsedURL)
	require.Len(t, cookies, 1)
	require.Equal(t, anonymousTestCookieValue(1), cookies[0].Value)
}

func TestAnonymousA2AClientSerializesForeignPreloadedCookie(t *testing.T) {
	const currentCookie = "A2A_ANONYMOUS_22222222222222222222222222222222"
	foreignCookie := anonymousTestCookieValue(99)
	var (
		mu              sync.Mutex
		receivedCookies []string
	)
	firstRequestStarted := make(chan struct{})
	secondRequestObserved := make(chan struct{})
	releaseFirstRequest := make(chan struct{})
	var (
		firstStartedOnce   sync.Once
		secondObservedOnce sync.Once
		releaseFirstOnce   sync.Once
	)
	releaseFirst := func() {
		releaseFirstOnce.Do(func() { close(releaseFirstRequest) })
	}
	defer releaseFirst()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieValue := ""
		if cookie, err := r.Cookie(anonymousUserIDCookieName); err == nil {
			cookieValue = cookie.Value
		}
		mu.Lock()
		receivedCookies = append(receivedCookies, cookieValue)
		requestNumber := len(receivedCookies)
		mu.Unlock()
		if requestNumber == 1 {
			firstStartedOnce.Do(func() { close(firstRequestStarted) })
			select {
			case <-releaseFirstRequest:
			case <-r.Context().Done():
				return
			}
		}
		if requestNumber == 2 {
			secondObservedOnce.Do(func() { close(secondRequestObserved) })
		}

		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: currentCookie,
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: fmt.Sprintf("response-%d", requestNumber),
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		})
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	foreignURL := *serverURL
	foreignURL.Host = serverURL.Hostname() + ":1"
	jar.SetCookies(&foreignURL, []*http.Cookie{{
		Name:  anonymousUserIDCookieName,
		Value: foreignCookie,
		Path:  "/",
	}})

	middleware := newAnonymousA2AClientInitMiddleware()
	secondWaitingForInit := make(chan struct{})
	var secondWaitingOnce sync.Once
	middleware.waitHook = func() {
		secondWaitingOnce.Do(func() { close(secondWaitingForInit) })
	}
	directClient, err := client.NewA2AClient(
		srv.URL,
		client.WithHTTPClient(&http.Client{Jar: jar}),
		client.WithMiddleware(middleware),
	)
	require.NoError(t, err)
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("hello")},
	)
	send := func() error {
		return sendDirectClientMessage(directClient, message)
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- send() }()
	select {
	case <-firstRequestStarted:
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("first anonymous request did not start")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- send() }()
	select {
	case <-secondWaitingForInit:
	case <-secondRequestObserved:
		t.Fatal("foreign preloaded cookie bypassed initialization gate")
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("second anonymous request did not wait for initialization gate")
	}
	releaseFirst()
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)

	mu.Lock()
	received := append([]string(nil), receivedCookies...)
	mu.Unlock()
	require.Equal(t, []string{foreignCookie, currentCookie}, received)
}

func TestAnonymousA2AClientInitializationHonorsContextCancellation(t *testing.T) {
	middleware := newAnonymousA2AClientInitMiddleware()
	firstRelease, err := middleware.acquire(context.Background())
	require.NoError(t, err)
	require.NotNil(t, firstRelease)
	var firstReleaseOnce sync.Once
	releaseFirst := func() {
		firstReleaseOnce.Do(firstRelease)
	}
	defer releaseFirst()

	waiting := make(chan struct{})
	var waitingOnce sync.Once
	middleware.waitHook = func() {
		waitingOnce.Do(func() { close(waiting) })
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secondDone := make(chan error, 1)
	go func() {
		secondRelease, acquireErr := middleware.acquire(ctx)
		if secondRelease != nil {
			secondRelease()
		}
		secondDone <- acquireErr
	}()
	select {
	case <-waiting:
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("second anonymous client request did not wait")
	}
	cancel()
	select {
	case err := <-secondDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("second anonymous client request did not observe cancellation")
	}

	releaseFirst()
	thirdRelease, err := middleware.acquire(context.Background())
	require.NoError(t, err)
	require.NotNil(t, thirdRelease)
	thirdRelease()
}

func TestAnonymousA2AClientRequestWaitHonorsContextCancellation(t *testing.T) {
	middleware := newAnonymousA2AClientInitMiddleware()
	firstRelease, err := middleware.acquire(context.Background())
	require.NoError(t, err)
	require.NotNil(t, firstRelease)
	defer firstRelease()

	waiting := make(chan struct{})
	var waitingOnce sync.Once
	middleware.waitHook = func() {
		waitingOnce.Do(func() { close(waiting) })
	}
	downstreamCalled := make(chan struct{})
	handler := middleware.Wrap(httpReqHandlerFunc(func(
		context.Context,
		*http.Client,
		*http.Request,
	) (*http.Response, error) {
		close(downstreamCalled)
		return nil, fmt.Errorf("downstream handler called")
	}))
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, handleErr := handler.Handle(ctx, &http.Client{}, req)
		done <- handleErr
	}()

	select {
	case <-waiting:
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("request did not wait for initialization")
	}
	cancel()
	select {
	case handleErr := <-done:
		require.ErrorIs(t, handleErr, context.Canceled)
	case <-downstreamCalled:
		t.Fatal("request reached downstream handler after cancellation")
	case <-time.After(anonymousClientTestTimeout):
		t.Fatal("request wait did not observe cancellation")
	}
}

func TestAnonymousA2AClientInitHandlerBoundaryInputs(t *testing.T) {
	t.Run("nil handler", func(t *testing.T) {
		var handler *anonymousA2AClientInitHandler
		_, err := handler.Handle(context.Background(), nil, nil)
		require.EqualError(t, err, "anonymous A2A client: initialization middleware is nil")
	})

	t.Run("nil middleware", func(t *testing.T) {
		handler := &anonymousA2AClientInitHandler{
			next: httpReqHandlerFunc(func(context.Context, *http.Client, *http.Request) (*http.Response, error) {
				return nil, nil
			}),
		}
		_, err := handler.Handle(context.Background(), nil, nil)
		require.EqualError(t, err, "anonymous A2A client: initialization middleware is nil")
	})

	t.Run("nil next handler", func(t *testing.T) {
		handler := &anonymousA2AClientInitHandler{
			middleware: newAnonymousA2AClientInitMiddleware(),
		}
		_, err := handler.Handle(context.Background(), nil, nil)
		require.EqualError(t, err, "anonymous A2A client: next HTTP request handler is nil")
	})

	t.Run("nil client or request passes through", func(t *testing.T) {
		sentinel := fmt.Errorf("downstream sentinel")
		var gotCtx context.Context
		var gotClient *http.Client
		var gotReq *http.Request
		handler := &anonymousA2AClientInitHandler{
			middleware: newAnonymousA2AClientInitMiddleware(),
			next: httpReqHandlerFunc(func(
				ctx context.Context,
				httpClient *http.Client,
				req *http.Request,
			) (*http.Response, error) {
				gotCtx, gotClient, gotReq = ctx, httpClient, req
				return nil, sentinel
			}),
		}
		_, err := handler.Handle(nil, nil, nil)
		require.ErrorIs(t, err, sentinel)
		require.NotNil(t, gotCtx)
		require.Nil(t, gotClient)
		require.Nil(t, gotReq)
	})

	t.Run("preloaded cookie still enters initialization gate", func(t *testing.T) {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodGet, "http://example.com/a2a", nil)
		require.NoError(t, err)
		jar.SetCookies(req.URL, []*http.Cookie{{
			Name:  anonymousUserIDCookieName,
			Value: anonymousTestCookieValue(10),
			Path:  "/",
		}})
		middleware := newAnonymousA2AClientInitMiddleware()
		release, err := middleware.acquire(context.Background())
		require.NoError(t, err)
		defer release()
		called := false
		handler := middleware.Wrap(httpReqHandlerFunc(func(
			_ context.Context,
			httpClient *http.Client,
			request *http.Request,
		) (*http.Response, error) {
			called = true
			return nil, nil
		}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = handler.Handle(ctx, &http.Client{Jar: jar}, req)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, called)
	})

	t.Run("clientWithCookieJar accepts nil client", func(t *testing.T) {
		client, err := newAnonymousA2AClientInitMiddleware().clientWithCookieJar(nil)
		require.NoError(t, err)
		require.Nil(t, client)
	})
}

func TestAnonymousA2AClientInitializationStateBoundaries(t *testing.T) {
	middleware := newAnonymousA2AClientInitMiddleware()
	req, err := http.NewRequest(http.MethodGet, "http://example.com/a2a", nil)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	require.NoError(t, middleware.ensureCookieJar(jar))

	require.False(t, middleware.needsInitialization(nil))
	require.False(t, middleware.needsInitialization(&http.Request{}))
	require.True(t, middleware.needsInitialization(req))

	middleware.markInitialized(req.URL, []*http.Cookie{{
		Name:  anonymousUserIDCookieName,
		Value: "invalid",
	}})
	require.True(t, middleware.needsInitialization(req))
	validCookie := &http.Cookie{
		Name:  anonymousUserIDCookieName,
		Value: anonymousTestCookieValue(11),
	}
	jar.SetCookies(req.URL, []*http.Cookie{validCookie})
	middleware.markInitialized(req.URL, []*http.Cookie{validCookie})
	require.False(t, middleware.needsInitialization(req))
}

func TestAnonymousA2AClientRejectedCookieDoesNotInitialize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: anonymousTestCookieValue(1),
			Path:  "/unrelated",
		})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/a2a", nil)
	require.NoError(t, err)
	middleware := newAnonymousA2AClientInitMiddleware()
	handler := middleware.Wrap(httpReqHandlerFunc(func(
		_ context.Context,
		httpClient *http.Client,
		req *http.Request,
	) (*http.Response, error) {
		return httpClient.Do(req)
	}))
	resp, err := handler.Handle(context.Background(), &http.Client{}, req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.True(t, middleware.needsInitialization(req))
	require.Empty(t, middleware.cookieJar().Cookies(req.URL))
}

func TestNewAnonymousA2AClientInstallsCookieJar(t *testing.T) {
	var (
		mu              sync.Mutex
		receivedCookies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieValue := ""
		if cookie, err := r.Cookie(anonymousUserIDCookieName); err == nil {
			cookieValue = cookie.Value
		}
		mu.Lock()
		receivedCookies = append(receivedCookies, cookieValue)
		mu.Unlock()
		if cookieValue == "" {
			cookieValue = anonymousTestCookieValue(1)
		}
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: cookieValue,
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			ID:      nil,
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: "response",
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		})
	}))
	defer srv.Close()

	directClient, err := NewAnonymousA2AClient(srv.URL)
	require.NoError(t, err)
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("hello")},
	)
	require.NoError(t, sendDirectClientMessage(directClient, message))
	require.NoError(t, sendDirectClientMessage(directClient, message))
	mu.Lock()
	received := append([]string(nil), receivedCookies...)
	mu.Unlock()
	require.Equal(t, []string{"", anonymousTestCookieValue(1)}, received)
}

func TestNewAnonymousA2AClientCustomHandlerKeepsCookieContinuity(t *testing.T) {
	var receivedCookies []string
	customHandler := httpReqHandlerFunc(func(
		_ context.Context,
		_ *http.Client,
		req *http.Request,
	) (*http.Response, error) {
		cookieValue := ""
		if cookie, err := req.Cookie(anonymousUserIDCookieName); err == nil {
			cookieValue = cookie.Value
		}
		receivedCookies = append(receivedCookies, cookieValue)
		if cookieValue == "" {
			cookieValue = anonymousTestCookieValue(1)
		}

		rr := httptest.NewRecorder()
		http.SetCookie(rr, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: cookieValue,
			Path:  "/",
		})
		rr.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rr).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: "response",
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		})
		resp := rr.Result()
		resp.Request = req
		return resp, nil
	})

	directClient, err := NewAnonymousA2AClient(
		"http://example.com/a2a",
		client.WithHTTPReqHandler(customHandler),
	)
	require.NoError(t, err)
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("hello")},
	)
	require.NoError(t, sendDirectClientMessage(directClient, message))
	require.NoError(t, sendDirectClientMessage(directClient, message))
	require.Equal(t, []string{"", anonymousTestCookieValue(1)}, receivedCookies)
}

func TestAnonymousA2AClientJarReadBeforeDoDoesNotDuplicateCookies(t *testing.T) {
	const routingCookieName = "route"
	wantAnonymousCookie := anonymousTestCookieValue(1)
	var receivedCookieCounts map[string]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCookieCounts = make(map[string]int)
		for _, cookie := range r.Cookies() {
			receivedCookieCounts[cookie.Name]++
		}
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: wantAnonymousCookie,
			Path:  "/",
		})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(req.URL, []*http.Cookie{
		{Name: anonymousUserIDCookieName, Value: wantAnonymousCookie},
		{Name: routingCookieName, Value: "kept"},
	})
	middleware := newAnonymousA2AClientInitMiddleware()
	handler := middleware.Wrap(httpReqHandlerFunc(func(
		_ context.Context,
		httpClient *http.Client,
		req *http.Request,
	) (*http.Response, error) {
		_ = httpClient.Jar.Cookies(req.URL)
		return httpClient.Do(req)
	}))
	resp, err := handler.Handle(context.Background(), &http.Client{Jar: jar}, req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, map[string]int{
		anonymousUserIDCookieName: 1,
		routingCookieName:         1,
	}, receivedCookieCounts)
}

func TestAnonymousA2AClientRedirectDoesNotDuplicateUnchangedCookies(t *testing.T) {
	const (
		routingCookieName     = "route"
		destinationCookieName = "destination"
	)
	wantAnonymousCookie := anonymousTestCookieValue(1)
	var (
		finalCookieCounts map[string]int
		finalCookieValues map[string][]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{
				Name:  routingCookieName,
				Value: "updated",
				Path:  "/",
			})
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		finalCookieCounts = make(map[string]int)
		finalCookieValues = make(map[string][]string)
		for _, cookie := range r.Cookies() {
			finalCookieCounts[cookie.Name]++
			finalCookieValues[cookie.Name] = append(
				finalCookieValues[cookie.Name],
				cookie.Value,
			)
		}
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: wantAnonymousCookie,
			Path:  "/",
		})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/start", nil)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(req.URL, []*http.Cookie{
		{Name: anonymousUserIDCookieName, Value: wantAnonymousCookie},
		{Name: routingCookieName, Value: "initial"},
		{Name: destinationCookieName, Value: "final-only", Path: "/final"},
	})
	middleware := newAnonymousA2AClientInitMiddleware()
	handler := middleware.Wrap(httpReqHandlerFunc(func(
		_ context.Context,
		httpClient *http.Client,
		req *http.Request,
	) (*http.Response, error) {
		return httpClient.Do(req)
	}))
	resp, err := handler.Handle(context.Background(), &http.Client{Jar: jar}, req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, map[string]int{
		anonymousUserIDCookieName: 1,
		routingCookieName:         1,
		destinationCookieName:     1,
	}, finalCookieCounts)
	require.Equal(t, []string{wantAnonymousCookie}, finalCookieValues[anonymousUserIDCookieName])
	require.Equal(t, []string{"updated"}, finalCookieValues[routingCookieName])
	require.Equal(t, []string{"final-only"}, finalCookieValues[destinationCookieName])
}

func TestAnonymousA2AClientCustomHandlerStoresDistinctResponseCookies(t *testing.T) {
	const unrelatedCookieName = "unrelated"
	wantAnonymousCookie := anonymousTestCookieValue(1)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	middleware := newAnonymousA2AClientInitMiddleware()
	handler := middleware.Wrap(httpReqHandlerFunc(func(
		_ context.Context,
		httpClient *http.Client,
		req *http.Request,
	) (*http.Response, error) {
		httpClient.Jar.SetCookies(req.URL, []*http.Cookie{{
			Name:  unrelatedCookieName,
			Value: "stored-by-handler",
			Path:  "/",
		}})
		rr := httptest.NewRecorder()
		http.SetCookie(rr, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: wantAnonymousCookie,
			Path:  "/",
		})
		rr.WriteHeader(http.StatusNoContent)
		resp := rr.Result()
		resp.Request = req
		return resp, nil
	}))
	req, err := http.NewRequest(http.MethodGet, "http://example.com/a2a", nil)
	require.NoError(t, err)
	resp, err := handler.Handle(context.Background(), &http.Client{Jar: jar}, req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	cookies := make(map[string]string)
	for _, cookie := range jar.Cookies(req.URL) {
		cookies[cookie.Name] = cookie.Value
	}
	require.Equal(t, map[string]string{
		anonymousUserIDCookieName: wantAnonymousCookie,
		unrelatedCookieName:       "stored-by-handler",
	}, cookies)
	require.False(t, middleware.needsInitialization(req))
}

func TestNewAnonymousA2AClientPreservesConfiguredCookieJar(t *testing.T) {
	const (
		routingCookieName = "route"
		serverCookieName  = "server"
	)
	wantAnonymousCookie := anonymousTestCookieValue(7)
	var (
		mu                       sync.Mutex
		receivedAnonymousCookies []string
		receivedRoutingCookies   []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anonymousCookie := ""
		if cookie, err := r.Cookie(anonymousUserIDCookieName); err == nil {
			anonymousCookie = cookie.Value
		}
		routingCookie := ""
		if cookie, err := r.Cookie(routingCookieName); err == nil {
			routingCookie = cookie.Value
		}
		mu.Lock()
		receivedAnonymousCookies = append(receivedAnonymousCookies, anonymousCookie)
		receivedRoutingCookies = append(receivedRoutingCookies, routingCookie)
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: wantAnonymousCookie,
			Path:  "/",
		})
		http.SetCookie(w, &http.Cookie{
			Name:  serverCookieName,
			Value: "updated",
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: "response",
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		})
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	serverURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	jar.SetCookies(serverURL, []*http.Cookie{
		{Name: anonymousUserIDCookieName, Value: wantAnonymousCookie},
		{Name: routingCookieName, Value: "kept"},
	})
	httpClient := &http.Client{Jar: jar}
	directClient, err := NewAnonymousA2AClient(
		srv.URL,
		client.WithHTTPClient(httpClient),
	)
	require.NoError(t, err)
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("hello")},
	)
	require.NoError(t, sendDirectClientMessage(directClient, message))

	mu.Lock()
	receivedAnonymous := append([]string(nil), receivedAnonymousCookies...)
	receivedRouting := append([]string(nil), receivedRoutingCookies...)
	mu.Unlock()
	require.Equal(t, []string{wantAnonymousCookie}, receivedAnonymous)
	require.Equal(t, []string{"kept"}, receivedRouting)
	require.Same(t, jar, httpClient.Jar)
	cookies := make(map[string]string)
	for _, cookie := range jar.Cookies(serverURL) {
		cookies[cookie.Name] = cookie.Value
	}
	require.Equal(t, wantAnonymousCookie, cookies[anonymousUserIDCookieName])
	require.Equal(t, "kept", cookies[routingCookieName])
	require.Equal(t, "updated", cookies[serverCookieName])
}

func TestNewAnonymousA2AClientProcessesConfiguredCookieJarOnce(t *testing.T) {
	var (
		mu                    sync.Mutex
		anonymousCookieCounts []int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anonymousCookieCount := 0
		for _, cookie := range r.Cookies() {
			if cookie.Name == anonymousUserIDCookieName {
				anonymousCookieCount++
			}
		}
		mu.Lock()
		anonymousCookieCounts = append(anonymousCookieCounts, anonymousCookieCount)
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: anonymousTestCookieValue(1),
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: "response",
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		})
	}))
	defer srv.Close()

	baseJar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar := &countingCookieJar{base: baseJar}
	directClient, err := NewAnonymousA2AClient(
		srv.URL,
		client.WithHTTPClient(&http.Client{Jar: jar}),
	)
	require.NoError(t, err)
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("hello")},
	)
	require.NoError(t, sendDirectClientMessage(directClient, message))
	require.NoError(t, sendDirectClientMessage(directClient, message))

	mu.Lock()
	receivedCounts := append([]int(nil), anonymousCookieCounts...)
	mu.Unlock()
	require.Equal(t, []int{0, 1}, receivedCounts)
	jar.mu.Lock()
	defer jar.mu.Unlock()
	require.Equal(t, 3, jar.cookiesCalls)
	require.Equal(t, 2, jar.setCookiesCalls)
}

func TestAnonymousA2AClientsDoNotModifySharedHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieValue := anonymousTestCookieValue(1)
		if cookie, err := r.Cookie(anonymousUserIDCookieName); err == nil {
			cookieValue = cookie.Value
		}
		http.SetCookie(w, &http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: cookieValue,
			Path:  "/",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      any              `json:"id"`
			Result  protocol.Message `json:"result"`
		}{
			JSONRPC: "2.0",
			Result: protocol.Message{
				Kind:      protocol.KindMessage,
				MessageID: "response",
				Role:      protocol.MessageRoleAgent,
				Parts:     []protocol.Part{protocol.NewTextPart("test response")},
			},
		})
	}))
	defer srv.Close()

	sharedClient := &http.Client{}
	clients := make([]*client.A2AClient, 4)
	for i := range clients {
		var err error
		clients[i], err = NewAnonymousA2AClient(
			srv.URL,
			client.WithHTTPClient(sharedClient),
		)
		require.NoError(t, err)
	}

	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("hello")},
	)
	errs := make(chan error, len(clients))
	var wg sync.WaitGroup
	for _, directClient := range clients {
		wg.Add(1)
		go func(directClient *client.A2AClient) {
			defer wg.Done()
			errs <- sendDirectClientMessage(directClient, message)
		}(directClient)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Nil(t, sharedClient.Jar)
}

func sendDirectClientMessage(directClient *client.A2AClient, message protocol.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), anonymousClientTestTimeout)
	defer cancel()
	_, err := directClient.SendMessage(
		ctx,
		protocol.SendMessageParams{Message: message},
	)
	return err
}

type countingCookieJar struct {
	base            http.CookieJar
	mu              sync.Mutex
	cookiesCalls    int
	setCookiesCalls int
}

func (j *countingCookieJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	j.cookiesCalls++
	j.mu.Unlock()
	return j.base.Cookies(u)
}

func (j *countingCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	j.setCookiesCalls++
	j.mu.Unlock()
	j.base.SetCookies(u, cookies)
}

var _ http.CookieJar = (*countingCookieJar)(nil)
