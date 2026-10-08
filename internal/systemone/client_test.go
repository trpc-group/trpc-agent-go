//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

func mixedRequest() *systemone.Request {
	return &systemone.Request{
		State: map[string]any{"message": "我被重复扣款了", "retries": 2},
		Questions: map[string]systemone.Question{
			"route": systemone.ChoiceQuestion{
				Instructions: "Which team?",
				Options: []systemone.ChoiceOption{
					{Name: "z_billing", Description: "Payments"},
					{Name: "a_other"},
				},
			},
			"severity": &systemone.ScoreQuestion{
				Instructions: "Rate urgency",
				Criteria:     []string{"low", "medium", "high"},
			},
			"refund": &systemone.BinaryQuestion{
				Instructions: "Is a refund requested?",
				Criteria:     &systemone.BinaryCriteria{True: "Explicit refund request"},
			},
		},
	}
}

func binaryRequest() *systemone.Request {
	return &systemone.Request{State: "hello", Questions: map[string]systemone.Question{
		"q": systemone.BinaryQuestion{Instructions: "Is this a greeting?"},
	}}
}

const binaryResponse = `{"model":"test","answers":{"q":{"type":"noul","noul":0}},"usage":{"input_tokens":0}}`

func TestProviderWireCompatibility(t *testing.T) {
	for _, provider := range []string{"jev", "laya"} {
		t.Run(provider, func(t *testing.T) {
			fixture, err := os.ReadFile("testdata/" + provider + ".json")
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/gateway/v1/systemone" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer test-key" ||
					r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
					t.Errorf("unexpected headers: %v", r.Header)
				}
				body, _ := io.ReadAll(r.Body)
				var request struct {
					Model     string
					State     map[string]any
					Questions map[string]struct {
						Type         string
						Instructions json.RawMessage
						Criteria     json.RawMessage
					}
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Error(err)
				}
				if request.Model != "explicit-model" || request.State["message"] != "我被重复扣款了" {
					t.Errorf("request lost model or structured state: %s", body)
				}
				if got := string(request.Questions["route"].Criteria); got != `{"z_billing":"Payments","a_other":null}` {
					t.Errorf("option order/null changed: %s", got)
				}
				if got := string(request.Questions["severity"].Criteria); got != `["low","medium","high"]` {
					t.Errorf("text criteria changed: %s", got)
				}
				if got := string(request.Questions["refund"].Criteria); got != `{"true":"Explicit refund request"}` {
					t.Errorf("noul criteria changed: %s", got)
				}
				for id, kind := range map[string]string{"refund": "noul", "route": "choice", "severity": "score"} {
					if request.Questions[id].Type != kind {
						t.Errorf("question %s has wrong wire type", id)
					}
				}
				if string(request.Questions["severity"].Instructions) != `"Rate urgency"` {
					t.Error("instructions must be JSON text")
				}
				w.Header().Set("X-Request-ID", "req-123")
				_, _ = w.Write(fixture)
			}))
			defer srv.Close()
			client, err := systemone.NewClient(srv.URL+"/gateway/",
				systemone.WithAPIKey("test-key"), systemone.WithDefaultModel("default-model"))
			if err != nil {
				t.Fatal(err)
			}
			req := mixedRequest()
			req.Model = "explicit-model"
			resp, err := client.SystemOne(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			choice, err := resp.Choice("route")
			if err != nil {
				t.Fatal(err)
			}
			if choice.Choice != "z_billing" || choice.Probabilities["a_other"] != 0.2 {
				t.Fatalf("unexpected choice: %+v", choice)
			}
			score, err := resp.Score("severity")
			if err != nil {
				t.Fatal(err)
			}
			binary, err := resp.Binary("refund")
			if err != nil {
				t.Fatal(err)
			}
			if score.Score != 1.7 || binary.Probability != 0 {
				t.Fatalf("fractional score or zero noul lost: %+v", resp)
			}
			wantLevels := []systemone.ScoreLevel{
				{Description: "low", Probability: 0},
				{Description: "medium", Probability: 0.3},
				{Description: "high", Probability: 0.7},
			}
			if !reflect.DeepEqual(score.Levels, wantLevels) {
				t.Fatalf("score levels are not paired in ordinal order: %+v", score.Levels)
			}
			if resp.RequestID != "req-123" || string(resp.Raw) != string(fixture) {
				t.Fatal("request ID or raw extensions were lost")
			}
			if provider == "jev" {
				if choice.Confidence != 0.6 || resp.Usage.Truncated != nil ||
					score.Levels[1].Description != "medium" {
					t.Fatal("Jev confidence, legend, or absent truncation changed")
				}
			} else {
				if choice.Confidence != 0.2781 || resp.Usage.Truncated == nil || !*resp.Usage.Truncated ||
					score.Levels[1].Description != "medium" {
					t.Fatal("Laya confidence, string legend, or truncation changed")
				}
			}
		})
	}
}

func TestLocalDefaultsAndUsage(t *testing.T) {
	for _, defaultModel := range []string{"", "jev-latest"} {
		t.Run(defaultModel, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				_, present := body["model"]
				if present != (defaultModel != "") || r.Header.Get("Authorization") != "" {
					t.Error("local default must omit model and authentication")
				}
				if present && string(body["model"]) != `"jev-latest"` {
					t.Error("configured default model not sent")
				}
				_, _ = io.WriteString(w, binaryResponse)
			}))
			defer srv.Close()
			client, _ := systemone.NewClient(srv.URL, nil, systemone.WithDefaultModel(defaultModel))
			req := binaryRequest()
			resp, err := client.SystemOne(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if req.Model != "" || resp.Usage.InputTokens == nil || *resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != nil {
				t.Fatal("request mutated or absent usage conflated with zero")
			}
		})
	}
}

func TestHTTPErrorsDoNotRetry(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Request-ID", "failed-request")
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"detail":"sensitive error data"}`)
			}))
			defer srv.Close()
			client, _ := systemone.NewClient(srv.URL)
			resp, err := client.SystemOne(context.Background(), binaryRequest())
			var httpErr *systemone.HTTPError
			if resp != nil || !errors.As(err, &httpErr) || httpErr.StatusCode != status || calls.Load() != 1 {
				t.Fatalf("unexpected error/retry: response=%v error=%v calls=%d", resp, err, calls.Load())
			}
			if httpErr.RequestID != "failed-request" || httpErr.RetryAfter != "2" ||
				string(httpErr.Body) != `{"detail":"sensitive error data"}` || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("error metadata lost or leaked: %+v", httpErr)
			}
		})
	}
}

func TestHTTPErrorBodyReadFailure(t *testing.T) {
	const partialBody = `{"detail":"sensitive partial error`
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Length", "100")
				w.Header().Set("X-Request-ID", "interrupted-response")
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, partialBody)
			}))
			defer srv.Close()
			client, err := systemone.NewClient(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.SystemOne(context.Background(), binaryRequest())
			if resp != nil || !errors.Is(err, io.ErrUnexpectedEOF) || calls.Load() != 1 {
				t.Fatalf("unexpected response/read error/retry: response=%v error=%v calls=%d", resp, err, calls.Load())
			}
			var httpErr *systemone.HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status ||
				httpErr.RequestID != "interrupted-response" || httpErr.RetryAfter != "30" ||
				string(httpErr.Body) != partialBody || !httpErr.Truncated {
				t.Fatalf("HTTP error metadata or partial body lost: %v", err)
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("response body leaked in error: %v", err)
			}
		})
	}
}

func TestTransportErrorDoesNotRetry(t *testing.T) {
	transportErr := errors.New("connection failed")
	var calls int
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, transportErr
	})
	client, err := systemone.NewClient("https://example.invalid",
		systemone.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SystemOne(context.Background(), binaryRequest())
	if resp != nil || !errors.Is(err, transportErr) {
		t.Fatalf("transport error cause lost or partial response returned: response=%v error=%v", resp, err)
	}
	if calls != 1 {
		t.Fatalf("transport called %d times, want 1", calls)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	var callbackCalls atomic.Int32
	original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		callbackCalls.Add(1)
		return nil
	}}
	client, _ := systemone.NewClient(srv.URL, systemone.WithHTTPClient(original), systemone.WithAPIKey("secret"))
	_, err := client.SystemOne(context.Background(), binaryRequest())
	var httpErr *systemone.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 307 || targetCalls.Load() != 0 || callbackCalls.Load() != 0 {
		t.Fatalf("redirect policy failed: %v", err)
	}
	_ = original.CheckRedirect(nil, nil)
	if callbackCalls.Load() != 1 {
		t.Fatal("caller HTTP client was mutated")
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send headers first to exercise cancellation during response reading.
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	client, _ := systemone.NewClient(srv.URL, systemone.WithTimeout(20*time.Millisecond))
	_, err := client.SystemOne(context.Background(), binaryRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline cause lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.SystemOne(ctx, binaryRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, binaryResponse)
	}))
	defer srv.Close()
	client, _ := systemone.NewClient(srv.URL)
	req := binaryRequest()
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := client.SystemOne(context.Background(), req); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
}

func TestResponseSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", (8<<20)+1))
	}))
	defer srv.Close()
	client, _ := systemone.NewClient(srv.URL)
	resp, err := client.SystemOne(context.Background(), binaryRequest())
	if resp != nil || err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("oversized response accepted: %v", err)
	}
}

func TestResponseBodyClosed(t *testing.T) {
	readErr := errors.New("response stream interrupted")
	for _, tc := range []struct {
		name   string
		status int
		body   string
		cause  error
	}{
		{name: "success", status: http.StatusOK, body: binaryResponse},
		{name: "decode error", status: http.StatusOK, body: "invalid json"},
		{name: "http error", status: http.StatusServiceUnavailable, body: "unavailable"},
		{name: "oversized success", status: http.StatusOK, body: strings.Repeat(" ", (8<<20)+1)},
		{name: "oversized error", status: http.StatusServiceUnavailable, body: strings.Repeat("x", (8<<20)+1)},
		{name: "read error", status: http.StatusOK, cause: readErr},
		{name: "success body with read error", status: http.StatusOK, body: binaryResponse, cause: readErr},
		{name: "http error with empty body read error", status: http.StatusServiceUnavailable, cause: readErr},
		{name: "http error with partial body", status: http.StatusServiceUnavailable, body: "partial", cause: readErr},
		{name: "http error with canceled read", status: http.StatusServiceUnavailable, body: "partial", cause: context.Canceled},
		{name: "http error with read deadline", status: http.StatusServiceUnavailable, body: "partial", cause: context.DeadlineExceeded},
		{name: "limit-sized error", status: http.StatusServiceUnavailable, body: strings.Repeat("x", 8<<20)},
		{name: "limit-sized error with read error", status: http.StatusServiceUnavailable, body: strings.Repeat("x", 8<<20), cause: readErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedResponseBody{Reader: strings.NewReader(tc.body), readErr: tc.cause}
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status, Body: body, Request: req,
					Header: http.Header{"X-Request-Id": {"tracked-response"}, "Retry-After": {"2"}},
				}, nil
			})
			client, err := systemone.NewClient("https://example.invalid",
				systemone.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.SystemOne(context.Background(), binaryRequest())
			if body.closes != 1 {
				t.Fatalf("response body closed %d times, want 1", body.closes)
			}
			if tc.name == "success" {
				if err != nil || response == nil || response.RequestID != "tracked-response" {
					t.Fatalf("unexpected response: %v, error: %v", response, err)
				}
				return
			}
			if err == nil || response != nil {
				t.Fatalf("expected error without partial response: %v, %v", response, err)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("read error cause lost: %v", err)
			}
			if tc.status >= 300 {
				var httpErr *systemone.HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != tc.status ||
					httpErr.RequestID != "tracked-response" || httpErr.RetryAfter != "2" ||
					httpErr.Truncated != (len(tc.body) > 8<<20 || tc.cause != nil) ||
					string(httpErr.Body) != tc.body[:min(len(tc.body), 8<<20)] {
					t.Fatalf("HTTP error metadata or bounded body lost: %v", err)
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackedResponseBody struct {
	io.Reader
	readErr error
	closes  int
}

func (b *trackedResponseBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF && b.readErr != nil {
		return n, b.readErr
	}
	return n, err
}

func (b *trackedResponseBody) Close() error {
	b.closes++
	return nil
}
