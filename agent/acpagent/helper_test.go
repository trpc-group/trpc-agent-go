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

	var request struct {
		ID     json.RawMessage          `json:"id"`
		Method string                   `json:"method"`
		Params acpsdk.InitializeRequest `json:"params"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		writeHelperError(request.ID, acpsdk.NewParseError(err.Error()))
		os.Exit(1)
	}
	if request.Method != acpsdk.AgentMethodInitialize {
		writeHelperError(request.ID, acpsdk.NewMethodNotFound(request.Method))
		os.Exit(1)
	}
	if err := validateHelperInitializeRequest(request.Params); err != nil {
		writeHelperError(request.ID, acpsdk.NewInvalidParams(map[string]any{
			"error": err.Error(),
		}))
		os.Exit(1)
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
	switch mode {
	case "exit_after_init":
		os.Exit(7)
	case "hang":
		time.Sleep(time.Hour)
		os.Exit(0)
	default:
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
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
