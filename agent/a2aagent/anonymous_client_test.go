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

const anonymousClientTestTimeout = time.Second

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
