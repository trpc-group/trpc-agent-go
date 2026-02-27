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

	pathEditMessageText = "editMessageText"
	pathGetMe           = "getMe"
	pathGetUpdate       = "getUpdates"
	pathGetWebhookInfo  = "getWebhookInfo"
	pathSendChatAction  = "sendChatAction"
	pathSendMsg         = "sendMessage"
)

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
}

// EditMessageTextParams contains parameters for EditMessageText.
type EditMessageTextParams struct {
	ChatID    int64
	MessageID int
	Text      string
}

// SendChatActionParams contains parameters for SendChatAction.
type SendChatActionParams struct {
	ChatID          int64
	MessageThreadID int
	Action          string
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
		ChatID:             params.ChatID,
		Text:               params.Text,
		MessageThreadID:    params.MessageThreadID,
		ReplyToMessageID:   params.ReplyToMessageID,
		DisableWebPagePrev: true,
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

// EditMessageText edits an existing message.
func (c *Client) EditMessageText(
	ctx context.Context,
	params EditMessageTextParams,
) (Message, error) {
	req := editMessageTextRequest{
		ChatID:             params.ChatID,
		MessageID:          params.MessageID,
		Text:               params.Text,
		DisableWebPagePrev: true,
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

type apiResponse[T any] struct {
	OK          bool           `json:"ok"`
	Result      T              `json:"result,omitempty"`
	Description string         `json:"description,omitempty"`
	ErrorCode   int            `json:"error_code,omitempty"`
	Parameters  *apiParameters `json:"parameters,omitempty"`
}

type sendMessageRequest struct {
	ChatID             int64  `json:"chat_id"`
	Text               string `json:"text"`
	MessageThreadID    int    `json:"message_thread_id,omitempty"`
	ReplyToMessageID   int    `json:"reply_to_message_id,omitempty"`
	DisableWebPagePrev bool   `json:"disable_web_page_preview,omitempty"`
}

type editMessageTextRequest struct {
	ChatID             int64  `json:"chat_id"`
	MessageID          int    `json:"message_id"`
	Text               string `json:"text"`
	DisableWebPagePrev bool   `json:"disable_web_page_preview,omitempty"`
}

type sendChatActionRequest struct {
	ChatID          int64  `json:"chat_id"`
	MessageThreadID int    `json:"message_thread_id,omitempty"`
	Action          string `json:"action"`
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
