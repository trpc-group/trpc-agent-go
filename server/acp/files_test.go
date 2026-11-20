//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestClientFileToolsRequireEnabledOptionsAndCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		options    []Option
		readCap    bool
		writeCap   bool
		wantTools  []string
		wantWarned int
	}{
		{
			name:      "default disabled",
			readCap:   true,
			writeCap:  true,
			wantTools: nil,
		},
		{
			name:      "read only",
			options:   []Option{WithClientFileReadEnabled(true)},
			readCap:   true,
			writeCap:  true,
			wantTools: []string{clientReadTextFileToolName},
		},
		{
			name:      "write only",
			options:   []Option{WithClientFileWriteEnabled(true)},
			readCap:   true,
			writeCap:  true,
			wantTools: []string{clientWriteTextFileToolName},
		},
		{
			name: "both",
			options: []Option{
				WithClientFileReadEnabled(true),
				WithClientFileWriteEnabled(true),
			},
			readCap:   true,
			writeCap:  true,
			wantTools: []string{clientReadTextFileToolName, clientWriteTextFileToolName},
		},
		{
			name: "enabled without client capabilities",
			options: []Option{
				WithClientFileReadEnabled(true),
				WithClientFileWriteEnabled(true),
			},
			wantWarned: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var warnings int
			originalWarnContext := log.WarnContext
			log.WarnContext = func(context.Context, ...any) {
				warnings++
			}
			t.Cleanup(func() {
				log.WarnContext = originalWarnContext
			})

			_, _, session := newClientFileTestAgent(
				t,
				test.options,
				test.readCap,
				test.writeCap,
			)
			assert.Equal(t, test.wantWarned, warnings)
			assert.ElementsMatch(t, test.wantTools, toolNames(session.tools))
		})
	}
}

func TestClientFileToolsAreInjectedPerPrompt(t *testing.T) {
	run := &testRunner{}
	protocolAgent, _, session := newClientFileTestAgentWithRunner(
		t,
		run,
		[]Option{
			WithClientFileReadEnabled(true),
			WithClientFileWriteEnabled(true),
		},
		true,
		true,
	)
	var injected [][]string
	var injectedReadTool tool.Tool
	readTool := findTool(session.tools, clientReadTextFileToolName)
	require.NotNil(t, readTool)
	run.run = func(context.Context) <-chan *event.Event {
		call := run.lastCall()
		injected = append(injected, toolNames(call.options.AdditionalTools))
		injectedReadTool = findTool(call.options.AdditionalTools, clientReadTextFileToolName)
		events := make(chan *event.Event)
		close(events)
		return events
	}

	for index := 0; index < 2; index++ {
		_, err := protocolAgent.Prompt(
			context.Background(),
			acpsdk.PromptRequest{
				SessionId: acpsdk.SessionId(session.id),
				Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("read a file")},
			},
		)
		require.NoError(t, err)
	}
	assert.Equal(t, [][]string{
		{clientReadTextFileToolName, clientWriteTextFileToolName},
		{clientReadTextFileToolName, clientWriteTextFileToolName},
	}, injected)
	assert.Same(t, readTool, injectedReadTool)
}

func TestClientReadTextFileTool(t *testing.T) {
	_, connection, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileReadEnabled(true)},
		true,
		true,
	)
	connection.readResponse = acpsdk.ReadTextFileResponse{Content: "file content"}
	readTool := findTool(session.tools, clientReadTextFileToolName)
	require.NotNil(t, readTool)

	line := 2
	limit := 3
	result, err := readTool.(tool.CallableTool).Call(
		context.Background(),
		[]byte(`{"path":"src/../README.md","line":2,"limit":3}`),
	)
	require.NoError(t, err)
	assert.Equal(t, "file content", result)
	require.Len(t, connection.readRequests(), 1)
	request := connection.readRequests()[0]
	assert.Equal(t, filepath.Join("/workspace", "README.md"), request.Path)
	assert.Equal(t, &line, request.Line)
	assert.Equal(t, &limit, request.Limit)
	assert.Equal(t, acpsdk.SessionId(session.id), request.SessionId)

	connection.readResponse = acpsdk.ReadTextFileResponse{}
	result, err = readTool.(tool.CallableTool).Call(
		context.Background(),
		[]byte(`{"path":"empty.txt"}`),
	)
	require.NoError(t, err)
	assert.Empty(t, result)
	require.Len(t, connection.readRequests(), 2)
	defaults := connection.readRequests()[1]
	require.NotNil(t, defaults.Line)
	require.NotNil(t, defaults.Limit)
	assert.Equal(t, defaultClientFileReadLine, *defaults.Line)
	assert.Equal(t, defaultClientFileReadLimit, *defaults.Limit)

	_, err = readTool.(tool.CallableTool).Call(
		context.Background(),
		[]byte(`{"path":"late.txt","line":5000000}`),
	)
	require.NoError(t, err)
	require.Len(t, connection.readRequests(), 3)
	largeLine := connection.readRequests()[2]
	require.NotNil(t, largeLine.Line)
	assert.Equal(t, 5_000_000, *largeLine.Line)
}

func TestClientReadTextFileToolValidation(t *testing.T) {
	_, connection, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileReadEnabled(true)},
		true,
		false,
	)
	readTool := findTool(session.tools, clientReadTextFileToolName)
	require.NotNil(t, readTool)
	tests := []struct {
		name      string
		arguments string
		wantError string
	}{
		{name: "empty path", arguments: `{"path":""}`, wantError: "path is required"},
		{name: "zero line", arguments: `{"path":"README.md","line":0}`, wantError: "line must be greater than zero"},
		{name: "zero limit", arguments: `{"path":"README.md","limit":0}`, wantError: "limit must be greater than zero"},
		{
			name:      "limit exceeds maximum",
			arguments: fmt.Sprintf(`{"path":"README.md","limit":%d}`, maxClientFileReadLimit+1),
			wantError: fmt.Sprintf("limit must be at most %d", maxClientFileReadLimit),
		},
		{name: "parent traversal", arguments: `{"path":"../secret.txt"}`, wantError: "outside the session workspace"},
		{
			name:      "absolute outside",
			arguments: `{"path":"/workspace-other/secret.txt"}`,
			wantError: "outside the session workspace",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readTool.(tool.CallableTool).Call(
				context.Background(),
				[]byte(test.arguments),
			)
			require.ErrorContains(t, err, test.wantError)
		})
	}
	assert.Empty(t, connection.readRequests())
}

func TestClientReadTextFileInputSchema(t *testing.T) {
	_, _, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileReadEnabled(true)},
		true,
		false,
	)
	readTool := findTool(session.tools, clientReadTextFileToolName)
	require.NotNil(t, readTool)
	schema := readTool.Declaration().InputSchema
	require.NotNil(t, schema)
	require.Contains(t, schema.Properties, "line")
	require.Contains(t, schema.Properties, "limit")
	assert.Equal(t, defaultClientFileReadLine, schema.Properties["line"].Default)
	assert.Equal(t, defaultClientFileReadLimit, schema.Properties["limit"].Default)
	assert.Contains(
		t,
		schema.Properties["line"].Description,
		fmt.Sprintf("Default: %d", defaultClientFileReadLine),
	)
	assert.Contains(
		t,
		schema.Properties["limit"].Description,
		fmt.Sprintf("Maximum: %d", maxClientFileReadLimit),
	)
	assert.Contains(
		t,
		schema.Properties["limit"].Description,
		fmt.Sprintf("Default: %d", defaultClientFileReadLimit),
	)
}

func TestClientWriteTextFileTool(t *testing.T) {
	_, connection, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileWriteEnabled(true)},
		false,
		true,
	)
	writeTool := findTool(session.tools, clientWriteTextFileToolName)
	require.NotNil(t, writeTool)

	result, err := writeTool.(tool.CallableTool).Call(
		context.Background(),
		[]byte(`{"path":"docs/new.txt","content":"hello"}`),
	)
	require.NoError(t, err)
	assert.Equal(t, "file written successfully", result)
	require.Len(t, connection.writeRequests(), 1)
	request := connection.writeRequests()[0]
	assert.Equal(t, filepath.Join("/workspace", "docs/new.txt"), request.Path)
	assert.Equal(t, "hello", request.Content)
	assert.Equal(t, acpsdk.SessionId(session.id), request.SessionId)
	assert.Equal(t, tool.ToolMetadata{
		Destructive:     true,
		ConcurrencySafe: false,
	}, tool.MetadataOf(writeTool))
}

func TestClientWriteTextFilePermission(t *testing.T) {
	_, connection, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileWriteEnabled(true)},
		false,
		true,
	)
	writeTool := findTool(session.tools, clientWriteTextFileToolName)
	require.NotNil(t, writeTool)
	checker := writeTool.(tool.PermissionChecker)
	arguments := []byte(`{"path":"docs/new.txt","content":"hello"}`)

	connection.queuePermission(acpsdk.PermissionOptionKindAllowOnce)
	decision, err := checker.CheckPermission(context.Background(), &tool.PermissionRequest{
		ToolCallID: "call-1",
		Arguments:  arguments,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
	connection.queuePermission(acpsdk.PermissionOptionKindAllowOnce)
	_, err = checker.CheckPermission(context.Background(), &tool.PermissionRequest{
		ToolCallID: "call-2",
		Arguments:  arguments,
	})
	require.NoError(t, err)
	assert.Len(t, connection.permissionRequests(), 2)
	request := connection.permissionRequests()[0]
	assert.Equal(t, acpsdk.ToolCallId("call-1"), request.ToolCall.ToolCallId)
	assert.Equal(t, acpsdk.ToolKindEdit, *request.ToolCall.Kind)
	assert.Equal(t, filepath.Join("/workspace", "docs/new.txt"), request.ToolCall.Locations[0].Path)
	assert.Len(t, request.Options, 4)
	var optionKinds []acpsdk.PermissionOptionKind
	for _, option := range request.Options {
		optionKinds = append(optionKinds, option.Kind)
	}
	assert.ElementsMatch(t, []acpsdk.PermissionOptionKind{
		acpsdk.PermissionOptionKindAllowOnce,
		acpsdk.PermissionOptionKindAllowAlways,
		acpsdk.PermissionOptionKindRejectOnce,
		acpsdk.PermissionOptionKindRejectAlways,
	}, optionKinds)

	connection.queuePermission(acpsdk.PermissionOptionKindRejectOnce)
	decision, err = checker.CheckPermission(context.Background(), &tool.PermissionRequest{
		ToolCallID: "call-3",
		Arguments:  arguments,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)

	connection.queuePermission(acpsdk.PermissionOptionKindAllowAlways)
	_, err = checker.CheckPermission(context.Background(), &tool.PermissionRequest{
		ToolCallID: "call-4",
		Arguments:  arguments,
	})
	require.NoError(t, err)
	decision, err = checker.CheckPermission(context.Background(), &tool.PermissionRequest{
		ToolCallID: "call-5",
		Arguments:  arguments,
	})
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionAllow, decision.Action)
	assert.Len(t, connection.permissionRequests(), 4)
}

func TestClientWriteTextFilePermissionCancelled(t *testing.T) {
	_, connection, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileWriteEnabled(true)},
		false,
		true,
	)
	writeTool := findTool(session.tools, clientWriteTextFileToolName)
	require.NotNil(t, writeTool)
	connection.queuePermission("")
	cancelled := false
	session.mu.Lock()
	session.cancel = func() {
		cancelled = true
	}
	session.mu.Unlock()

	_, err := writeTool.(tool.PermissionChecker).CheckPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolCallID: "call-cancel",
			Arguments:  []byte(`{"path":"README.md","content":"hello"}`),
		},
	)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, cancelled)
}

func TestClientWriteTextFilePermissionRejectsPathBeforeRequest(t *testing.T) {
	_, connection, session := newClientFileTestAgent(
		t,
		[]Option{WithClientFileWriteEnabled(true)},
		false,
		true,
	)
	writeTool := findTool(session.tools, clientWriteTextFileToolName)
	require.NotNil(t, writeTool)

	decision, err := writeTool.(tool.PermissionChecker).CheckPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolCallID: "call-outside",
			Arguments:  []byte(`{"path":"../secret.txt","content":"hello"}`),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, tool.PermissionActionDeny, decision.Action)
	assert.Contains(t, decision.Reason, "outside the session workspace")
	assert.Empty(t, connection.permissionRequests())
}

func TestClientWritePermissionCacheIsScopedToSession(t *testing.T) {
	protocolAgent, connection, firstSession := newClientFileTestAgent(
		t,
		[]Option{WithClientFileWriteEnabled(true)},
		false,
		true,
	)
	secondSession, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)
	secondState := protocolAgent.sessions[string(secondSession.SessionId)]
	require.NotNil(t, secondState)
	firstTool := findTool(
		firstSession.tools,
		clientWriteTextFileToolName,
	)
	secondTool := findTool(
		secondState.tools,
		clientWriteTextFileToolName,
	)
	require.NotNil(t, firstTool)
	require.NotNil(t, secondTool)
	arguments := []byte(`{"path":"README.md","content":"hello"}`)

	connection.queuePermission(acpsdk.PermissionOptionKindAllowAlways)
	_, err = firstTool.(tool.PermissionChecker).CheckPermission(
		context.Background(),
		&tool.PermissionRequest{ToolCallID: "first", Arguments: arguments},
	)
	require.NoError(t, err)
	_, err = firstTool.(tool.PermissionChecker).CheckPermission(
		context.Background(),
		&tool.PermissionRequest{ToolCallID: "first-again", Arguments: arguments},
	)
	require.NoError(t, err)
	assert.Len(t, connection.permissionRequests(), 1)

	connection.queuePermission(acpsdk.PermissionOptionKindAllowOnce)
	_, err = secondTool.(tool.PermissionChecker).CheckPermission(
		context.Background(),
		&tool.PermissionRequest{ToolCallID: "second", Arguments: arguments},
	)
	require.NoError(t, err)
	assert.Len(t, connection.permissionRequests(), 2)
}

func TestResolveSessionPath(t *testing.T) {
	tests := []struct {
		name    string
		cwd     string
		path    string
		want    string
		wantErr string
	}{
		{name: "relative", cwd: "/workspace", path: "src/main.go", want: "/workspace/src/main.go"},
		{name: "absolute inside", cwd: "/workspace", path: "/workspace/README.md", want: "/workspace/README.md"},
		{name: "parent traversal", cwd: "/workspace", path: "../secret", wantErr: "outside the session workspace"},
		{name: "component prefix", cwd: "/workspace", path: "/workspace-other/secret", wantErr: "outside the session workspace"},
		{name: "empty", cwd: "/workspace", path: "", wantErr: "path is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveSessionPath(test.cwd, test.path)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

type fileClientConnection struct {
	mu                  sync.Mutex
	reads               []acpsdk.ReadTextFileRequest
	writes              []acpsdk.WriteTextFileRequest
	permissions         []acpsdk.RequestPermissionRequest
	permissionResponses []acpsdk.RequestPermissionOutcome
	readResponse        acpsdk.ReadTextFileResponse
	writeResponse       acpsdk.WriteTextFileResponse
}

func (*fileClientConnection) SessionUpdate(context.Context, acpsdk.SessionNotification) error {
	return nil
}

func (c *fileClientConnection) ReadTextFile(
	_ context.Context,
	request acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads = append(c.reads, request)
	return c.readResponse, nil
}

func (c *fileClientConnection) WriteTextFile(
	_ context.Context,
	request acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, request)
	return c.writeResponse, nil
}

func (c *fileClientConnection) RequestPermission(
	_ context.Context,
	request acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissions = append(c.permissions, request)
	if len(c.permissionResponses) == 0 {
		return acpsdk.RequestPermissionResponse{}, errors.New("permission response not configured")
	}
	outcome := c.permissionResponses[0]
	c.permissionResponses = c.permissionResponses[1:]
	return acpsdk.RequestPermissionResponse{Outcome: outcome}, nil
}

func (c *fileClientConnection) queuePermission(kind acpsdk.PermissionOptionKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if kind == "" {
		c.permissionResponses = append(c.permissionResponses, acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{},
		})
		return
	}
	c.permissionResponses = append(c.permissionResponses, acpsdk.RequestPermissionOutcome{
		Selected: &acpsdk.RequestPermissionOutcomeSelected{
			OptionId: acpsdk.PermissionOptionId(kind),
		},
	})
}

func (c *fileClientConnection) readRequests() []acpsdk.ReadTextFileRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acpsdk.ReadTextFileRequest(nil), c.reads...)
}

func (c *fileClientConnection) writeRequests() []acpsdk.WriteTextFileRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acpsdk.WriteTextFileRequest(nil), c.writes...)
}

func (c *fileClientConnection) permissionRequests() []acpsdk.RequestPermissionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acpsdk.RequestPermissionRequest(nil), c.permissions...)
}

func newClientFileTestAgent(
	t *testing.T,
	options []Option,
	readCapability bool,
	writeCapability bool,
) (*protocolAgent, *fileClientConnection, *sessionState) {
	t.Helper()
	return newClientFileTestAgentWithRunner(
		t,
		&testRunner{},
		options,
		readCapability,
		writeCapability,
	)
}

func newClientFileTestAgentWithRunner(
	t *testing.T,
	run *testRunner,
	options []Option,
	readCapability bool,
	writeCapability bool,
) (*protocolAgent, *fileClientConnection, *sessionState) {
	t.Helper()
	nextSessionID := 0
	allOptions := append([]Option(nil), options...)
	allOptions = append(allOptions, WithSessionIDGenerator(func() string {
		nextSessionID++
		return fmt.Sprintf("session-files-%d", nextSessionID)
	}))
	server, err := New(run, allOptions...)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	connection := &fileClientConnection{}
	protocolAgent.connection = connection
	_, err = protocolAgent.Initialize(context.Background(), acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{
				ReadTextFile:  readCapability,
				WriteTextFile: writeCapability,
			},
		},
	})
	require.NoError(t, err)
	session, err := protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.NoError(t, err)
	sessionState := protocolAgent.sessions[string(session.SessionId)]
	require.NotNil(t, sessionState)
	return protocolAgent, connection, sessionState
}
