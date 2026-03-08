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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

const redactedToken = "<redacted>"

const (
	defaultMaxRetries     = 3
	defaultRetryBaseDelay = 200 * time.Millisecond
	defaultRetryMaxDelay  = 5 * time.Second
)

const (
	methodGet  = "GET"
	methodPost = "POST"

	pathAnswerCallback  = "answerCallbackQuery"
	pathEditMessageText = "editMessageText"
	pathGetFile         = "getFile"
	pathGetMe           = "getMe"
	pathSetMyCommands   = "setMyCommands"
	pathGetUpdate       = "getUpdates"
	pathGetWebhookInfo  = "getWebhookInfo"
	pathSendChatAction  = "sendChatAction"
	pathSendMsg         = "sendMessage"
	pathSendDocument    = "sendDocument"
	pathSendPhoto       = "sendPhoto"
	pathSendAudio       = "sendAudio"
	pathSendVoice       = "sendVoice"
	pathSendVideo       = "sendVideo"
)

const ParseModeHTML = "HTML"

const queryFileID = "file_id"

const (
	errEmptyFileID     = "telegram: empty file id"
	errEmptyFilePath   = "telegram: empty file path"
	errInvalidMaxBytes = "telegram: non-positive max bytes"
	errFileTooLarge    = "telegram: file too large"
)

const (
	parseErrContainsEntities = "parse entities"
	parseErrContainsEnd      = "find end of the entity"
	errMessageNotModified    = "message is not modified"
)

// ErrFileTooLarge is returned when a downloaded file exceeds the configured
// maximum size.
var ErrFileTooLarge = errors.New(errFileTooLarge)

const maxErrorBodyBytes int64 = 4 << 10

type redactedError struct {
	msg string
	err error
}

type statusError struct {
	status int
	body   string
}

func (e statusError) Error() string {
	return fmt.Sprintf("telegram: status %d: %s", e.status, e.body)
}

func (e redactedError) Error() string {
	return e.msg
}

func (e redactedError) Unwrap() error {
	return e.err
}

type apiParameters struct {
	RetryAfter int `json:"retry_after,omitempty"`
}

type apiCallError struct {
	statusCode  int
	errorCode   int
	description string
	retryAfter  time.Duration
}

func (e *apiCallError) Error() string {
	if e == nil {
		return "telegram: api error"
	}
	if e.description == "" {
		return "telegram: api error"
	}
	if e.errorCode == 0 {
		return fmt.Sprintf("telegram: api error: %s", e.description)
	}
	return fmt.Sprintf(
		"telegram: api error %d: %s",
		e.errorCode,
		e.description,
	)
}

// Client talks to the Telegram Bot API.
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client

	maxRetries     int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration
}

// SendMessageParams contains parameters for SendMessage.
type SendMessageParams struct {
	ChatID           int64
	MessageThreadID  int
	ReplyToMessageID int
	Text             string
	ParseMode        string
	ReplyMarkup      *InlineKeyboardMarkup
}

// SendFileParams contains parameters for Telegram media uploads.
type SendFileParams struct {
	ChatID           int64
	MessageThreadID  int
	ReplyToMessageID int
	Caption          string
	ParseMode        string
	FileName         string
	Data             []byte
}

// EditMessageTextParams contains parameters for EditMessageText.
type EditMessageTextParams struct {
	ChatID      int64
	MessageID   int
	Text        string
	ParseMode   string
	ReplyMarkup *InlineKeyboardMarkup
}

// SendChatActionParams contains parameters for SendChatAction.
type SendChatActionParams struct {
	ChatID          int64
	MessageThreadID int
	Action          string
}

// AnswerCallbackQueryParams contains parameters for AnswerCallbackQuery.
type AnswerCallbackQueryParams struct {
	CallbackQueryID string
	Text            string
	ShowAlert       bool
}

// SetMyCommandsParams contains parameters for SetMyCommands.
type SetMyCommandsParams struct {
	Commands []BotCommand
}

// Option configures the Telegram client.
type Option func(*Client)

// WithBaseURL overrides the default Telegram API base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = baseURL }
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.httpClient = client }
}

// WithMaxRetries configures how many times a request is retried on
// transient failures (429 / 5xx / transport errors).
func WithMaxRetries(maxRetries int) Option {
	return func(c *Client) { c.maxRetries = maxRetries }
}

// WithRetryBaseDelay configures the initial retry backoff duration.
func WithRetryBaseDelay(delay time.Duration) Option {
	return func(c *Client) { c.retryBaseDelay = delay }
}

// WithRetryMaxDelay configures the maximum retry backoff duration.
func WithRetryMaxDelay(delay time.Duration) Option {
	return func(c *Client) { c.retryMaxDelay = delay }
}

// New creates a Telegram Bot API client.
func New(token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram: empty token")
	}
	c := &Client{
		token:          token,
		baseURL:        defaultBaseURL,
		httpClient:     http.DefaultClient,
		maxRetries:     defaultMaxRetries,
		retryBaseDelay: defaultRetryBaseDelay,
		retryMaxDelay:  defaultRetryMaxDelay,
	}
	for _, opt := range opts {
		opt(c)
	}
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, errors.New("telegram: empty base url")
	}
	if c.httpClient == nil {
		return nil, errors.New("telegram: nil http client")
	}
	if c.maxRetries < 0 {
		return nil, errors.New("telegram: negative max retries")
	}
	if c.retryBaseDelay < 0 {
		return nil, errors.New("telegram: negative retry base delay")
	}
	if c.retryMaxDelay < 0 {
		return nil, errors.New("telegram: negative retry max delay")
	}
	return c, nil
}

// GetMe returns the bot user.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var rsp apiResponse[User]
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodGet,
			pathGetMe,
			nil,
			nil,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return User{}, err
	}
	return rsp.Result, nil
}

// GetUpdates fetches updates via long polling.
func (c *Client) GetUpdates(
	ctx context.Context,
	offset int,
	timeout time.Duration,
) ([]Update, error) {
	values := url.Values{}
	if offset > 0 {
		values.Set("offset", strconv.Itoa(offset))
	}
	if timeout > 0 {
		values.Set(
			"timeout",
			strconv.Itoa(int(timeout.Seconds())),
		)
	}

	var rsp apiResponse[[]Update]
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodGet,
			pathGetUpdate,
			values,
			nil,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return nil, err
	}
	return rsp.Result, nil
}

// SendMessage sends a message to a chat.
func (c *Client) SendMessage(
	ctx context.Context,
	params SendMessageParams,
) (Message, error) {
	req := sendMessageRequest{
		ChatID:   params.ChatID,
		Text:     params.Text,
		ThreadID: params.MessageThreadID,
		ReplyID:  params.ReplyToMessageID,
		Mode:     params.ParseMode,
		NoPrev:   true,
		Markup:   params.ReplyMarkup,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, fmt.Errorf("telegram: marshal request: %w", err)
	}

	var rsp apiResponse[Message]
	err = c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodPost,
			pathSendMsg,
			nil,
			body,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return Message{}, err
	}
	return rsp.Result, nil
}

// SendDocument uploads a document to a chat.
func (c *Client) SendDocument(
	ctx context.Context,
	params SendFileParams,
) (Message, error) {
	return c.sendMedia(ctx, pathSendDocument, "document", params)
}

// SendPhoto uploads a photo to a chat.
func (c *Client) SendPhoto(
	ctx context.Context,
	params SendFileParams,
) (Message, error) {
	return c.sendMedia(ctx, pathSendPhoto, "photo", params)
}

// SendAudio uploads an audio file to a chat.
func (c *Client) SendAudio(
	ctx context.Context,
	params SendFileParams,
) (Message, error) {
	return c.sendMedia(ctx, pathSendAudio, "audio", params)
}

// SendVoice uploads a voice note to a chat.
func (c *Client) SendVoice(
	ctx context.Context,
	params SendFileParams,
) (Message, error) {
	return c.sendMedia(ctx, pathSendVoice, "voice", params)
}

// SendVideo uploads a video to a chat.
func (c *Client) SendVideo(
	ctx context.Context,
	params SendFileParams,
) (Message, error) {
	return c.sendMedia(ctx, pathSendVideo, "video", params)
}

// EditMessageText edits an existing message.
func (c *Client) EditMessageText(
	ctx context.Context,
	params EditMessageTextParams,
) (Message, error) {
	req := editMessageTextRequest{
		ChatID: params.ChatID,
		MsgID:  params.MessageID,
		Text:   params.Text,
		Mode:   params.ParseMode,
		NoPrev: true,
		Markup: params.ReplyMarkup,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, fmt.Errorf("telegram: marshal request: %w", err)
	}

	var rsp apiResponse[Message]
	err = c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodPost,
			pathEditMessageText,
			nil,
			body,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return Message{}, err
	}
	return rsp.Result, nil
}

// AnswerCallbackQuery answers one callback query to stop the client spinner.
func (c *Client) AnswerCallbackQuery(
	ctx context.Context,
	params AnswerCallbackQueryParams,
) error {
	req := answerCallbackQueryRequest{
		CallbackQueryID: params.CallbackQueryID,
		Text:            params.Text,
		ShowAlert:       params.ShowAlert,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram: marshal request: %w", err)
	}

	var rsp apiResponse[bool]
	return c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodPost,
			pathAnswerCallback,
			nil,
			body,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
}

// SendChatAction sends a chat action (for example "typing").
func (c *Client) SendChatAction(
	ctx context.Context,
	params SendChatActionParams,
) error {
	req := sendChatActionRequest{
		ChatID:          params.ChatID,
		MessageThreadID: params.MessageThreadID,
		Action:          params.Action,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram: marshal request: %w", err)
	}

	var rsp apiResponse[bool]
	return c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodPost,
			pathSendChatAction,
			nil,
			body,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
}

// SetMyCommands registers the bot command menu shown by Telegram clients.
func (c *Client) SetMyCommands(
	ctx context.Context,
	params SetMyCommandsParams,
) error {
	req := setMyCommandsRequest{
		Commands: params.Commands,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("telegram: marshal request: %w", err)
	}

	var rsp apiResponse[bool]
	return c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodPost,
			pathSetMyCommands,
			nil,
			body,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
}

func (c *Client) sendMedia(
	ctx context.Context,
	path string,
	field string,
	params SendFileParams,
) (Message, error) {
	body, contentType, err := buildMultipartPayload(field, params)
	if err != nil {
		return Message{}, err
	}

	var rsp apiResponse[Message]
	err = c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doMultipartOnce(
			ctx,
			path,
			contentType,
			body,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return Message{}, err
	}
	return rsp.Result, nil
}

func buildMultipartPayload(
	field string,
	params SendFileParams,
) ([]byte, string, error) {
	if strings.TrimSpace(field) == "" {
		return nil, "", errors.New("telegram: empty media field")
	}
	if params.ChatID == 0 {
		return nil, "", errors.New("telegram: empty chat id")
	}
	if strings.TrimSpace(params.FileName) == "" {
		return nil, "", errors.New("telegram: empty file name")
	}
	if len(params.Data) == 0 {
		return nil, "", errors.New("telegram: empty file data")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writeMultipartField(
		writer,
		"chat_id",
		strconv.FormatInt(params.ChatID, 10),
	); err != nil {
		return nil, "", err
	}
	if params.MessageThreadID > 0 {
		if err := writeMultipartField(
			writer,
			"message_thread_id",
			strconv.Itoa(params.MessageThreadID),
		); err != nil {
			return nil, "", err
		}
	}
	if params.ReplyToMessageID > 0 {
		if err := writeMultipartField(
			writer,
			"reply_to_message_id",
			strconv.Itoa(params.ReplyToMessageID),
		); err != nil {
			return nil, "", err
		}
	}
	caption := strings.TrimSpace(params.Caption)
	if caption != "" {
		if err := writeMultipartField(
			writer,
			"caption",
			caption,
		); err != nil {
			return nil, "", err
		}
	}
	parseMode := strings.TrimSpace(params.ParseMode)
	if parseMode != "" {
		if err := writeMultipartField(
			writer,
			"parse_mode",
			parseMode,
		); err != nil {
			return nil, "", err
		}
	}
	part, err := writer.CreateFormFile(field, params.FileName)
	if err != nil {
		return nil, "", fmt.Errorf(
			"telegram: create multipart file: %w",
			err,
		)
	}
	if _, err := part.Write(params.Data); err != nil {
		return nil, "", fmt.Errorf(
			"telegram: write multipart file: %w",
			err,
		)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf(
			"telegram: close multipart body: %w",
			err,
		)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func writeMultipartField(
	writer *multipart.Writer,
	key string,
	value string,
) error {
	if writer == nil {
		return errors.New("telegram: nil multipart writer")
	}
	if err := writer.WriteField(key, value); err != nil {
		return fmt.Errorf("telegram: write multipart field: %w", err)
	}
	return nil
}

// GetFile resolves a file ID into a downloadable file path.
func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return File{}, errors.New(errEmptyFileID)
	}

	values := url.Values{}
	values.Set(queryFileID, fileID)

	var rsp apiResponse[File]
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodGet,
			pathGetFile,
			values,
			nil,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return File{}, err
	}
	return rsp.Result, nil
}

// DownloadFile downloads the file content by its Telegram file path.
func (c *Client) DownloadFile(
	ctx context.Context,
	filePath string,
	maxBytes int64,
) ([]byte, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return nil, errors.New(errEmptyFilePath)
	}
	if maxBytes <= 0 {
		return nil, errors.New(errInvalidMaxBytes)
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("telegram: parse base url: %w", err)
	}

	filePath = strings.TrimPrefix(filePath, "/")
	u = u.JoinPath("file", "bot"+c.token, filePath)

	req, err := http.NewRequestWithContext(ctx, methodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: new request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.redactErr(fmt.Errorf("telegram: request: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, err := io.ReadAll(
			io.LimitReader(resp.Body, maxErrorBodyBytes),
		)
		if err != nil {
			return nil, fmt.Errorf("telegram: read response: %w", err)
		}
		return nil, statusError{
			status: resp.StatusCode,
			body:   strings.TrimSpace(string(raw)),
		}
	}

	if resp.ContentLength > maxBytes && resp.ContentLength > 0 {
		return nil, ErrFileTooLarge
	}
	return readLimited(resp.Body, maxBytes)
}

// DownloadFileByID resolves the file ID and downloads its content.
func (c *Client) DownloadFileByID(
	ctx context.Context,
	fileID string,
	maxBytes int64,
) (File, []byte, error) {
	f, err := c.GetFile(ctx, fileID)
	if err != nil {
		return File{}, nil, err
	}

	filePath := strings.TrimSpace(f.FilePath)
	if filePath == "" {
		return File{}, nil, errors.New(errEmptyFilePath)
	}
	data, err := c.DownloadFile(ctx, filePath, maxBytes)
	if err != nil {
		return File{}, nil, err
	}
	return f, data, nil
}

type apiResponse[T any] struct {
	OK          bool           `json:"ok"`
	Result      T              `json:"result,omitempty"`
	Description string         `json:"description,omitempty"`
	ErrorCode   int            `json:"error_code,omitempty"`
	Parameters  *apiParameters `json:"parameters,omitempty"`
}

type sendMessageRequest struct {
	ChatID   int64                 `json:"chat_id"`
	Text     string                `json:"text"`
	ThreadID int                   `json:"message_thread_id,omitempty"`
	ReplyID  int                   `json:"reply_to_message_id,omitempty"`
	Mode     string                `json:"parse_mode,omitempty"`
	NoPrev   bool                  `json:"disable_web_page_preview,omitempty"`
	Markup   *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type editMessageTextRequest struct {
	ChatID int64                 `json:"chat_id"`
	MsgID  int                   `json:"message_id"`
	Text   string                `json:"text"`
	Mode   string                `json:"parse_mode,omitempty"`
	NoPrev bool                  `json:"disable_web_page_preview,omitempty"`
	Markup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type answerCallbackQueryRequest struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type sendChatActionRequest struct {
	ChatID          int64  `json:"chat_id"`
	MessageThreadID int    `json:"message_thread_id,omitempty"`
	Action          string `json:"action"`
}

type setMyCommandsRequest struct {
	Commands []BotCommand `json:"commands"`
}

// WebhookInfo describes the currently configured Telegram webhook.
type WebhookInfo struct {
	URL                string `json:"url"`
	PendingUpdateCount int    `json:"pending_update_count,omitempty"`
	LastErrorMessage   string `json:"last_error_message,omitempty"`
	LastErrorDate      int64  `json:"last_error_date,omitempty"`
}

// GetWebhookInfo returns the current webhook configuration.
func (c *Client) GetWebhookInfo(
	ctx context.Context,
) (WebhookInfo, error) {
	var rsp apiResponse[WebhookInfo]
	err := c.doWithRetry(ctx, func(ctx context.Context) error {
		status, err := c.doOnce(
			ctx,
			methodGet,
			pathGetWebhookInfo,
			nil,
			nil,
			&rsp,
		)
		if err != nil {
			return err
		}
		return validateResponse(status, rsp)
	})
	if err != nil {
		return WebhookInfo{}, err
	}
	return rsp.Result, nil
}

func (c *Client) doOnce(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	body []byte,
	out any,
) (int, error) {
	if out == nil {
		return 0, errors.New("telegram: nil response target")
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return 0, fmt.Errorf("telegram: parse base url: %w", err)
	}
	u = u.JoinPath("bot"+c.token, path)
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return 0, fmt.Errorf("telegram: new request: %w", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, c.redactErr(fmt.Errorf("telegram: request: %w", err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf(
			"telegram: read response: %w", err,
		)
	}

	if err := json.Unmarshal(raw, out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp.StatusCode, statusError{
				status: resp.StatusCode,
				body:   strings.TrimSpace(string(raw)),
			}
		}
		return resp.StatusCode, fmt.Errorf("telegram: decode json: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, statusError{
			status: resp.StatusCode,
			body:   strings.TrimSpace(string(raw)),
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) doMultipartOnce(
	ctx context.Context,
	path string,
	contentType string,
	body []byte,
	out any,
) (int, error) {
	if out == nil {
		return 0, errors.New("telegram: nil response target")
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return 0, fmt.Errorf("telegram: parse base url: %w", err)
	}
	u = u.JoinPath("bot"+c.token, path)

	req, err := http.NewRequestWithContext(
		ctx,
		methodPost,
		u.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, fmt.Errorf("telegram: new request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, c.redactErr(fmt.Errorf("telegram: request: %w", err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf(
			"telegram: read response: %w",
			err,
		)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		if resp.StatusCode < http.StatusOK ||
			resp.StatusCode >= http.StatusMultipleChoices {
			return resp.StatusCode, statusError{
				status: resp.StatusCode,
				body:   strings.TrimSpace(string(raw)),
			}
		}
		return resp.StatusCode, fmt.Errorf(
			"telegram: decode json: %w",
			err,
		)
	}
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, statusError{
			status: resp.StatusCode,
			body:   strings.TrimSpace(string(raw)),
		}
	}
	return resp.StatusCode, nil
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New(errInvalidMaxBytes)
	}

	limit := maxBytes
	if maxBytes < math.MaxInt64 {
		limit = maxBytes + 1
	}

	lr := &io.LimitedReader{R: r, N: limit}
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

func (c *Client) redactErr(err error) error {
	if err == nil {
		return nil
	}
	token := strings.TrimSpace(c.token)
	if token == "" {
		return err
	}

	orig := err.Error()
	msg := strings.ReplaceAll(orig, token, redactedToken)
	if msg == orig {
		return err
	}
	return redactedError{msg: msg, err: err}
}

func validateResponse[T any](
	statusCode int,
	rsp apiResponse[T],
) error {
	if rsp.OK {
		return nil
	}

	retryAfter := time.Duration(0)
	if rsp.Parameters != nil && rsp.Parameters.RetryAfter > 0 {
		retryAfter = time.Duration(rsp.Parameters.RetryAfter) * time.Second
	}

	return &apiCallError{
		statusCode:  statusCode,
		errorCode:   rsp.ErrorCode,
		description: rsp.Description,
		retryAfter:  retryAfter,
	}
}

func (c *Client) doWithRetry(
	ctx context.Context,
	fn func(ctx context.Context) error,
) error {
	if fn == nil {
		return errors.New("telegram: nil request func")
	}

	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !c.shouldRetry(attempt, err) {
			return err
		}

		delay := c.retryDelay(attempt, err)
		attempt++
		if !sleep(ctx, delay) {
			return ctx.Err()
		}
	}
}

func (c *Client) shouldRetry(attempt int, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if attempt >= c.maxRetries {
		return false
	}

	var apiErr *apiCallError
	if errors.As(err, &apiErr) {
		if apiErr.errorCode == http.StatusTooManyRequests {
			return true
		}
		return apiErr.errorCode >= http.StatusInternalServerError
	}

	var statusErr statusError
	if errors.As(err, &statusErr) {
		if statusErr.status == http.StatusTooManyRequests {
			return true
		}
		return statusErr.status >= http.StatusInternalServerError
	}

	return true
}

func (c *Client) retryDelay(attempt int, err error) time.Duration {
	var apiErr *apiCallError
	if errors.As(err, &apiErr) && apiErr.retryAfter > 0 {
		return apiErr.retryAfter
	}

	base := c.retryBaseDelay
	if base <= 0 {
		return 0
	}

	delay := base
	for i := 0; i < attempt; i++ {
		if delay > c.retryMaxDelay/2 {
			delay = c.retryMaxDelay
			break
		}
		delay *= 2
	}
	if delay > c.retryMaxDelay {
		return c.retryMaxDelay
	}
	return delay
}

// IsEntityParseError reports whether Telegram rejected formatted text due to
// invalid entity markup.
func IsEntityParseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, parseErrContainsEntities) ||
		strings.Contains(msg, parseErrContainsEnd)
}

// IsMessageNotModifiedError reports whether Telegram rejected an edit
// because the message content already matched the requested update.
func IsMessageNotModifiedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		errMessageNotModified,
	)
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
