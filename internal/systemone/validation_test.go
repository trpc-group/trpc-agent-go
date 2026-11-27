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
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// Embedding can satisfy the marker interface without defining a supported type.
type embeddedQuestion struct{ systemone.Question }

func TestInvalidRequestsDoNotReachServer(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, binaryResponse)
	}))
	defer srv.Close()
	client, _ := systemone.NewClient(srv.URL)
	invalidUTF8 := string([]byte{0xff})
	setQuestion := func(q systemone.Question) func(*systemone.Request) {
		return func(r *systemone.Request) { r.Questions["q"] = q }
	}
	for name, change := range map[string]func(*systemone.Request){
		"nil state":                   func(r *systemone.Request) { r.State = nil },
		"typed nil state map":         func(r *systemone.Request) { r.State = map[string]string(nil) },
		"typed nil state slice":       func(r *systemone.Request) { r.State = []string(nil) },
		"typed nil state pointer":     func(r *systemone.Request) { r.State = (*string)(nil) },
		"number state":                func(r *systemone.Request) { r.State = 3 },
		"boolean state":               func(r *systemone.Request) { r.State = true },
		"invalid raw state":           func(r *systemone.Request) { r.State = json.RawMessage(`{`) },
		"empty raw state":             func(r *systemone.Request) { r.State = json.RawMessage{} },
		"unsupported state":           func(r *systemone.Request) { r.State = make(chan int) },
		"no questions":                func(r *systemone.Request) { r.Questions = nil },
		"nil question":                setQuestion(nil),
		"nil binary pointer":          setQuestion((*systemone.BinaryQuestion)(nil)),
		"nil choice pointer":          setQuestion((*systemone.ChoiceQuestion)(nil)),
		"nil score pointer":           setQuestion((*systemone.ScoreQuestion)(nil)),
		"embedded question":           setQuestion(embeddedQuestion{systemone.BinaryQuestion{Instructions: "hello"}}),
		"embedded nil question":       setQuestion(embeddedQuestion{}),
		"invalid question id":         func(r *systemone.Request) { r.Questions[invalidUTF8] = r.Questions["q"] },
		"empty question id":           func(r *systemone.Request) { r.Questions[""] = r.Questions["q"] },
		"blank question id":           func(r *systemone.Request) { r.Questions[" \t\n"] = r.Questions["q"] },
		"missing binary instructions": setQuestion(systemone.BinaryQuestion{}),
		"blank binary instructions":   setQuestion(systemone.BinaryQuestion{Instructions: " \n"}),
		"invalid binary instructions": setQuestion(systemone.BinaryQuestion{Instructions: invalidUTF8}),
		"missing choice instructions": setQuestion(systemone.ChoiceQuestion{Options: []systemone.ChoiceOption{{Name: "a"}}}),
		"invalid choice instructions": setQuestion(systemone.ChoiceQuestion{Instructions: invalidUTF8, Options: []systemone.ChoiceOption{{Name: "a"}}}),
		"missing score instructions":  setQuestion(systemone.ScoreQuestion{Criteria: []string{"low"}}),
		"invalid score instructions":  setQuestion(systemone.ScoreQuestion{Instructions: invalidUTF8, Criteria: []string{"low"}}),
		"invalid true criterion":      setQuestion(systemone.BinaryQuestion{Instructions: "judge", Criteria: &systemone.BinaryCriteria{True: invalidUTF8}}),
		"invalid false criterion":     setQuestion(systemone.BinaryQuestion{Instructions: "judge", Criteria: &systemone.BinaryCriteria{False: invalidUTF8}}),
		"empty choices":               setQuestion(systemone.ChoiceQuestion{Instructions: "choose"}),
		"duplicate choices":           setQuestion(systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}, {Name: "a"}}}),
		"empty choice name":           setQuestion(systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: ""}}}),
		"blank choice name":           setQuestion(systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: " \t"}}}),
		"invalid choice name":         setQuestion(systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: invalidUTF8}}}),
		"invalid description":         setQuestion(systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a", Description: invalidUTF8}}}),
		"empty score":                 setQuestion(systemone.ScoreQuestion{Instructions: "rate"}),
		"blank score level":           setQuestion(systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{" \t"}}),
		"invalid score level":         setQuestion(systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{invalidUTF8}}),
	} {
		t.Run(name, func(t *testing.T) {
			req := binaryRequest()
			change(req)
			before := calls.Load()
			if resp, err := client.SystemOne(context.Background(), req); resp != nil || err == nil {
				t.Fatalf("invalid request accepted: %v %v", resp, err)
			}
			if calls.Load() != before {
				t.Fatal("invalid request reached server")
			}
		})
	}
	if _, err := client.SystemOne(context.Background(), nil); err == nil {
		t.Error("nil request accepted")
	}
	if _, err := client.SystemOne(context.Background(), &systemone.Request{}); err == nil {
		t.Error("zero request accepted")
	}
	if _, err := client.SystemOne(nil, binaryRequest()); err == nil {
		t.Error("nil context accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached server")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, url := range []string{"", "localhost:8000", "ftp://host", "https://", "https://host/%zz", "https://user:secret@host", "https://host?q=x", "https://host?", "https://host#x", "https://host#"} {
		if _, err := systemone.NewClient(url); err == nil {
			t.Errorf("invalid URL accepted: %q", url)
		}
	}
	if _, err := systemone.NewClient("https://host", systemone.WithTimeout(-time.Second)); err == nil {
		t.Error("negative timeout accepted")
	}
	if _, err := systemone.NewClient("https://host", systemone.WithAPIKey("key\r\nheader: value")); err == nil {
		t.Error("header injection accepted")
	}
	for _, c := range []*systemone.Client{nil, {}} {
		if _, err := c.SystemOne(context.Background(), binaryRequest()); err == nil {
			t.Error("uninitialized client accepted")
		}
	}
}

func TestInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"non json":            `<html>gateway error</html>`,
		"null":                `null`,
		"missing model":       `{"answers":{"q":{"type":"noul","noul":0}}}`,
		"missing answer":      `{"model":"test","answers":{}}`,
		"wrong id":            `{"model":"test","answers":{"other":{"type":"noul","noul":0}}}`,
		"extra answer":        `{"model":"test","answers":{"q":{"type":"noul","noul":0},"other":{"type":"noul","noul":0}}}`,
		"null answer":         `{"model":"test","answers":{"q":null}}`,
		"unknown type":        `{"model":"test","answers":{"q":{"type":"future","noul":0}}}`,
		"wrong type":          `{"model":"test","answers":{"q":{"type":"choice","choice":"x"}}}`,
		"missing probability": `{"model":"test","answers":{"q":{"type":"noul"}}}`,
		"null probability":    `{"model":"test","answers":{"q":{"type":"noul","noul":null}}}`,
		"out of range":        `{"model":"test","answers":{"q":{"type":"noul","noul":1.1}}}`,
		"negative usage":      `{"model":"test","answers":{"q":{"type":"noul","noul":0}},"usage":{"input_tokens":-1}}`,
		"trailing json":       binaryResponse + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			client, _ := systemone.NewClient(srv.URL)
			if resp, err := client.SystemOne(context.Background(), binaryRequest()); resp != nil || err == nil {
				t.Fatalf("malformed response accepted: %v %v", resp, err)
			}
		})
	}
}

func TestChoiceAndScoreValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		question systemone.Question
		answer   string
	}{
		"score object legend":        {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"0":{"impact":"low"}}}`},
		"score array legend":         {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"0":["low"]}}`},
		"score numeric legend":       {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"0":0}}`},
		"score missing legend":       {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"0":1}}`},
		"score wrong legend index":   {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"1":"low"}}`},
		"score null score":           {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":null,"confidence":1,"probabilities":{"0":1},"legend":{"0":"low"}}`},
		"score missing confidence":   {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"probabilities":{"0":1},"legend":{"0":"low"}}`},
		"score missing probability":  {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{},"legend":{"0":"low"}}`},
		"choice negative confidence": {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"a","confidence":-1,"probabilities":{"a":1}}`},
		"choice null choice":         {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":null,"confidence":1,"probabilities":{"a":1}}`},

		"choice missing confidence":         {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"a","probabilities":{"a":1}}`},
		"choice null probability":           {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":null}}`},
		"choice wrong selected label":       {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"b","confidence":1,"probabilities":{"a":1}}`},
		"choice wrong probability label":    {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"a","confidence":1,"probabilities":{"b":1}}`},
		"choice missing option probability": {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}, {Name: "b"}}}, `{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":1}}`},
		"choice extra option probability":   {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":0.5,"b":0.5}}`},
		"choice zero distribution":          {systemone.ChoiceQuestion{Instructions: "choose", Options: []systemone.ChoiceOption{{Name: "a"}}}, `{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":0}}`},
		"score missing score":               {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","confidence":1,"probabilities":{"0":1},"legend":{"0":"low"}}`},
		"score out of range":                {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":1,"confidence":1,"probabilities":{"0":1},"legend":{"0":"low"}}`},
		"score null legend":                 {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"0":1},"legend":{"0":null}}`},
		"score wrong index":                 {systemone.ScoreQuestion{Instructions: "rate", Criteria: []string{"low"}}, `{"type":"score","score":0,"confidence":1,"probabilities":{"1":1},"legend":{"0":"low"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"model":"test","answers":{"q":`+tc.answer+`}}`)
			}))
			defer srv.Close()
			client, _ := systemone.NewClient(srv.URL)
			req := binaryRequest()
			req.Questions["q"] = tc.question
			if resp, err := client.SystemOne(context.Background(), req); resp != nil || err == nil {
				t.Fatalf("malformed answer accepted: %v %v", resp, err)
			}
			if calls.Load() != 1 {
				t.Fatal("test did not reach response validation")
			}
		})
	}
}
