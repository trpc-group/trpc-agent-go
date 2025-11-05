package jupyter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var cstUpgrader = websocket.Upgrader{
	Subprotocols:      []string{"p0", "p1"},
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	EnableCompression: true,
	Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
		http.Error(w, reason.Error(), status)
	},
}

type jupyterHandler struct {
	*testing.T
	s *cstServer
}

type cstServer struct {
	url    string
	Server *httptest.Server
	wg     sync.WaitGroup
	host   string
	port   int
	cli    *Client
}

func makeWsProto(s string) string {
	return "ws" + strings.TrimPrefix(s, "http")
}

func (s *cstServer) Close() {
	s.Server.Close()
	// Wait for handler functions to complete.
	s.wg.Wait()
}

func newServer(t *testing.T) *cstServer {
	var s cstServer
	s.Server = httptest.NewServer(jupyterHandler{T: t, s: &s})
	s.url = makeWsProto(s.Server.URL)
	parsed, err := url.Parse(s.Server.URL)
	assert.NoError(t, err)
	s.host = parsed.Hostname()
	s.port, err = strconv.Atoi(parsed.Port())
	assert.NoError(t, err)
	cli := &Client{
		connectionInfo: ConnectionInfo{
			Host: s.url,
			Port: s.port,
		},
		baseURL:    fmt.Sprintf("http://%s:%d", s.host, s.port),
		httpClient: s.Server.Client(),
	}
	wsUrl := fmt.Sprintf("ws://%s:%d/api/kernels/123/channels", s.host, s.port)
	ws, _, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	assert.NoError(t, err)
	cli.ws = ws
	s.cli = cli
	return &s
}

func (t jupyterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.URL.Path == "/api/kernelspecs" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"kernelspecs":{"python3":{"name":"python3","spec":{"argv":["python3","-m","ipykernel_launcher","-f","{connection_file}"],"display_name":"Python 3 (ipykernel)","language":"python","metadata":{"interrupt_mode":"message","env":{}},"env":{}},"resources":{}}}}`))
		return
	}
	if r.URL.Path == "/api/kernels" {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": "123"}`))
		return
	}
	if r.URL.Path != "/api/kernels/123/channels" {
		t.Logf("query=%v, want %v", r.URL.RawQuery, "api/kernels/123/channels")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	ws, err := cstUpgrader.Upgrade(w, r, http.Header{"Set-Cookie": {"sessionID=1234"}})
	if err != nil {
		t.Logf("Upgrade: %v", err)
		return
	}
	defer ws.Close()
	_, rd, err := ws.NextReader()
	if err != nil {
		t.Logf("NextReader: %v", err)
		return
	}
	var msg executionMessage
	if err := json.NewDecoder(rd).Decode(&msg); err != nil {
		t.Logf("Decode: %v", err)
		return
	}
	res := executionMessage{
		Header: struct {
			MsgType string `json:"msg_type"`
			MsgID   string `json:"msg_id"`
		}{
			MsgID: msg.Header.MsgID,
		},
		ParentHeader: struct {
			MsgID string `json:"msg_id"`
		}{
			MsgID: msg.Header.MsgID,
		},
		Content: msg.Content,
	}
	if msg.Header.MsgType == "kernel_info_request" {
		res.Header.MsgType = "kernel_info_reply"
		ws.WriteJSON(res)
		return
	}
	res.Header.MsgType = "status"
	res.Content = map[string]interface{}{"execution_state": "idle"}
	ws.WriteJSON(res)
}

func newInvalidClient() *Client {
	return &Client{
		connectionInfo: ConnectionInfo{
			Host: "127.0.0.1",
			Port: 8889,
		},
		baseURL: "http://127.0.0.1:8889",
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		waitReadyTimeout: 10 * time.Second,
	}
}

func Test_listKernelSpecs(t *testing.T) {
	cli := newInvalidClient()
	_, err := cli.listKernelSpecs()
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = srv.cli.listKernelSpecs()
	assert.NoError(t, err)
}

func Test_startKernel(t *testing.T) {
	cli := newInvalidClient()
	_, err := cli.startKernel("python3")
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = srv.cli.startKernel("python3")
	assert.NoError(t, err)
}

func Test_executeCode(t *testing.T) {
	cli := newInvalidClient()
	_, err := cli.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "print('hello world')", Language: "python"},
		},
		ExecutionID: "test",
	})
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = srv.cli.ExecuteCode(context.Background(), codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{Code: "print('hello world')", Language: "python"},
		},
		ExecutionID: "test",
	})
	assert.NoError(t, err)
}

func Test_waitForReady(t *testing.T) {
	cli := newInvalidClient()
	_, err := cli.waitForReady()
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = srv.cli.waitForReady()
	assert.NoError(t, err)
}

func Test_sendMessage(t *testing.T) {
	cli := newInvalidClient()
	_, err := cli.sendMessage(map[string]interface{}{}, "test", "test")
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = srv.cli.sendMessage(map[string]interface{}{}, "test", "test")
	assert.NoError(t, err)
}

func Test_runCode(t *testing.T) {
	cli := newInvalidClient()
	_, err := cli.runCode("print('hello world')")
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = srv.cli.runCode("print('hello world')")
	assert.NoError(t, err)
}

func TestClose(t *testing.T) {
	srv := newServer(t)
	err := srv.cli.Close()
	assert.NoError(t, err)
}

func TestNewClient(t *testing.T) {
	_, err := NewClient(ConnectionInfo{
		Host:             "127.0.0.1",
		Port:             8889,
		KernelName:       "python3",
		WaitReadyTimeout: time.Second * 10,
	})
	assert.Error(t, err)

	srv := newServer(t)
	defer srv.Close()

	_, err = NewClient(ConnectionInfo{
		Host:             srv.host,
		Port:             srv.port,
		KernelName:       "python3",
		WaitReadyTimeout: time.Second * 10,
	})
	assert.NoError(t, err)
}
