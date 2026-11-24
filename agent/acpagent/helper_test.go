//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "acp helper stderr")
	mode := os.Getenv("ACP_HELPER_MODE")
	if mode == "hang_initialize" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}

	decoder := json.NewDecoder(os.Stdin)
	initialized := false
	authenticated := false
	authCalls := 0
	sessionCount := 0
	sessions := make(map[acpsdk.SessionId]struct{})
	var pendingCancelID json.RawMessage
	var pendingCancelSession acpsdk.SessionId
	for {
		var request helperRequest
		if err := decoder.Decode(&request); err != nil {
			if err == io.EOF {
				os.Exit(0)
			}
			writeHelperError(request.ID, acpsdk.NewParseError(err.Error()))
			os.Exit(1)
		}
		switch request.Method {
		case acpsdk.AgentMethodInitialize:
			if initialized {
				writeHelperError(request.ID, acpsdk.NewInternalError("already initialized"))
				continue
			}
			var params acpsdk.InitializeRequest
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(err.Error()))
				continue
			}
			if err := validateHelperInitializeRequest(params); err != nil {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(map[string]any{
					"error": err.Error(),
				}))
				continue
			}
			if mode == "initialize_error" {
				writeHelperError(request.ID, acpsdk.NewInternalError("initialize failed"))
				os.Exit(1)
			}
			response := helperInitializeResponseForMode(mode)
			if mode == "bad_protocol" {
				response.ProtocolVersion = supportedProtocolVersion + 1
			}
			writeHelperResult(request.ID, response)
			initialized = true
			switch mode {
			case "exit_after_init":
				time.Sleep(100 * time.Millisecond)
				os.Exit(7)
			case "hang":
				time.Sleep(time.Hour)
				os.Exit(0)
			}
		case acpsdk.AgentMethodSessionNew:
			if !initialized {
				writeHelperError(request.ID, acpsdk.NewInvalidRequest("not initialized"))
				continue
			}
			if mode == "always_auth_required" || !authenticated {
				writeHelperError(request.ID, acpsdk.NewAuthRequired(helperAuthData()))
				continue
			}
			sessionCount++
			sessionID := acpsdk.SessionId(fmt.Sprintf("helper-session-%d", sessionCount))
			sessions[sessionID] = struct{}{}
			writeHelperResult(request.ID, acpsdk.NewSessionResponse{SessionId: sessionID})
		case acpsdk.AgentMethodSessionClose:
			if !initialized {
				writeHelperError(request.ID, acpsdk.NewInvalidRequest("not initialized"))
				continue
			}
			if mode == "close_error" {
				writeHelperError(request.ID, acpsdk.NewInternalError("session close called"))
				continue
			}
			var params acpsdk.CloseSessionRequest
			_ = json.Unmarshal(request.Params, &params)
			delete(sessions, params.SessionId)
			writeHelperResult(request.ID, acpsdk.CloseSessionResponse{})
		case acpsdk.AgentMethodSessionPrompt:
			var params acpsdk.PromptRequest
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(err.Error()))
				continue
			}
			if _, ok := sessions[params.SessionId]; !ok {
				writeHelperError(request.ID, &acpsdk.RequestError{
					Code:    -32002,
					Message: "Resource not found",
					Data:    map[string]any{"sessionId": params.SessionId},
				})
				continue
			}
			if mode == "prompt_process_exit" {
				writeHelperNotification(params.SessionId, acpsdk.UpdateAgentMessageText("partial"))
				os.Exit(7)
			}
			if mode == "prompt_cancel" {
				pendingCancelID = append(json.RawMessage(nil), request.ID...)
				pendingCancelSession = params.SessionId
				continue
			}
			helperWritePromptUpdates(params.SessionId, mode)
			writeHelperResult(request.ID, acpsdk.PromptResponse{
				StopReason: acpsdk.StopReasonEndTurn,
				Usage: &acpsdk.Usage{
					InputTokens:  3,
					OutputTokens: 5,
					TotalTokens:  8,
				},
			})
		case acpsdk.AgentMethodSessionCancel:
			var params acpsdk.CancelNotification
			_ = json.Unmarshal(request.Params, &params)
			if len(pendingCancelID) > 0 && params.SessionId == pendingCancelSession {
				writeHelperResult(pendingCancelID, acpsdk.PromptResponse{
					StopReason: acpsdk.StopReasonCancelled,
				})
				pendingCancelID = nil
				pendingCancelSession = ""
			}
			continue
		case acpsdk.AgentMethodSessionResume:
			if !strings.HasPrefix(mode, "resume_") {
				writeHelperError(request.ID, acpsdk.NewMethodNotFound(request.Method))
				continue
			}
			var params acpsdk.ResumeSessionRequest
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(err.Error()))
				continue
			}
			if mode == "resume_unknown" {
				writeHelperError(request.ID, helperUnknownSessionError(params.SessionId))
				continue
			}
			if mode == "resume_error" {
				writeHelperError(request.ID, acpsdk.NewInternalError("resume failed"))
				continue
			}
			sessions[params.SessionId] = struct{}{}
			writeHelperResult(request.ID, acpsdk.ResumeSessionResponse{})
		case acpsdk.AgentMethodSessionLoad:
			if mode == "resume_both_success" {
				writeHelperError(request.ID, acpsdk.NewInternalError("load must not be called"))
				continue
			}
			if !strings.HasPrefix(mode, "load_") && mode != "prompt_process_exit" {
				writeHelperError(request.ID, acpsdk.NewMethodNotFound(request.Method))
				continue
			}
			var params acpsdk.LoadSessionRequest
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(err.Error()))
				continue
			}
			if mode == "load_unknown" {
				writeHelperError(request.ID, helperUnknownSessionError(params.SessionId))
				continue
			}
			if mode == "load_error" {
				writeHelperError(request.ID, acpsdk.NewInternalError("load failed"))
				continue
			}
			sessions[params.SessionId] = struct{}{}
			writeHelperResult(request.ID, acpsdk.LoadSessionResponse{})
		case acpsdk.AgentMethodAuthenticate:
			authCalls++
			if authCalls > 1 {
				writeHelperError(request.ID, acpsdk.NewInternalError("authenticate called more than once"))
				continue
			}
			var params acpsdk.AuthenticateRequest
			if err := json.Unmarshal(request.Params, &params); err != nil {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(err.Error()))
				continue
			}
			if params.MethodId != "agent-login" || params.Meta["tenant"] != "acme" {
				writeHelperError(request.ID, acpsdk.NewInvalidParams(map[string]any{
					"methodId": params.MethodId,
					"meta":     params.Meta,
				}))
				continue
			}
			if mode == "authenticate_error" {
				writeHelperError(request.ID, acpsdk.NewInternalError("authenticate failed"))
				continue
			}
			authenticated = true
			writeHelperResult(request.ID, acpsdk.AuthenticateResponse{})
		default:
			writeHelperError(request.ID, acpsdk.NewMethodNotFound(request.Method))
		}
	}
}

type helperRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func validateHelperInitializeRequest(request acpsdk.InitializeRequest) error {
	if request.ProtocolVersion != supportedProtocolVersion {
		return fmt.Errorf("protocol version = %d, want %d", request.ProtocolVersion, supportedProtocolVersion)
	}
	expectedTerminalAuth := os.Getenv("ACP_HELPER_EXPECT_TERMINAL_AUTH") == "1"
	if request.ClientCapabilities.Auth.Terminal != expectedTerminalAuth {
		return fmt.Errorf(
			"auth.terminal capability = %t, want %t",
			request.ClientCapabilities.Auth.Terminal,
			expectedTerminalAuth,
		)
	}
	if request.ClientCapabilities.Fs.ReadTextFile || request.ClientCapabilities.Fs.WriteTextFile {
		return fmt.Errorf("file system capability was advertised")
	}
	if request.ClientCapabilities.Terminal {
		return fmt.Errorf("terminal capability was advertised")
	}
	return nil
}

func helperInitializeResponse() acpsdk.InitializeResponse {
	return helperInitializeResponseForMode("")
}

func helperInitializeResponseForMode(mode string) acpsdk.InitializeResponse {
	sessionCapabilities := acpsdk.SessionCapabilities{
		Close: &acpsdk.SessionCloseCapabilities{},
	}
	loadSession := true
	if strings.HasPrefix(mode, "resume_") {
		loadSession = false
		sessionCapabilities.Resume = &acpsdk.SessionResumeCapabilities{}
	}
	if mode == "resume_both_success" {
		loadSession = true
		sessionCapabilities.Resume = &acpsdk.SessionResumeCapabilities{}
	}
	if mode == "new_only" {
		loadSession = false
	}
	return acpsdk.InitializeResponse{
		ProtocolVersion: supportedProtocolVersion,
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession:         loadSession,
			SessionCapabilities: sessionCapabilities,
		},
		AgentInfo: &acpsdk.Implementation{
			Name:    "helper-agent",
			Version: "1.2.3",
		},
		AuthMethods: []acpsdk.AuthMethod{
			{
				Agent: &acpsdk.AuthMethodAgent{
					Id:   "agent-login",
					Name: "Agent login",
				},
			},
			{
				Terminal: &acpsdk.AuthMethodTerminalInline{
					Id:   "terminal-login",
					Name: "Terminal login",
					Type: "terminal",
				},
			},
		},
	}
}

func helperUnknownSessionError(sessionID acpsdk.SessionId) *acpsdk.RequestError {
	return &acpsdk.RequestError{
		Code:    -32002,
		Message: "Resource not found",
		Data:    map[string]any{"sessionId": sessionID},
	}
}

func writeHelperResult(id json.RawMessage, result any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeHelperError(id json.RawMessage, requestErr *acpsdk.RequestError) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   requestErr,
	})
}

func helperAuthData() map[string]any {
	return map[string]any{
		"loginUrl": "https://example.test/login",
	}
}

func helperWritePromptUpdates(sessionID acpsdk.SessionId, mode string) {
	if mode == "prompt_overflow" {
		for i := 0; i < defaultSessionUpdateQueueSize*2; i++ {
			writeHelperNotification(sessionID, acpsdk.UpdateAgentMessageText("x"))
		}
		return
	}
	if mode != "prompt_stream" {
		return
	}
	writeHelperNotification(sessionID, acpsdk.UpdateAgentThoughtText("thinking"))
	writeHelperNotification(sessionID, acpsdk.UpdateAgentMessageText("hello"))
	writeHelperNotification(
		sessionID,
		acpsdk.StartToolCall(
			"tool-1",
			"Read file",
			acpsdk.WithStartKind(acpsdk.ToolKindRead),
			acpsdk.WithStartRawInput(map[string]any{"path": "file.txt"}),
		),
	)
	writeHelperNotification(sessionID, acpsdk.UpdatePlan(acpsdk.PlanEntry{
		Content:  "Inspect file",
		Priority: acpsdk.PlanEntryPriorityHigh,
		Status:   acpsdk.PlanEntryStatusInProgress,
	}))
	writeHelperNotification(sessionID, acpsdk.UpdateAgentMessageText(" world"))
}

func writeHelperNotification(sessionID acpsdk.SessionId, update acpsdk.SessionUpdate) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  acpsdk.ClientMethodSessionUpdate,
		"params": acpsdk.SessionNotification{
			SessionId: sessionID,
			Update:    update,
		},
	})
}
