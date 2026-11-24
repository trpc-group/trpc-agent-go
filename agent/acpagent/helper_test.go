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
			response := helperInitializeResponse()
			if mode == "bad_protocol" {
				response.ProtocolVersion = supportedProtocolVersion + 1
			}
			writeHelperResult(request.ID, response)
			initialized = true
			switch mode {
			case "exit_after_init":
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
			writeHelperResult(request.ID, acpsdk.NewSessionResponse{
				SessionId: acpsdk.SessionId(fmt.Sprintf("helper-session-%d", sessionCount)),
			})
		case acpsdk.AgentMethodSessionClose:
			if !initialized {
				writeHelperError(request.ID, acpsdk.NewInvalidRequest("not initialized"))
				continue
			}
			if mode == "close_error" {
				writeHelperError(request.ID, acpsdk.NewInternalError("session close called"))
				continue
			}
			writeHelperResult(request.ID, acpsdk.CloseSessionResponse{})
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
	return acpsdk.InitializeResponse{
		ProtocolVersion: supportedProtocolVersion,
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: acpsdk.SessionCapabilities{
				Close: &acpsdk.SessionCloseCapabilities{},
			},
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
