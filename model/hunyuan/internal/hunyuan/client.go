//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package hunyuan provides a client for the Hunyuan API.
package hunyuan

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// HunYuanBaseUrl is the base URL for the Hunyuan API.
	HunYuanBaseUrl = "https://hunyuan.tencentcloudapi.com"
	// HunYuanHost is the host for the Hunyuan API.
	HunYuanHost = "hunyuan.tencentcloudapi.com"
	// HunYuanDefaultAction is the default action for the Hunyuan API.
	HunYuanDefaultAction = "ChatCompletions"
)

// Client is the Hunyuan API client.
type Client struct {
	config     *clientConfig
	httpClient *http.Client
}

type clientConfig struct {
	baseUrl    string
	host       string
	secretId   string
	secretKey  string
	httpClient *http.Client
}

var defaultConfig = clientConfig{
	baseUrl:   HunYuanBaseUrl,
	host:      HunYuanHost,
	secretId:  "",
	secretKey: "",
}

// Option is a functional option for configuring the Hunyuan client.
type Option func(*clientConfig)

// WithBaseUrl sets the base URL for the Hunyuan client.
// default: https://hunyuan.tencentcloudapi.com
func WithBaseUrl(baseUrl string) Option {
	return func(c *clientConfig) {
		c.baseUrl = baseUrl
	}
}

// WithHost sets the host for the Hunyuan client.
// default: hunyuan.tencentcloudapi.com
func WithHost(host string) Option {
	return func(c *clientConfig) {
		c.host = host
	}
}

// WithSecretId sets the secret ID for the Hunyuan client.
func WithSecretId(secretId string) Option {
	return func(c *clientConfig) {
		c.secretId = secretId
	}
}

// WithSecretKey sets the secret key for the Hunyuan client.
func WithSecretKey(secretKey string) Option {
	return func(c *clientConfig) {
		c.secretKey = secretKey
	}
}

// WithHttpClient sets the HTTP client for the Hunyuan client.
func WithHttpClient(httpClient *http.Client) Option {
	return func(c *clientConfig) {
		c.httpClient = httpClient
	}
}

// NewClient creates a new Hunyuan client with the given configuration.
func NewClient(options ...Option) *Client {
	cfg := defaultConfig
	for _, option := range options {
		option(&cfg)
	}
	cli := &Client{
		config:     &cfg,
		httpClient: http.DefaultClient,
	}
	if cfg.httpClient != nil {
		cli.httpClient = cfg.httpClient
	}
	return cli
}

// ChatCompletion sends a chat completion request to Hunyuan API.
func (c *Client) ChatCompletion(ctx context.Context, params *ChatCompletionNewParams) (*ChatCompletionResponse, error) {
	// Marshal request payload
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.baseUrl, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Get authorization header
	timestamp := time.Now().Unix()
	authorization := c.getAuthorization(string(payload), timestamp)

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", c.config.host)
	req.Header.Set("X-TC-Action", HunYuanDefaultAction)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-TC-Version", "2023-09-01")
	req.Header.Set("Authorization", authorization)

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result chatCompletionResponseData
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Response.Error != nil {
		return nil, fmt.Errorf("API request failed with error: %s", result.Response.Error.Message)
	}

	return &result.Response, nil
}

// ChatCompletionStream sends a streaming chat completion request to Hunyuan API.
func (c *Client) ChatCompletionStream(ctx context.Context, params *ChatCompletionNewParams, callback func(*ChatCompletionResponse) error) error {
	params.Stream = true

	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.baseUrl, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Get authorization header
	timestamp := time.Now().Unix()
	authorization := c.getAuthorization(string(payload), timestamp)

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", c.config.host)
	req.Header.Set("X-TC-Action", HunYuanDefaultAction)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-TC-Version", "2023-09-01")
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
		}
		line := scanner.Text()

		if line == "" {
			continue
		}

		// {
		//    "Response": {
		//        "RequestId": "188cc996-ab09-49a7-aa9f-1df88f11c6b4",
		//        "Error": {
		//            "Code": "InvalidParameter",
		//            "Message": "Temperature must be 2 or less"
		//        }
		//    }
		//}
		// Check for API error in SSE stream
		if strings.Contains(line, "Error") && strings.Contains(line, "Code") {
			var res chatCompletionResponseData
			if err := json.Unmarshal([]byte(line), &res); err == nil {
				if res.Response.Error != nil {
					return fmt.Errorf("API error [%s]: %s", res.Response.Error.Code, res.Response.Error.Message)
				}
			}
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

			// Check for stream end
			if data == "[DONE]" {
				break
			}

			// Parse JSON chunk
			var chunk ChatCompletionResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				return fmt.Errorf("failed to decode chunk: %w", err)
			}

			// Check for API error in chunk
			if chunk.Error != nil {
				return fmt.Errorf("API error [%s]: %s", chunk.Error.Code, chunk.Error.Message)
			}

			// Call callback with chunk
			if err := callback(&chunk); err != nil {
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stream: %w", err)
	}

	return nil
}

func (c *Client) getAuthorization(payload string, timestamp int64) (authorization string) {
	algorithm := "TC3-HMAC-SHA256"
	service := "hunyuan"

	httpRequestMethod := "POST"
	canonicalURI := "/"
	canonicalQueryString := ""
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-tc-action:%s\n",
		"application/json", c.config.host, strings.ToLower(HunYuanDefaultAction))
	signedHeaders := "content-type;host;x-tc-action"
	hashedRequestPayload := sha256hex(payload)
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		httpRequestMethod,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		hashedRequestPayload)

	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	credentialScope := fmt.Sprintf("%s/%s/tc3_request", date, service)
	hashedCanonicalRequest := sha256hex(canonicalRequest)
	string2sign := fmt.Sprintf("%s\n%d\n%s\n%s",
		algorithm,
		timestamp,
		credentialScope,
		hashedCanonicalRequest)

	secretDate := hmacSha256(date, "TC3"+c.config.secretKey)
	secretService := hmacSha256(service, secretDate)
	secretSigning := hmacSha256("tc3_request", secretService)
	signature := hex.EncodeToString([]byte(hmacSha256(string2sign, secretSigning)))

	authorization = fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm,
		c.config.secretId,
		credentialScope,
		signedHeaders,
		signature)

	return
}

func sha256hex(s string) string {
	b := sha256.Sum256([]byte(s))

	return hex.EncodeToString(b[:])
}

func hmacSha256(s, key string) string {
	hashed := hmac.New(sha256.New, []byte(key))
	hashed.Write([]byte(s))

	return string(hashed.Sum(nil))
}
