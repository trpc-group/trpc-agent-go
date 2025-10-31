//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jupyter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// ConnectionInfo ...
type ConnectionInfo struct {
	Host       string
	Port       int
	Token      string
	KernelName string
}

// Client ...
type Client struct {
	connectionInfo ConnectionInfo
	baseURL        string
	httpClient     *http.Client
	kernelID       string
	ws             *websocket.Conn
	sessionID      string
}

// kernelSpec ...
type kernelSpec struct {
	Argv          []string `json:"argv"`
	DisplayName   string   `json:"display_name"`
	Language      string   `json:"language"`
	InterruptMode string   `json:"interrupt_mode"`
}

// kernelInfo ...
type kernelInfo struct {
	Name string     `json:"name"`
	Spec kernelSpec `json:"spec"`
}

// kernelSpecsResponse ...
type kernelSpecsResponse struct {
	KernelSpces map[string]kernelInfo `json:"kernelspecs"`
}

// executionMessage ...
type executionMessage struct {
	Header struct {
		MsgType string `json:"msg_type"`
	} `json:"header"`
	Content      map[string]interface{} `json:"content"`
	Metadata     map[string]interface{} `json:"metadata"`
	ParentHeader struct {
		MsgID string `json:"msg_id"`
	} `json:"parent_header"`
}

// NewClient creates a new Jupyter client
func NewClient(connectionInfo ConnectionInfo) (*Client, error) {
	baseURL := fmt.Sprintf("http://%s:%d", connectionInfo.Host, connectionInfo.Port)
	c := &Client{
		connectionInfo: connectionInfo,
		baseURL:        baseURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}

	availableKernels, err := c.listKernelSpecs()
	if err != nil {
		return nil, err
	}

	if _, ok := availableKernels.KernelSpces[connectionInfo.KernelName]; !ok {
		return nil, fmt.Errorf("kernel %s not found", connectionInfo.KernelName)
	}

	c.kernelID, err = c.startKernel(connectionInfo.KernelName)
	if err != nil {
		return nil, err
	}

	wsUrl := fmt.Sprintf("ws://%s:%d/api/kernels/%s/channels", c.connectionInfo.Host, c.connectionInfo.Port, c.kernelID)
	var reqHeader http.Header
	if c.connectionInfo.Token != "" {
		reqHeader = http.Header{
			"Authorization": []string{"token " + c.connectionInfo.Token},
		}
	}
	ws, _, err := websocket.DefaultDialer.Dial(wsUrl, reqHeader)
	if err != nil {
		return nil, err
	}

	c.ws = ws
	c.sessionID = uuid.New().String()
	ready, err := c.waitForReady()
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, fmt.Errorf("kernel not ready")
	}

	return c, nil
}

// CodeBlockDelimiter implements the CodeExecutor interface
func (c *Client) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}

// ExecuteCode implements the CodeExecutor interface
func (c *Client) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	var allOutput strings.Builder

	for _, block := range input.CodeBlocks {
		code := block.Code
		lang := block.Language

		code = silencePip(code, lang)

		output, err := c.runCode(code)
		if err != nil {
			return codeexecutor.CodeExecutionResult{}, err
		}

		allOutput.WriteString(output)
	}

	return codeexecutor.CodeExecutionResult{
		Output:      allOutput.String(),
		OutputFiles: []codeexecutor.File{}, // jupyter executor does not support output files yet
	}, nil
}

// listKernelSpecs lists all available kernel specs
func (c *Client) listKernelSpecs() (kernelSpecsResponse, error) {
	url := fmt.Sprintf("%s/api/kernelspecs", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return kernelSpecsResponse{}, err
	}

	if c.connectionInfo.Token != "" {
		req.Header.Set("Authorization", "token "+c.connectionInfo.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return kernelSpecsResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return kernelSpecsResponse{}, fmt.Errorf("failed to list kernelspecs: %s", resp.Status)
	}

	var kernelSpecs kernelSpecsResponse
	if err := json.NewDecoder(resp.Body).Decode(&kernelSpecs); err != nil {
		return kernelSpecsResponse{}, err
	}

	return kernelSpecs, nil
}

// startKernel starts a new kernel with the given name.
func (c *Client) startKernel(kernelName string) (string, error) {
	url := fmt.Sprintf("%s/api/kernels", c.baseURL)

	type KernelRequest struct {
		Name string `json:"name"`
	}

	type KernelResponse struct {
		ID string `json:"id"`
	}

	kernelReq := KernelRequest{
		Name: kernelName,
	}

	body, err := json.Marshal(kernelReq)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.connectionInfo.Token != "" {
		req.Header.Set("Authorization", "token "+c.connectionInfo.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("failed to start kernel: %s", resp.Status)
	}

	var kernelResp KernelResponse
	if err := json.NewDecoder(resp.Body).Decode(&kernelResp); err != nil {
		return "", err
	}

	return kernelResp.ID, nil
}

func (c *Client) waitForReady() (bool, error) {
	msgID, err := c.sendMessage(map[string]interface{}{}, "shell", "kernel_info_request")
	if err != nil {
		return false, err
	}

	for {
		var message executionMessage
		if err := c.ws.ReadJSON(&message); err != nil {
			return false, err
		}

		if message.Header.MsgType == "kernel_info_reply" && message.ParentHeader.MsgID == msgID {
			return true, nil
		}
	}
}

// sendMessage sends a message to the kernel
func (c *Client) sendMessage(content map[string]interface{}, channel string, messageType string) (string, error) {
	timestamp := time.Now().Format(time.RFC3339)
	messageID := uuid.New().String()
	message := map[string]interface{}{
		"header": map[string]interface{}{
			"username": "trpc-agent-go",
			"version":  "5.0",
			"session":  c.sessionID,
			"msg_id":   messageID,
			"msg_type": messageType,
			"date":     timestamp,
		},
		"parent_header": map[string]interface{}{},
		"metadata":      map[string]interface{}{},
		"content":       content,
		"buffers":       []interface{}{},
		"channel":       channel,
	}
	if err := c.ws.WriteJSON(message); err != nil {
		return "", err
	}

	return messageID, nil
}

// runCode executes the given code, now only return text output
func (c *Client) runCode(code string) (string, error) {
	msgID, err := c.sendMessage(map[string]interface{}{
		"code":             code,
		"silent":           false,
		"store_history":    true,
		"user_expressions": map[string]interface{}{},
		"allow_stdin":      false,
		"stop_on_error":    true,
	}, "shell", "execute_request")
	if err != nil {
		return "", err
	}
	textOutput := make([]string, 0)
	errMsg := make([]string, 0)
	for {
		var message executionMessage
		if err := c.ws.ReadJSON(&message); err != nil {
			return "", err
		}
		if message.Header.MsgType == "" {
			return "", fmt.Errorf("message is nil")
		}
		if message.ParentHeader.MsgID != msgID {
			continue
		}
		msgType := message.Header.MsgType
		content := message.Content
		if msgType == "status" && content["execution_state"] == "idle" {
			break
		}
		if msgType == "error" {
			for errKey, errValue := range content {
				errMsg = append(errMsg, fmt.Sprintf("%s: %v", errKey, errValue))
			}
		}
		if text, ok := content["text"].(string); ok {
			textOutput = append(textOutput, text)
		}
	}
	if len(errMsg) != 0 {
		return "", fmt.Errorf("execute code error: %s", strings.Join(errMsg, "\n"))
	}
	return strings.Join(textOutput, "\n"), nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	return c.ws.Close()
}
