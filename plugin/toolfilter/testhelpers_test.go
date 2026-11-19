//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubTool carries a tool declaration for tests.
type stubTool struct {
	name        string
	description string
}

// Declaration implements tool.Tool.
func (t *stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name, Description: t.description}
}

// newToolMap builds a model request tool map from stub tools.
func newToolMap(tools ...*stubTool) map[string]tool.Tool {
	m := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		m[t.name] = t
	}
	return m
}

// newUserRequest builds a one-user-message request with the given tools.
func newUserRequest(content string, tools map[string]tool.Tool) *model.Request {
	return &model.Request{
		Messages: []model.Message{model.NewUserMessage(content)},
		Tools:    tools,
	}
}

// decisionRecorder records the decision requests seen by a test server.
type decisionRecorder struct {
	mu        sync.Mutex
	count     int
	questions []map[string]string // per request: question ID -> wire type
	states    []any
}

func (r *decisionRecorder) lastState() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.states) == 0 {
		return nil
	}
	return r.states[len(r.states)-1]
}

// requests reports how many decision requests were received.
func (r *decisionRecorder) requests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// lastQuestions reports the question IDs of the most recent request.
func (r *decisionRecorder) lastQuestions() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.questions) == 0 {
		return nil
	}
	return r.questions[len(r.questions)-1]
}

// decisionServer serves scripted System One decision responses. respond
// receives the requested questions (ID -> wire type) and returns the
// response body; status overrides the HTTP status code.
func decisionServer(
	t *testing.T,
	respond func(questions map[string]string) string,
	status int,
) (*httptest.Server, *decisionRecorder) {
	t.Helper()
	rec := &decisionRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var wire struct {
			State     any `json:"state"`
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		questions := make(map[string]string, len(wire.Questions))
		for id, q := range wire.Questions {
			questions[id] = q.Type
		}
		rec.mu.Lock()
		rec.count++
		rec.questions = append(rec.questions, questions)
		rec.states = append(rec.states, wire.State)
		rec.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respond(questions))
	}))
	t.Cleanup(server.Close)
	return server, rec
}

// binaryResponder answers every asked question with override or fallback
// probabilities.
func binaryResponder(
	overrides map[string]float64,
	fallback float64,
) func(map[string]string) string {
	return func(questions map[string]string) string {
		answers := make(map[string]float64, len(questions))
		for id := range questions {
			p := fallback
			if value, ok := overrides[id]; ok {
				p = value
			}
			answers[id] = p
		}
		return binaryResponse(answers)
	}
}

// binaryResponse builds a wire response with one binary answer per entry.
func binaryResponse(answers map[string]float64) string {
	var b strings.Builder
	b.WriteString(`{"model":"test-model","answers":{`)
	writeAnswerFields(&b, answers, "noul")
	b.WriteString("}}")
	return b.String()
}

// choiceResponse builds a wire response with one choice answer.
func choiceResponse(
	id, choice string,
	probabilities map[string]float64,
	confidence float64,
) string {
	var b strings.Builder
	b.WriteString(`{"model":"test-model","answers":{`)
	fmt.Fprintf(&b, `%q:{"type":"choice","choice":%q,"confidence":%v,"probabilities":{`,
		id, choice, confidence)
	first := true
	for name, p := range probabilities {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `%q:%v`, name, p)
	}
	b.WriteString("}}}")
	b.WriteString("}")
	return b.String()
}

func writeAnswerFields(b *strings.Builder, answers map[string]float64, kind string) {
	first := true
	for id, p := range answers {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(b, `%q:{"type":%q,"noul":%v}`, id, kind, p)
	}
}

// fakeSkillRepo is a static skill repository for tests.
type fakeSkillRepo struct {
	summaries []skill.Summary
}

// Summaries implements skill.Repository.
func (r *fakeSkillRepo) Summaries() []skill.Summary { return r.summaries }

// Get implements skill.Repository.
func (r *fakeSkillRepo) Get(name string) (*skill.Skill, error) {
	return nil, fmt.Errorf("skill %q not found", name)
}

// Path implements skill.Repository.
func (r *fakeSkillRepo) Path(name string) (string, error) {
	return "", fmt.Errorf("skill %q not found", name)
}

// stubModel is a routing target for ModelRouter tests.
type stubModel struct {
	name string
}

// GenerateContent implements model.Model.
func (m *stubModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	return nil, fmt.Errorf("stubModel is not callable")
}

// Info implements model.Model.
func (m *stubModel) Info() model.Info {
	return model.Info{Name: m.name}
}
