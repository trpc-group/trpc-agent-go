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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testToken = "TOKEN"

func TestClient_GetMe(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetMe, r.URL.Path)

		_ = json.NewEncoder(w).Encode(apiResponse[User]{
			OK: true,
			Result: User{
				ID:       1,
				IsBot:    true,
				Username: "mybot",
			},
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	me, err := c.GetMe(context.Background())
	require.NoError(t, err)
	require.Equal(t, "mybot", me.Username)
}

func TestClient_GetUpdates(t *testing.T) {
	t.Parallel()

	var seenOffset string
	var seenTimeout string

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetUpdate, r.URL.Path)
		q := r.URL.Query()
		seenOffset = q.Get("offset")
		seenTimeout = q.Get("timeout")

		_ = json.NewEncoder(w).Encode(apiResponse[[]Update]{
			OK: true,
			Result: []Update{
				{UpdateID: 10},
				{UpdateID: 11},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	updates, err := c.GetUpdates(context.Background(), 7, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	require.Equal(t, "7", seenOffset)
	require.Equal(t, "30", seenTimeout)
}

func TestClient_SendMessage(t *testing.T) {
	t.Parallel()

	const (
		testChatID   = int64(42)
		testThreadID = 7
		testReplyTo  = 100
		testReplyMsg = "hello"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathSendMsg, r.URL.Path)

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload sendMessageRequest
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, testChatID, payload.ChatID)
		require.Equal(t, testThreadID, payload.MessageThreadID)
		require.Equal(t, testReplyTo, payload.ReplyToMessageID)
		require.Equal(t, testReplyMsg, payload.Text)
		require.True(t, payload.DisableWebPagePrev)

		_ = json.NewEncoder(w).Encode(apiResponse[Message]{
			OK: true,
			Result: Message{
				MessageID: 101,
				Text:      payload.Text,
			},
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	msg, err := c.SendMessage(
		context.Background(),
		SendMessageParams{
			ChatID:           testChatID,
			MessageThreadID:  testThreadID,
			ReplyToMessageID: testReplyTo,
			Text:             testReplyMsg,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 101, msg.MessageID)
	require.Equal(t, testReplyMsg, msg.Text)
}

func TestClient_ValidateResponse_Error(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_ = json.NewEncoder(w).Encode(apiResponse[User]{
			OK:          false,
			ErrorCode:   400,
			Description: "bad request",
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "bad request")
}

func TestClient_GetUpdates_TimeoutIsSeconds(t *testing.T) {
	t.Parallel()

	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		seen = r.URL.Query().Get("timeout")
		_ = json.NewEncoder(w).Encode(apiResponse[[]Update]{OK: true})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.GetUpdates(context.Background(), 0, 1500*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(1), seen)
}

func TestClient_New_ValidationErrors(t *testing.T) {
	t.Parallel()

	_, err := New("")
	require.Error(t, err)

	_, err = New(testToken, WithBaseURL(""))
	require.Error(t, err)

	_, err = New(testToken, WithHTTPClient(nil))
	require.Error(t, err)
}

func TestClient_do_NilResponseTarget(t *testing.T) {
	t.Parallel()

	c, err := New(testToken)
	require.NoError(t, err)

	err = c.do(
		context.Background(),
		methodGet,
		pathGetMe,
		nil,
		nil,
		nil,
	)
	require.Error(t, err)
}

func TestClient_GetMe_HTTPStatusError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "status")
}

func TestClient_GetMe_BadJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = w.Write([]byte("{"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode json")
}

func TestValidateResponse_NoDescription(t *testing.T) {
	t.Parallel()

	err := validateResponse(apiResponse[User]{OK: false})
	require.Error(t, err)
}

func TestClient_GetMe_HTTPClientError(t *testing.T) {
	t.Parallel()

	expected := errors.New("transport down")
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, expected
		}),
	}
	c, err := New(testToken, WithHTTPClient(httpClient))
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.ErrorIs(t, err, expected)
}

func TestClient_GetMe_ReadError(t *testing.T) {
	t.Parallel()

	expected := errors.New("read fail")
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(&errReader{err: expected}),
				Header:     make(http.Header),
			}, nil
		}),
	}
	c, err := New(testToken, WithHTTPClient(httpClient))
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.ErrorIs(t, err, expected)
}

func TestClient_GetMe_ParseBaseURLError(t *testing.T) {
	t.Parallel()

	c, err := New(testToken, WithBaseURL("http://[::1"))
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.Error(t, err)
}

func TestClient_GetUpdates_QueryEmptyByDefault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, "", r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(apiResponse[[]Update]{OK: true})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.GetUpdates(context.Background(), 0, 0)
	require.NoError(t, err)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(
	r *http.Request,
) (*http.Response, error) {
	return f(r)
}

type errReader struct {
	err error
}

func (r *errReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func (r *errReader) Close() error {
	return nil
}

func TestClient_SendMessage_MarshalError(t *testing.T) {
	t.Parallel()

	c, err := New(testToken)
	require.NoError(t, err)

	_, err = c.SendMessage(
		context.Background(),
		SendMessageParams{Text: string([]byte{0xff})},
	)
	require.Error(t, err)
}
