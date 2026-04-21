//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	httpHeaderAuthorization = "Authorization"
	httpHeaderAccept        = "Accept"
	httpHeaderContentType   = "Content-Type"

	httpContentTypeJSON = "application/json"

	httpMethodGet  = "GET"
	httpMethodPost = "POST"

	maxResponseBodySize = 10 << 20

	maxRetries       = 3
	retryBaseBackoff = 200 * time.Millisecond
	retryMaxBackoff  = 2 * time.Second

	maxErrorBodyPreview = 512
)

type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("mem0 api request failed: status=%d body=%s", e.StatusCode, e.Body)
}

type client struct {
	host      string
	apiKey    string
	orgID     string
	projectID string
	hc        *http.Client
	timeout   time.Duration
}

func newClient(opts serviceOpts) (*client, error) {
	if opts.apiKey == "" {
		return nil, errors.New("mem0 api key is required")
	}
	hc := opts.client
	if hc == nil {
		hc = &http.Client{}
	}
	return &client{
		host:      strings.TrimRight(opts.host, "/"),
		apiKey:    opts.apiKey,
		orgID:     opts.orgID,
		projectID: opts.projectID,
		hc:        hc,
		timeout:   opts.timeout,
	}, nil
}

func (c *client) doJSON(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	in any,
	out any,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	u, err := url.Parse(c.host)
	if err != nil {
		return fmt.Errorf("mem0: invalid host: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var payload []byte
	if in != nil {
		payload, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("mem0: marshal request failed: %w", err)
		}
	}

	attempts := maxRetries + 1
	if method != httpMethodGet {
		attempts = 1 // Do not retry non-idempotent write requests.
	}
	for i := 0; i < attempts; i++ {
		err = c.doJSONOnce(ctx, method, u.String(), payload, out)
		if err == nil {
			return nil
		}
		if !shouldRetry(err) || i == attempts-1 {
			return err
		}
		t := time.NewTimer(retrySleep(i, cryptoJitter))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return err
}

func (c *client) doJSONOnce(
	ctx context.Context,
	method string,
	urlStr string,
	payload []byte,
	out any,
) error {
	if method == "" {
		return errors.New("mem0: http method is empty")
	}
	if urlStr == "" {
		return errors.New("mem0: url is empty")
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return fmt.Errorf("mem0: build request failed: %w", err)
	}
	req.Header.Set(httpHeaderAuthorization, "Token "+c.apiKey)
	req.Header.Set(httpHeaderAccept, httpContentTypeJSON)
	if payload != nil {
		req.Header.Set(httpHeaderContentType, httpContentTypeJSON)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mem0: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
	if err != nil {
		return fmt.Errorf("mem0: read response failed: %w", err)
	}
	if len(respBody) > maxResponseBodySize {
		return errors.New("mem0: response body too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := string(respBody)
		if len(preview) > maxErrorBodyPreview {
			preview = preview[:maxErrorBodyPreview] + "...(truncated)"
		}
		return &apiError{StatusCode: resp.StatusCode, Body: preview}
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("mem0: unmarshal response failed: %w", err)
	}
	return nil
}

func shouldRetry(err error) bool {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return false
}

func retrySleep(attempt int, jitterFn func(max int64) int64) time.Duration {
	if attempt < 0 {
		attempt = 1
	}
	d := min(time.Duration(1<<attempt)*retryBaseBackoff, retryMaxBackoff)
	if jitterFn == nil || d <= 1 {
		return d
	}
	jitterMax := int64(d / 2)
	if jitterMax <= 0 {
		return d
	}
	jitter := time.Duration(jitterFn(jitterMax))
	if jitter < 0 {
		jitter = 0
	}
	if jitter > d/2 {
		jitter = d / 2
	}
	return d/2 + jitter
}

func cryptoJitter(max int64) int64 {
	if max <= 0 {
		return 0
	}
	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(max))
	if err != nil {
		return 0
	}
	return n.Int64()
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
