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

const (
	methodGet  = "GET"
	methodPost = "POST"

	pathGetMe     = "getMe"
	pathGetUpdate = "getUpdates"
	pathSendMsg   = "sendMessage"
)

// Client talks to the Telegram Bot API.
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

// SendMessageParams contains parameters for SendMessage.
type SendMessageParams struct {
	ChatID           int64
	MessageThreadID  int
	ReplyToMessageID int
	Text             string
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

// New creates a Telegram Bot API client.
func New(token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram: empty token")
	}
	c := &Client{
		token:      token,
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
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
	return c, nil
}

// GetMe returns the bot user.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var rsp apiResponse[User]
	if err := c.do(ctx, methodGet, pathGetMe, nil, nil, &rsp); err != nil {
		return User{}, err
	}
	if err := validateResponse(rsp); err != nil {
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
	if err := c.do(
		ctx,
		methodGet,
		pathGetUpdate,
		values,
		nil,
		&rsp,
	); err != nil {
		return nil, err
	}
	if err := validateResponse(rsp); err != nil {
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
	if err := c.do(ctx, methodPost, pathSendMsg, nil, body, &rsp); err != nil {
		return Message{}, err
	}
	if err := validateResponse(rsp); err != nil {
		return Message{}, err
	}
	return rsp.Result, nil
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result,omitempty"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

type sendMessageRequest struct {
	ChatID             int64  `json:"chat_id"`
	Text               string `json:"text"`
	MessageThreadID    int    `json:"message_thread_id,omitempty"`
	ReplyToMessageID   int    `json:"reply_to_message_id,omitempty"`
	DisableWebPagePrev bool   `json:"disable_web_page_preview,omitempty"`
}

func (c *Client) do(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	body []byte,
	out any,
) error {
	if out == nil {
		return errors.New("telegram: nil response target")
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("telegram: parse base url: %w", err)
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
		return fmt.Errorf("telegram: new request: %w", err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)),
		)
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("telegram: decode json: %w", err)
	}
	return nil
}

func validateResponse[T any](rsp apiResponse[T]) error {
	if rsp.OK {
		return nil
	}
	if rsp.Description == "" {
		return errors.New("telegram: api error")
	}
	return fmt.Errorf(
		"telegram: api error %d: %s",
		rsp.ErrorCode,
		rsp.Description,
	)
}
