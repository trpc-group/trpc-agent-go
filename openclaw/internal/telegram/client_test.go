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
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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
				{
					UpdateID: 10,
					Message: &Message{
						MessageID: 100,
						Text:      "check this video",
						ReplyToMessage: &Message{
							MessageID: 99,
							Video: &Video{
								FileID: "vid1",
							},
						},
					},
				},
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
	require.NotNil(t, updates[0].Message)
	require.NotNil(t, updates[0].Message.ReplyToMessage)
	require.NotNil(t, updates[0].Message.ReplyToMessage.Video)
	require.Equal(
		t,
		"vid1",
		updates[0].Message.ReplyToMessage.Video.FileID,
	)
	require.Equal(t, "7", seenOffset)
	require.Equal(t, "30", seenTimeout)
}

func TestClient_GetUpdates_CallbackQuery(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetUpdate, r.URL.Path)

		_ = json.NewEncoder(w).Encode(apiResponse[[]Update]{
			OK: true,
			Result: []Update{{
				UpdateID: 12,
				CallbackQuery: &CallbackQuery{
					ID:   "cb-1",
					From: &User{ID: 2},
					Message: &Message{
						MessageID: 7,
						Chat: &Chat{
							ID:   42,
							Type: chatTypePrivate,
						},
					},
					Data: "persona:set:coach",
				},
			}},
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	updates, err := c.GetUpdates(context.Background(), 0, 0)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.NotNil(t, updates[0].CallbackQuery)
	require.Equal(t, "cb-1", updates[0].CallbackQuery.ID)
	require.Equal(
		t,
		"persona:set:coach",
		updates[0].CallbackQuery.Data,
	)
}

func TestClient_SendMessage(t *testing.T) {
	t.Parallel()

	const (
		testChatID   = int64(42)
		testThreadID = 7
		testReplyTo  = 100
		testReplyMsg = "hello"
		testMode     = ParseModeHTML
		testButton   = "Coach"
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
		require.Equal(t, testThreadID, payload.ThreadID)
		require.Equal(t, testReplyTo, payload.ReplyID)
		require.Equal(t, testReplyMsg, payload.Text)
		require.Equal(t, testMode, payload.Mode)
		require.True(t, payload.NoPrev)
		require.NotNil(t, payload.Markup)
		require.Equal(
			t,
			testButton,
			payload.Markup.InlineKeyboard[0][0].Text,
		)

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
			ParseMode:        testMode,
			ReplyMarkup: &InlineKeyboardMarkup{
				InlineKeyboard: [][]InlineKeyboardButton{{
					{
						Text:         testButton,
						CallbackData: "persona:set:coach",
					},
				}},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, 101, msg.MessageID)
	require.Equal(t, testReplyMsg, msg.Text)
}

func TestClient_SendDocument(t *testing.T) {
	t.Parallel()

	testMultipartSend(
		t,
		pathSendDocument,
		"document",
		func(c *Client, ctx context.Context) (Message, error) {
			return c.SendDocument(ctx, SendFileParams{
				ChatID:          42,
				MessageThreadID: 7,
				FileName:        "report.pdf",
				Data:            []byte("%PDF-1.4"),
			})
		},
	)
}

func TestClient_SendPhoto(t *testing.T) {
	t.Parallel()

	testMultipartSend(
		t,
		pathSendPhoto,
		"photo",
		func(c *Client, ctx context.Context) (Message, error) {
			return c.SendPhoto(ctx, SendFileParams{
				ChatID:   42,
				FileName: "frame.png",
				Data: []byte{
					0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
				},
			})
		},
	)
}

func TestClient_EditMessageText(t *testing.T) {
	t.Parallel()

	const (
		testChatID  = int64(42)
		testMsgID   = 100
		testNewText = "updated"
		testMode    = ParseModeHTML
		testButton  = "Creative"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathEditMessageText,
			r.URL.Path,
		)

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload editMessageTextRequest
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, testChatID, payload.ChatID)
		require.Equal(t, testMsgID, payload.MsgID)
		require.Equal(t, testNewText, payload.Text)
		require.Equal(t, testMode, payload.Mode)
		require.True(t, payload.NoPrev)
		require.NotNil(t, payload.Markup)
		require.Equal(
			t,
			testButton,
			payload.Markup.InlineKeyboard[0][0].Text,
		)

		_ = json.NewEncoder(w).Encode(apiResponse[Message]{
			OK: true,
			Result: Message{
				MessageID: testMsgID,
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

	msg, err := c.EditMessageText(
		context.Background(),
		EditMessageTextParams{
			ChatID:    testChatID,
			MessageID: testMsgID,
			Text:      testNewText,
			ParseMode: testMode,
			ReplyMarkup: &InlineKeyboardMarkup{
				InlineKeyboard: [][]InlineKeyboardButton{{
					{
						Text:         testButton,
						CallbackData: "persona:set:creative",
					},
				}},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, testMsgID, msg.MessageID)
	require.Equal(t, testNewText, msg.Text)
}

func TestClient_AnswerCallbackQuery(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathAnswerCallback,
			r.URL.Path,
		)

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload answerCallbackQueryRequest
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, "cb-1", payload.CallbackQueryID)
		require.Equal(t, "updated", payload.Text)
		require.True(t, payload.ShowAlert)

		_ = json.NewEncoder(w).Encode(apiResponse[bool]{
			OK:     true,
			Result: true,
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	err = c.AnswerCallbackQuery(
		context.Background(),
		AnswerCallbackQueryParams{
			CallbackQueryID: "cb-1",
			Text:            "updated",
			ShowAlert:       true,
		},
	)
	require.NoError(t, err)
}

func testMultipartSend(
	t *testing.T,
	apiPath string,
	field string,
	send func(*Client, context.Context) (Message, error),
) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+apiPath, r.URL.Path)

		mediaType, params, err := mime.ParseMediaType(
			r.Header.Get("Content-Type"),
		)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(mediaType, "multipart/"))

		reader := multipartReader(t, r, params["boundary"])
		values, files := readMultipartParts(t, reader)
		require.Equal(t, "42", values["chat_id"])
		require.Equal(t, field, files[0].field)
		require.NotEmpty(t, files[0].name)
		require.NotEmpty(t, files[0].data)

		_ = json.NewEncoder(w).Encode(apiResponse[Message]{
			OK: true,
			Result: Message{
				MessageID: 101,
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

	msg, err := send(c, context.Background())
	require.NoError(t, err)
	require.Equal(t, 101, msg.MessageID)
}

type multipartFile struct {
	field string
	name  string
	data  []byte
}

func multipartReader(
	t *testing.T,
	r *http.Request,
	boundary string,
) *multipart.Reader {
	t.Helper()
	require.NotEmpty(t, boundary)
	return multipart.NewReader(r.Body, boundary)
}

func readMultipartParts(
	t *testing.T,
	reader *multipart.Reader,
) (map[string]string, []multipartFile) {
	t.Helper()

	values := make(map[string]string)
	files := make([]multipartFile, 0, 1)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)

		data, err := io.ReadAll(part)
		require.NoError(t, err)

		name := part.FormName()
		if part.FileName() == "" {
			values[name] = string(data)
			continue
		}
		files = append(files, multipartFile{
			field: name,
			name:  part.FileName(),
			data:  data,
		})
	}
	return values, files
}

func TestIsEntityParseError(t *testing.T) {
	t.Parallel()

	require.True(t, IsEntityParseError(
		errors.New("telegram: api error 400: can't parse entities"),
	))
	require.True(t, IsEntityParseError(
		errors.New("telegram: api error 400: find end of the entity"),
	))
	require.False(t, IsEntityParseError(
		errors.New("telegram: api error 400: chat not found"),
	))
	require.False(t, IsEntityParseError(nil))
}

func TestClient_SendChatAction(t *testing.T) {
	t.Parallel()

	const (
		testChatID = int64(42)
		testThread = 7
		testAction = "typing"
		resultTrue = true
	)

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathSendChatAction,
			r.URL.Path,
		)

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload sendChatActionRequest
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, testChatID, payload.ChatID)
		require.Equal(t, testThread, payload.MessageThreadID)
		require.Equal(t, testAction, payload.Action)

		_ = json.NewEncoder(w).Encode(apiResponse[bool]{
			OK:     true,
			Result: resultTrue,
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	require.NoError(t, c.SendChatAction(
		context.Background(),
		SendChatActionParams{
			ChatID:          testChatID,
			MessageThreadID: testThread,
			Action:          testAction,
		},
	))
}

func TestClient_GetFile(t *testing.T) {
	t.Parallel()

	const (
		testFileID   = "file_1"
		testFilePath = "photos/file_1.jpg"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetFile, r.URL.Path)
		require.Equal(t, testFileID, r.URL.Query().Get(queryFileID))

		_ = json.NewEncoder(w).Encode(apiResponse[File]{
			OK: true,
			Result: File{
				FileID:   testFileID,
				FilePath: testFilePath,
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

	f, err := c.GetFile(context.Background(), testFileID)
	require.NoError(t, err)
	require.Equal(t, testFileID, f.FileID)
	require.Equal(t, testFilePath, f.FilePath)
}

func TestClient_DownloadFileByID(t *testing.T) {
	t.Parallel()

	const (
		testFileID   = "file_1"
		testFilePath = "photos/file_1.jpg"
	)

	expected := []byte("abc")

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch r.URL.Path {
		case "/bot" + testToken + "/" + pathGetFile:
			_ = json.NewEncoder(w).Encode(apiResponse[File]{
				OK: true,
				Result: File{
					FileID:   testFileID,
					FilePath: testFilePath,
				},
			})
			return
		case "/file/bot" + testToken + "/" + testFilePath:
			_, _ = w.Write(expected)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	f, data, err := c.DownloadFileByID(
		context.Background(),
		testFileID,
		int64(len(expected)),
	)
	require.NoError(t, err)
	require.Equal(t, testFilePath, f.FilePath)
	require.Equal(t, expected, data)
}

func TestClient_DownloadFileByID_TooLarge(t *testing.T) {
	t.Parallel()

	const (
		testFileID   = "file_1"
		testFilePath = "photos/file_1.jpg"
	)

	body := []byte("abcd")

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch r.URL.Path {
		case "/bot" + testToken + "/" + pathGetFile:
			_ = json.NewEncoder(w).Encode(apiResponse[File]{
				OK: true,
				Result: File{
					FileID:   testFileID,
					FilePath: testFilePath,
				},
			})
			return
		case "/file/bot" + testToken + "/" + testFilePath:
			_, _ = w.Write(body)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, _, err = c.DownloadFileByID(context.Background(), testFileID, 3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too large")
}

func TestClient_GetWebhookInfo(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathGetWebhookInfo,
			r.URL.Path,
		)

		_ = json.NewEncoder(w).Encode(apiResponse[WebhookInfo]{
			OK: true,
			Result: WebhookInfo{
				URL: "https://example.com/webhook",
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

	info, err := c.GetWebhookInfo(context.Background())
	require.NoError(t, err)
	require.Equal(t, "https://example.com/webhook", info.URL)
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

	_, err = New(testToken, WithMaxRetries(-1))
	require.Error(t, err)

	_, err = New(testToken, WithRetryBaseDelay(-time.Second))
	require.Error(t, err)

	_, err = New(testToken, WithRetryMaxDelay(-time.Second))
	require.Error(t, err)
}

func TestReadLimited_TooLarge(t *testing.T) {
	t.Parallel()

	_, err := readLimited(strings.NewReader("abcd"), 3)
	require.ErrorIs(t, err, ErrFileTooLarge)
}

func TestClient_RedactErr(t *testing.T) {
	t.Parallel()

	c, err := New(testToken)
	require.NoError(t, err)

	orig := errors.New("telegram: request: bot TOKEN failed")
	red := c.redactErr(orig)
	require.Contains(t, red.Error(), redactedToken)
	require.NotContains(t, red.Error(), testToken)
	require.ErrorIs(t, red, orig)
}

func TestClient_ShouldRetryAndRetryDelay(t *testing.T) {
	t.Parallel()

	c, err := New(
		testToken,
		WithMaxRetries(1),
		WithRetryBaseDelay(100*time.Millisecond),
		WithRetryMaxDelay(250*time.Millisecond),
	)
	require.NoError(t, err)

	require.False(t, c.shouldRetry(0, context.Canceled))
	require.False(t, c.shouldRetry(0, context.DeadlineExceeded))
	require.False(t, c.shouldRetry(1, errors.New("boom")))

	require.False(t, c.shouldRetry(0, &apiCallError{errorCode: 400}))
	require.True(t, c.shouldRetry(0, &apiCallError{
		errorCode: http.StatusTooManyRequests,
	}))
	require.True(t, c.shouldRetry(0, &apiCallError{
		errorCode: http.StatusInternalServerError,
	}))

	require.False(t, c.shouldRetry(0, statusError{status: 400}))
	require.True(t, c.shouldRetry(0, statusError{
		status: http.StatusTooManyRequests,
	}))
	require.True(t, c.shouldRetry(0, statusError{
		status: http.StatusInternalServerError,
	}))

	require.True(t, c.shouldRetry(0, errors.New("transport error")))

	require.Equal(
		t,
		7*time.Second,
		c.retryDelay(0, &apiCallError{retryAfter: 7 * time.Second}),
	)
	require.Equal(
		t,
		100*time.Millisecond,
		c.retryDelay(0, errors.New("boom")),
	)
	require.Equal(
		t,
		200*time.Millisecond,
		c.retryDelay(1, errors.New("boom")),
	)
	require.Equal(
		t,
		250*time.Millisecond,
		c.retryDelay(2, errors.New("boom")),
	)
}

func TestClient_GetFile_EmptyFileID(t *testing.T) {
	t.Parallel()

	c, err := New(testToken)
	require.NoError(t, err)

	_, err = c.GetFile(context.Background(), " ")
	require.Error(t, err)
	require.Contains(t, err.Error(), errEmptyFileID)
}

func TestClient_DownloadFile_ValidationErrors(t *testing.T) {
	t.Parallel()

	c, err := New(testToken)
	require.NoError(t, err)

	_, err = c.DownloadFile(context.Background(), " ", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), errEmptyFilePath)

	_, err = c.DownloadFile(context.Background(), "x", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), errInvalidMaxBytes)
}

func TestClient_DownloadFile_ParseBaseURLError(t *testing.T) {
	t.Parallel()

	c := &Client{
		baseURL:    "http://[::1",
		token:      testToken,
		httpClient: http.DefaultClient,
	}

	_, err := c.DownloadFile(context.Background(), "x", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse base url")
}

func TestClient_DownloadFile_ContentLengthTooLarge(t *testing.T) {
	t.Parallel()

	const filePath = "photos/file_1.jpg"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(
			t,
			"/file/bot"+testToken+"/"+filePath,
			r.URL.Path,
		)
		w.Header().Set("Content-Length", "4")
		_, _ = io.WriteString(w, "abcd")
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.DownloadFile(context.Background(), filePath, 3)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrFileTooLarge))
}

func TestClient_DownloadFile_StatusError(t *testing.T) {
	t.Parallel()

	const filePath = "photos/file_1.jpg"
	const errBody = "nope"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, errBody)
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.DownloadFile(context.Background(), filePath, 10)
	require.Error(t, err)

	var se statusError
	require.True(t, errors.As(err, &se))
	require.Equal(t, http.StatusTeapot, se.status)
	require.Equal(t, errBody, se.body)
}

func TestClient_DownloadFile_StatusError_LargeBody(t *testing.T) {
	t.Parallel()

	const filePath = "photos/file_1.jpg"

	errBody := strings.Repeat(
		"x",
		int(maxErrorBodyBytes)+10,
	)

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.Header().Set(
			"Content-Length",
			strconv.Itoa(len(errBody)),
		)
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, errBody)
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	_, err = c.DownloadFile(context.Background(), filePath, 1)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrFileTooLarge))

	var se statusError
	require.True(t, errors.As(err, &se))
	require.Equal(t, http.StatusTeapot, se.status)
	require.Len(t, se.body, int(maxErrorBodyBytes))
}

func TestClient_DownloadFileByID_EmptyFilePath(t *testing.T) {
	t.Parallel()

	const fileID = "file_1"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, "/bot"+testToken+"/"+pathGetFile, r.URL.Path)
		require.Equal(t, fileID, r.URL.Query().Get(queryFileID))

		_ = json.NewEncoder(w).Encode(apiResponse[File]{
			OK: true,
			Result: File{
				FileID:   fileID,
				FilePath: " ",
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

	_, _, err = c.DownloadFileByID(context.Background(), fileID, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), errEmptyFilePath)
}

func TestReadLimited_Errors(t *testing.T) {
	t.Parallel()

	_, err := readLimited(strings.NewReader("x"), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), errInvalidMaxBytes)

	const maxInt64 = int64(^uint64(0) >> 1)
	data, err := readLimited(strings.NewReader(""), maxInt64)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestValidateResponse_RetryAfter(t *testing.T) {
	t.Parallel()

	err := validateResponse(http.StatusTooManyRequests, apiResponse[bool]{
		OK:          false,
		ErrorCode:   429,
		Description: "too many requests",
		Parameters:  &apiParameters{RetryAfter: 2},
	})
	require.Error(t, err)

	var apiErr *apiCallError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 2*time.Second, apiErr.retryAfter)
}

func TestClient_doOnce_NilResponseTarget(t *testing.T) {
	t.Parallel()

	c, err := New(testToken)
	require.NoError(t, err)

	_, err = c.doOnce(
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

	err := validateResponse(http.StatusOK, apiResponse[User]{OK: false})
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

func TestClient_GetMe_RedactsTokenInHTTPClientError(t *testing.T) {
	t.Parallel()

	expected := errors.New("transport down")
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(
			r *http.Request,
		) (*http.Response, error) {
			return nil, &url.Error{
				Op:  r.Method,
				URL: r.URL.String(),
				Err: expected,
			}
		}),
	}
	c, err := New(testToken, WithHTTPClient(httpClient))
	require.NoError(t, err)

	_, err = c.GetMe(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, expected)
	require.NotContains(t, err.Error(), testToken)
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

func TestClient_SetMyCommands(t *testing.T) {
	t.Parallel()

	commands := []BotCommand{
		{Command: "help", Description: "Show help"},
		{Command: "reset", Description: "Start a new DM session"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathSetMyCommands,
			r.URL.Path,
		)

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload setMyCommandsRequest
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, commands, payload.Commands)

		_ = json.NewEncoder(w).Encode(apiResponse[bool]{
			OK:     true,
			Result: true,
		})
	}))
	t.Cleanup(srv.Close)

	c, err := New(
		testToken,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	require.NoError(t, err)

	err = c.SetMyCommands(
		context.Background(),
		SetMyCommandsParams{Commands: commands},
	)
	require.NoError(t, err)
}

func TestClient_GetMe_RetriesOnAPI429(t *testing.T) {
	t.Parallel()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		calls++
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetMe, r.URL.Path)

		if calls == 1 {
			_ = json.NewEncoder(w).Encode(apiResponse[User]{
				OK:          false,
				ErrorCode:   http.StatusTooManyRequests,
				Description: "too many requests",
				Parameters:  &apiParameters{RetryAfter: 0},
			})
			return
		}
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
		WithMaxRetries(1),
		WithRetryBaseDelay(0),
		WithRetryMaxDelay(0),
	)
	require.NoError(t, err)

	me, err := c.GetMe(context.Background())
	require.NoError(t, err)
	require.Equal(t, "mybot", me.Username)
	require.Equal(t, 2, calls)
}

func TestClient_GetMe_RetriesOnAPI5xx(t *testing.T) {
	t.Parallel()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		calls++
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetMe, r.URL.Path)

		if calls == 1 {
			_ = json.NewEncoder(w).Encode(apiResponse[User]{
				OK:          false,
				ErrorCode:   http.StatusInternalServerError,
				Description: "server error",
			})
			return
		}
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
		WithMaxRetries(1),
		WithRetryBaseDelay(0),
		WithRetryMaxDelay(0),
	)
	require.NoError(t, err)

	me, err := c.GetMe(context.Background())
	require.NoError(t, err)
	require.Equal(t, "mybot", me.Username)
	require.Equal(t, 2, calls)
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

func TestClient_SendMessage_HTTPStatusError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathSendMsg, r.URL.Path)

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

	_, err = c.SendMessage(context.Background(), SendMessageParams{
		ChatID: 1,
		Text:   "hi",
	})
	require.Error(t, err)
}

func TestClient_GetUpdates_HTTPStatusError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodGet, r.Method)
		require.Equal(t, "/bot"+testToken+"/"+pathGetUpdate, r.URL.Path)
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

	_, err = c.GetUpdates(context.Background(), 0, 0)
	require.Error(t, err)
}

func TestClient_EditMessageText_HTTPStatusError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathEditMessageText,
			r.URL.Path,
		)
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

	_, err = c.EditMessageText(context.Background(), EditMessageTextParams{
		ChatID:    1,
		MessageID: 2,
		Text:      "hi",
	})
	require.Error(t, err)
}

func TestClient_SendChatAction_HTTPStatusError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		require.Equal(t, methodPost, r.Method)
		require.Equal(
			t,
			"/bot"+testToken+"/"+pathSendChatAction,
			r.URL.Path,
		)
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

	err = c.SendChatAction(context.Background(), SendChatActionParams{
		ChatID: 1,
		Action: "typing",
	})
	require.Error(t, err)
}
