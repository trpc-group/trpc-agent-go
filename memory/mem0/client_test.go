//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApiError_Error(t *testing.T) {
	e := &apiError{StatusCode: 418, Body: "teapot"}
	msg := e.Error()
	assert.Contains(t, msg, "418")
	assert.Contains(t, msg, "teapot")
}

func TestNewClient_RequiresAPIKey(t *testing.T) {
	_, err := newClient(serviceOpts{})
	assert.Error(t, err)
}

func TestNewClient_TrimsHostTrailingSlash(t *testing.T) {
	c, err := newClient(serviceOpts{apiKey: "k", host: "https://api.example.com///"})
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com", c.host)
}

func TestNewClient_UsesCustomHTTPClient(t *testing.T) {
	hc := &http.Client{}
	c, err := newClient(serviceOpts{apiKey: "k", client: hc})
	require.NoError(t, err)
	assert.Same(t, hc, c.hc)
}

func TestNewClient_DefaultHTTPClientWhenNil(t *testing.T) {
	c, err := newClient(serviceOpts{apiKey: "k"})
	require.NoError(t, err)
	assert.NotNil(t, c.hc)
}

func TestDoJSON_GetSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token k", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		assert.Equal(t, "1", r.URL.Query().Get("x"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, err := newClient(serviceOpts{apiKey: "k", host: srv.URL})
	require.NoError(t, err)
	var out struct {
		Ok bool `json:"ok"`
	}
	q := url.Values{"x": {"1"}}
	require.NoError(t, c.doJSON(context.Background(), httpMethodGet, "/ping", q, nil, &out))
	assert.True(t, out.Ok)
}

func TestDoJSON_PostMarshalsBodyAndSetsContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		b, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(b), `"k":"v"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})
	body := map[string]string{"k": "v"}
	assert.NoError(t, c.doJSON(context.Background(), httpMethodPost, "/p", nil, body, nil))
}

func TestDoJSON_InvalidHost(t *testing.T) {
	c := &client{host: "://bad", apiKey: "k", hc: &http.Client{}}
	assert.Error(t, c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, nil))
}

func TestDoJSON_MarshalFailure(t *testing.T) {
	c := &client{host: "https://example.com", apiKey: "k", hc: &http.Client{}}
	// A channel can't be marshaled to JSON.
	assert.Error(t, c.doJSON(
		context.Background(), httpMethodPost, "/p", nil,
		map[string]any{"ch": make(chan int)}, nil,
	))
}

func TestDoJSON_Non2xxReturnsApiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})

	err := c.doJSON(context.Background(), httpMethodPost, "/p", nil, nil, nil)
	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 400, apiErr.StatusCode)
}

func TestDoJSON_Non2xxBodyIsTruncated(t *testing.T) {
	big := strings.Repeat("X", maxErrorBodyPreview*4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})

	err := c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, nil)
	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.True(t, strings.HasSuffix(apiErr.Body, "...(truncated)"), "body not truncated: %q", apiErr.Body)
	assert.LessOrEqual(t, len(apiErr.Body), maxErrorBodyPreview+len("...(truncated)"))
}

func TestDoJSON_ResponseBodyTooLargeIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		buf := make([]byte, maxResponseBodySize+2)
		for i := range buf {
			buf[i] = 'a'
		}
		_, _ = w.Write(buf)
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})

	err := c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, &struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response body too large")
}

func TestDoJSON_UnmarshalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})
	err := c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, &struct {
		K string `json:"k"`
	}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestDoJSON_NilOutOrEmptyBodyIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})

	// nil out pointer.
	assert.NoError(t, c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, nil))
	// empty body with a non-nil out should also succeed without unmarshal.
	var out struct{}
	assert.NoError(t, c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, &out))
}

func TestDoJSON_GetRetriesOn500(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})
	var out struct {
		Ok bool `json:"ok"`
	}
	require.NoError(t, c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, &out))
	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(3))
}

func TestDoJSON_PostDoesNotRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})

	assert.Error(t, c.doJSON(context.Background(), httpMethodPost, "/p", nil, nil, nil))
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "non-idempotent method must not retry")
}

func TestDoJSON_GetStopsAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})

	assert.Error(t, c.doJSON(context.Background(), httpMethodGet, "/p", nil, nil, nil))
	assert.Equal(t, int32(maxRetries+1), atomic.LoadInt32(&calls))
}

func TestDoJSON_ContextCancelAbortsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	assert.Error(t, c.doJSON(ctx, httpMethodGet, "/p", nil, nil, nil))
}

func TestDoJSONOnce_EmptyMethod(t *testing.T) {
	c := &client{host: "http://x", apiKey: "k", hc: &http.Client{}}
	err := c.doJSONOnce(context.Background(), "", "http://x", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "method is empty")
}

func TestDoJSONOnce_EmptyURL(t *testing.T) {
	c := &client{host: "http://x", apiKey: "k", hc: &http.Client{}}
	err := c.doJSONOnce(context.Background(), httpMethodGet, "", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is empty")
}

func TestDoJSONOnce_BuildRequestError(t *testing.T) {
	c := &client{host: "http://x", apiKey: "k", hc: &http.Client{}}
	// Invalid method name (contains space) triggers http.NewRequest build failure.
	assert.Error(t, c.doJSONOnce(context.Background(), "GET HTTP", "http://x", nil, nil))
}

func TestDoJSON_HonoursPerRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c, _ := newClient(serviceOpts{apiKey: "k", host: srv.URL, timeout: 10 * time.Millisecond})
	assert.Error(t, c.doJSON(context.Background(), httpMethodPost, "/p", nil, nil, nil))
}

func TestShouldRetry(t *testing.T) {
	assert.False(t, shouldRetry(nil))
	assert.False(t, shouldRetry(errors.New("boom")))
	assert.True(t, shouldRetry(&apiError{StatusCode: 429}))
	assert.True(t, shouldRetry(&apiError{StatusCode: 503}))
	assert.False(t, shouldRetry(&apiError{StatusCode: 400}))
	assert.True(t, shouldRetry(&timeoutErr{}))
	assert.True(t, shouldRetry(&tempErr{}))
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

type tempErr struct{}

func (tempErr) Error() string   { return "temp" }
func (tempErr) Timeout() bool   { return false }
func (tempErr) Temporary() bool { return true }

// Ensure our stubs satisfy net.Error so errors.As hits the right branch.
var (
	_ net.Error = timeoutErr{}
	_ net.Error = tempErr{}
)

func TestRetrySleep(t *testing.T) {
	// Negative attempt is normalised to 1 (which produces 2*baseBackoff).
	assert.Equal(t, retryBaseBackoff*2, retrySleep(-1, nil))
	// Attempt 0 with no jitter is exactly baseBackoff.
	assert.Equal(t, retryBaseBackoff, retrySleep(0, nil))

	expectedHalf := (retryBaseBackoff * 2) / 2
	// Jitter returning 0 yields the lower half of the backoff window.
	assert.Equal(t, expectedHalf, retrySleep(1, func(max int64) int64 { return 0 }))
	// Jitter exceeding max is capped at d/2.
	assert.LessOrEqual(t, retrySleep(1, func(max int64) int64 { return max * 10 }), retryBaseBackoff*2)
	// Negative jitter is clamped to 0.
	assert.Equal(t, expectedHalf, retrySleep(1, func(max int64) int64 { return -5 }))
	// Very large attempt is capped at retryMaxBackoff.
	assert.Equal(t, retryMaxBackoff, retrySleep(20, nil))
}

func TestCryptoJitter(t *testing.T) {
	assert.Zero(t, cryptoJitter(0))
	assert.Zero(t, cryptoJitter(-1))
	for i := 0; i < 16; i++ {
		got := cryptoJitter(100)
		assert.GreaterOrEqual(t, got, int64(0))
		assert.Less(t, got, int64(100))
	}
}

func TestItoa(t *testing.T) {
	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "-5", itoa(-5))
	assert.Equal(t, "123", itoa(123))
}
