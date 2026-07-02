//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package trpcagent provides a remote runner for the tRPC-Agent API.
package trpcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/model"
	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	defaultBasePath = "/trpc-agent/v1/apps"
	headerAccept    = "Accept"
	headerContent   = "Content-Type"
	contentTypeJSON = "application/json"
)

// runner calls a tRPC-Agent API service.
type runner struct {
	appName    string
	target     string
	basePath   string
	httpClient *http.Client
	headers    http.Header
}

// New creates a runner that calls a tRPC-Agent API service.
func New(appName string, opts ...Option) (*runner, error) {
	options := newOptions(opts...)
	if appName == "" {
		return nil, errors.New("trpcagent runner: app name must not be empty")
	}
	if options.target == "" {
		return nil, errors.New("trpcagent runner: target must not be empty")
	}
	return &runner{
		appName:    appName,
		target:     options.target,
		basePath:   options.basePath,
		httpClient: options.httpClient,
		headers:    options.headers,
	}, nil
}

func (r *runner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	options := agent.NewRunOptions(runOpts...)
	if options.RequestID == "" {
		options.RequestID = uuid.NewString()
	}
	request := r.newRunRequest(userID, sessionID, message, options)
	response, err := r.postRun(ctx, request, options)
	if err != nil {
		return nil, err
	}
	if err := validateRunResponse(options, response); err != nil {
		return nil, err
	}
	events := r.eventsFromWireEvents(options, response)
	ch := make(chan *event.Event, len(events))
	for _, evt := range events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

// Describe fetches the remote app structure.
func (r *runner) Describe(ctx context.Context) (*astructure.Snapshot, error) {
	endpoint, err := r.describeEndpoint()
	if err != nil {
		return nil, fmt.Errorf("trpcagent runner: build describe endpoint: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("trpcagent runner: build describe request: %w", err)
	}
	httpRequest.Header.Set(headerAccept, contentTypeJSON)
	r.addHeaders(httpRequest)
	httpResponse, err := r.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("trpcagent runner: describe: %w", err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, r.httpStatusError("describe", httpResponse)
	}
	var response structureResponse
	if err := json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("trpcagent runner: decode describe response: %w", err)
	}
	if response.Structure == nil {
		return nil, errors.New("trpcagent runner: describe response structure is empty")
	}
	return response.Structure, nil
}

func (r *runner) newRunRequest(
	userID string,
	sessionID string,
	message model.Message,
	options agent.RunOptions,
) runRequest {
	request := runRequest{
		Session: session{
			UserID:    userID,
			SessionID: sessionID,
		},
		Input: message,
		RunOptions: runOptions{
			RequestID:             options.RequestID,
			ExecutionTraceEnabled: options.ExecutionTraceEnabled,
		},
	}
	if profile := profilecompiler.ProfileFromRunOptions(options); profile != nil {
		request.Profile = profile
	}
	return request
}

func (r *runner) postRun(
	ctx context.Context,
	request runRequest,
	options agent.RunOptions,
) (*runResponse, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(request); err != nil {
		return nil, fmt.Errorf("trpcagent runner: encode run request: %w", err)
	}
	endpoint, err := r.runEndpoint(options)
	if err != nil {
		return nil, fmt.Errorf("trpcagent runner: build run endpoint: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("trpcagent runner: build run request: %w", err)
	}
	httpRequest.Header.Set(headerContent, contentTypeJSON)
	httpRequest.Header.Set(headerAccept, contentTypeJSON)
	r.addHeaders(httpRequest)
	httpResponse, err := r.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("trpcagent runner: post run: %w", err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return nil, r.httpStatusError("run", httpResponse)
	}
	var response runResponse
	if err := json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("trpcagent runner: decode run response: %w", err)
	}
	return &response, nil
}

func (r *runner) runEndpoint(options agent.RunOptions) (string, error) {
	appName := r.appName
	if options.AppName != "" {
		appName = options.AppName
	}
	return url.JoinPath(r.target, r.basePath, appName, "runs")
}

func (r *runner) describeEndpoint() (string, error) {
	return url.JoinPath(r.target, r.basePath, r.appName, "structure")
}

func (r *runner) addHeaders(request *http.Request) {
	for key, values := range r.headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
}

func (r *runner) httpStatusError(operation string, response *http.Response) error {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("trpcagent runner: %s returned %s", operation, response.Status)
	}
	detail := string(body)
	if message := errorMessageFromBody(body); message != "" {
		detail = message
	}
	if detail == "" {
		return fmt.Errorf("trpcagent runner: %s returned %s", operation, response.Status)
	}
	return fmt.Errorf("trpcagent runner: %s returned %s: %s", operation, response.Status, detail)
}

func errorMessageFromBody(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	message, ok := payload["error"].(string)
	if !ok {
		return ""
	}
	return message
}

func validateRunResponse(options agent.RunOptions, response *runResponse) error {
	if len(response.Events) == 0 {
		return errors.New("trpcagent runner: run response events are empty")
	}
	for i := range response.Events {
		requestID := response.Events[i].RequestID
		if requestID == "" {
			return fmt.Errorf("trpcagent runner: event %d request id is empty", i)
		}
		if requestID != options.RequestID {
			return fmt.Errorf("trpcagent runner: event %d request id %q does not match run request id %q", i, requestID, options.RequestID)
		}
	}
	if !response.Events[len(response.Events)-1].IsRunnerCompletion() {
		return errors.New("trpcagent runner: run response last event is not runner completion")
	}
	return nil
}

func (r *runner) eventsFromWireEvents(
	options agent.RunOptions,
	response *runResponse,
) []*event.Event {
	events := make([]*event.Event, 0, len(response.Events))
	for i := range response.Events {
		evt := response.Events[i]
		if evt.IsRunnerCompletion() && evt.ExecutionTrace == nil {
			evt.ExecutionTrace = response.ExecutionTrace
		}
		events = append(events, &evt)
	}
	return events
}

func (r *runner) Close() error {
	return nil
}

var _ rootrunner.Runner = (*runner)(nil)
