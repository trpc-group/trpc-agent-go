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
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsGroupChat(t *testing.T) {
	t.Parallel()

	require.False(t, IsGroupChat(chatTypePrivate))
	require.True(t, IsGroupChat(chatTypeGroup))
	require.True(t, IsGroupChat(chatTypeSuperGroup))
}

func TestAPICallError_ErrorVariants(t *testing.T) {
	t.Parallel()

	var nilErr *apiCallError
	require.Equal(t, "telegram: api error", nilErr.Error())
	require.Equal(t, "telegram: api error", (&apiCallError{}).Error())
	require.Equal(
		t,
		"telegram: api error: boom",
		(&apiCallError{description: "boom"}).Error(),
	)
	require.Contains(
		t,
		(&apiCallError{errorCode: 400, description: "boom"}).Error(),
		"api error 400",
	)
}

func TestClient_doWithRetry_NilFunc(t *testing.T) {
	t.Parallel()

	c := &Client{}
	require.Error(t, c.doWithRetry(context.Background(), nil))
}

func TestClient_doWithRetry_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := &Client{}
	require.ErrorIs(t, c.doWithRetry(ctx, func(context.Context) error {
		return nil
	}), context.Canceled)
}

func TestClient_doWithRetry_CancelledDuringSleep(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	c := &Client{
		maxRetries:     1,
		retryBaseDelay: 10 * time.Millisecond,
		retryMaxDelay:  10 * time.Millisecond,
	}

	time.AfterFunc(time.Millisecond, cancel)
	require.ErrorIs(t, c.doWithRetry(ctx, func(context.Context) error {
		return errors.New("boom")
	}), context.Canceled)
}

func TestClient_shouldRetry_EdgeCases(t *testing.T) {
	t.Parallel()

	c := &Client{maxRetries: 1}
	require.False(t, c.shouldRetry(0, nil))
	require.False(t, c.shouldRetry(0, context.Canceled))
	require.False(t, c.shouldRetry(1, errors.New("boom")))

	require.True(t, c.shouldRetry(0, &apiCallError{errorCode: 429}))
	require.False(t, c.shouldRetry(0, &apiCallError{errorCode: 400}))
	require.True(t, c.shouldRetry(0, &apiCallError{errorCode: 500}))

	require.True(t, c.shouldRetry(0, statusError{status: 429}))
	require.False(t, c.shouldRetry(0, statusError{status: 400}))
	require.True(t, c.shouldRetry(0, statusError{status: 500}))
}

func TestClient_retryDelay_Variants(t *testing.T) {
	t.Parallel()

	c := &Client{
		retryBaseDelay: 2 * time.Second,
		retryMaxDelay:  3 * time.Second,
	}

	require.Equal(
		t,
		3*time.Second,
		c.retryDelay(10, &apiCallError{retryAfter: 3 * time.Second}),
	)
	require.Equal(
		t,
		time.Duration(0),
		(&Client{}).retryDelay(1, errors.New("x")),
	)
	require.Equal(
		t,
		3*time.Second,
		c.retryDelay(1, errors.New("x")),
	)
	require.Equal(
		t,
		2*time.Second,
		c.retryDelay(0, errors.New("x")),
	)
}

func TestSleep_ContextCancelled(t *testing.T) {
	t.Parallel()

	require.True(t, sleep(context.Background(), 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, sleep(ctx, time.Second))
}

func TestClient_redactErr_EdgeCases(t *testing.T) {
	t.Parallel()

	c := &Client{token: "token"}
	require.Nil(t, c.redactErr(nil))

	orig := errors.New("boom")
	require.Equal(t, orig, c.redactErr(orig))

	noToken := &Client{}
	require.Equal(t, orig, noToken.redactErr(orig))
}

func TestClient_doOnce_NewRequestError(t *testing.T) {
	t.Parallel()

	c := &Client{
		token:      testToken,
		baseURL:    "http://127.0.0.1",
		httpClient: http.DefaultClient,
	}

	var out apiResponse[User]
	_, err := c.doOnce(
		context.Background(),
		" ",
		pathGetMe,
		nil,
		nil,
		&out,
	)
	require.Error(t, err)
}

func TestClient_GetWebhookInfo_HTTPStatusError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetWebhookInfo, r.URL.Path)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithMaxRetries(0),
	)
	require.NoError(t, err)

	_, err = c.GetWebhookInfo(context.Background())
	require.Error(t, err)
}
