package telegram

import (
	"context"
	"encoding/json"
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
		testChatID,
		testReplyTo,
		testReplyMsg,
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
