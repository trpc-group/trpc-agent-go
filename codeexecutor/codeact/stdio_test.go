package codeact

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExecutePythonGuest(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	add := testTool{declaration: &tool.Declaration{Name: "add", InputSchema: &tool.Schema{Type: "object", Required: []string{"a", "b"}, Properties: map[string]*tool.Schema{"a": {Type: "integer"}, "b": {Type: "integer"}}}}, call: func(raw []byte) (any, error) {
		var in struct{ A, B int }
		require.NoError(t, json.Unmarshal(raw, &in))
		return in.A + in.B, nil
	}}
	g, err := NewGateway(add)
	require.NoError(t, err)
	result, err := Execute(context.Background(), LocalRunner{}, g, "value = await call_tool('add', a=20, b=22)\nprint('computed', value)\nreturn {'answer': value}")
	require.NoError(t, err)
	require.JSONEq(t, `{"answer":42}`, string(result.Value))
	require.Contains(t, result.Stdout, "computed 42")
}

func TestExecuteStdioReapsGuestAfterProtocolError(t *testing.T) {
	process := &fakeStdioProcess{stdout: io.NopCloser(strings.NewReader("not-json\n"))}
	_, err := executeStdio(
		context.Background(),
		fakeStdioRunner{process: process},
		Request{Code: "return 1"},
		fakeToolCallHandler{},
	)
	require.Error(t, err)
	require.Equal(t, 1, process.kills)
	require.Equal(t, 1, process.waits)
	require.Equal(t, 1, process.stdinCloses)
}

type fakeStdioRunner struct{ process *fakeStdioProcess }

func (r fakeStdioRunner) start(context.Context, string) (stdioProcess, error) { return r.process, nil }

type fakeStdioProcess struct {
	stdin       bytes.Buffer
	stdout      io.ReadCloser
	kills       int
	waits       int
	stdinCloses int
}

func (p *fakeStdioProcess) Stdin() io.WriteCloser {
	return fakeWriteCloser{Writer: &p.stdin, onClose: func() { p.stdinCloses++ }}
}
func (p *fakeStdioProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *fakeStdioProcess) Wait() error {
	p.waits++
	return nil
}
func (p *fakeStdioProcess) Kill() error {
	p.kills++
	return nil
}

type fakeWriteCloser struct {
	io.Writer
	onClose func()
}

func (w fakeWriteCloser) Close() error {
	w.onClose()
	return nil
}

type fakeToolCallHandler struct{}

func (fakeToolCallHandler) HandleToolCall(context.Context, ToolCall) (json.RawMessage, error) {
	return nil, nil
}
