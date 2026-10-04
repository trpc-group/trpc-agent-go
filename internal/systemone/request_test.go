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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

type stateJSON struct {
	calls *int
	data  string
	err   error
}

func (s stateJSON) MarshalJSON() ([]byte, error) {
	*s.calls++
	return []byte(s.data), s.err
}

func TestStateEncoding(t *testing.T) {
	type reviewState struct {
		Tool    string `json:"tool"`
		Count   int    `json:"count"`
		Private string `json:"-"`
	}
	for _, tc := range []struct {
		name  string
		state any
		want  string
	}{
		{"struct", reviewState{Tool: "ls", Count: 2, Private: "not sent"}, `{"tool":"ls","count":2}`},
		{"string", "hello", `"hello"`},
		{"empty string", "", `""`},
		{"object", map[string]any{"flag": false, "count": 2, "nested": nil}, `{"count":2,"flag":false,"nested":null}`},
		{"empty object", map[string]string{}, `{}`},
		{"array", []string{"a", "b"}, `["a","b"]`},
		{"empty array", []string{}, `[]`},
		{"raw json", json.RawMessage(`{"custom":true}`), `{"custom":true}`},
		{"bytes use base64", []byte("hello"), `"aGVsbG8="`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct{ State json.RawMessage }
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				if string(wire.State) != tc.want {
					t.Errorf("state: got %s, want %s", wire.State, tc.want)
				}
				_, _ = io.WriteString(w, binaryResponse)
			}))
			defer srv.Close()
			client, err := systemone.NewClient(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := binaryRequest()
			req.State = tc.state
			if _, err := client.SystemOne(context.Background(), req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStateMarshalJSON(t *testing.T) {
	cause := errors.New("state cannot be marshaled")
	for _, tc := range []struct {
		name, data string
		err        error
		success    bool
	}{
		{name: "object", data: `{"nested":null}`, success: true},
		{name: "array", data: `[]`, success: true},
		{name: "string", data: `"hello"`, success: true},
		{name: "null", data: `null`},
		{name: "number", data: `1`},
		{name: "boolean", data: `true`},
		{name: "malformed", data: `{`},
		{name: "error", err: cause},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var marshals, requests int
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				var wire struct{ State json.RawMessage }
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				if string(wire.State) != tc.data {
					t.Errorf("custom encoding changed: %s", wire.State)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(binaryResponse)), Request: r}, nil
			})
			client, err := systemone.NewClient("https://example.invalid", systemone.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			req := binaryRequest()
			req.State = stateJSON{calls: &marshals, data: tc.data, err: tc.err}
			response, err := client.SystemOne(context.Background(), req)
			if marshals != 1 {
				t.Fatalf("MarshalJSON called %d times", marshals)
			}
			if tc.success {
				if err != nil || requests != 1 || response == nil {
					t.Fatalf("valid state failed: %v, requests=%d", err, requests)
				}
			} else if err == nil || requests != 0 || response != nil {
				t.Fatalf("invalid state accepted: err=%v requests=%d", err, requests)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("marshal error cause lost: %v", err)
			}
		})
	}
}

func TestQuestionTextEncoding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		question systemone.Question
		want     string
		answer   string
	}{
		{"binary nil criteria", systemone.BinaryQuestion{Instructions: "  判断\n"},
			`{"type":"noul","instructions":"  判断\n"}`, `{"type":"noul","noul":0}`},
		{"binary empty criteria", &systemone.BinaryQuestion{Instructions: "judge", Criteria: &systemone.BinaryCriteria{}},
			`{"type":"noul","instructions":"judge"}`, `{"type":"noul","noul":0}`},
		{"binary one criterion", systemone.BinaryQuestion{Instructions: "judge", Criteria: &systemone.BinaryCriteria{False: "否"}},
			`{"type":"noul","instructions":"judge","criteria":{"false":"否"}}`, `{"type":"noul","noul":0}`},
		{"binary both criteria", systemone.BinaryQuestion{Instructions: "judge", Criteria: &systemone.BinaryCriteria{True: "是", False: "否"}},
			`{"type":"noul","instructions":"judge","criteria":{"true":"是","false":"否"}}`, `{"type":"noul","noul":0}`},
		{"choice case sensitive order", &systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a", Description: "  lower  "}, {Name: "A"}}},
			`{"type":"choice","instructions":"choose","criteria":{"a":"  lower  ","A":null}}`, `{"type":"choice","choice":"a","confidence":0,"probabilities":{"A":0.5,"a":0.5}}`},
		{"single score", systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{" low "}},
			`{"type":"score","instructions":"rate","criteria":[" low "]}`, `{"type":"score","score":0,"confidence":0,"probabilities":{"0":1},"legend":{"0":"returned text"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const id = "判断/一"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct{ Questions map[string]json.RawMessage }
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				if string(wire.Questions[id]) != tc.want {
					t.Errorf("question: got %s, want %s", wire.Questions[id], tc.want)
				}
				_, _ = io.WriteString(w, `{"model":"test","answers":{"`+id+`":`+tc.answer+`}}`)
			}))
			defer srv.Close()
			client, err := systemone.NewClient(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			req := &systemone.Request{State: "state", Questions: map[string]systemone.Question{id: tc.question}}
			before, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.SystemOne(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(req)
			if err != nil || string(before) != string(after) {
				t.Fatal("request mutated")
			}
			if tc.name == "single score" {
				score, err := resp.Score(id)
				if err != nil || score.Score != 0 || len(score.Levels) != 1 || score.Levels[0].Probability != 1 || score.Levels[0].Description != "returned text" {
					t.Fatalf("single level or server description lost: %+v %v", score, err)
				}
			}
		})
	}
}
