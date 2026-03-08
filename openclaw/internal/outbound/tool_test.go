//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package outbound

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stubSender struct {
	id     string
	target string
	text   string
	files  []channel.OutboundFile
}

func (s *stubSender) ID() string { return s.id }

func (s *stubSender) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *stubSender) SendText(
	ctx context.Context,
	target string,
	text string,
) error {
	s.target = target
	s.text = text
	return nil
}

func (s *stubSender) SendMessage(
	_ context.Context,
	target string,
	msg channel.OutboundMessage,
) error {
	s.target = target
	s.text = msg.Text
	s.files = append([]channel.OutboundFile(nil), msg.Files...)
	return nil
}

func TestTool_Call_UsesCurrentSession(t *testing.T) {
	router := NewRouter()
	sender := &stubSender{id: "telegram"}
	router.RegisterSender(sender)
	router.RegisterMessageSender(sender)

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", "user", "telegram:dm:100"),
		),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := NewTool(router).Call(
		ctx,
		[]byte(`{"text":"hello"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "100", sender.target)
	require.Equal(t, "hello", sender.text)
	require.Equal(t, true, result.(map[string]any)["ok"])
}

func TestRouter_Channels_Sorted(t *testing.T) {
	router := NewRouter()
	router.RegisterSender(&stubSender{id: "b"})
	router.RegisterSender(&stubSender{id: "a"})

	require.Equal(t, []string{"a", "b"}, router.Channels())
}

func TestTool_Call_ExplicitTargetAndErrors(t *testing.T) {
	router := NewRouter()
	sender := &stubSender{id: "telegram"}
	router.RegisterSender(sender)
	router.RegisterMessageSender(sender)

	tool := NewTool(router)
	require.Equal(t, toolMessage, tool.Declaration().Name)

	args, err := json.Marshal(map[string]any{
		"text":    "hello",
		"channel": "telegram",
		"target":  "200",
	})
	require.NoError(t, err)

	result, err := tool.Call(context.Background(), args)
	require.NoError(t, err)
	require.Equal(t, "200", sender.target)
	require.Equal(t, "hello", sender.text)
	require.Equal(t, "telegram", result.(map[string]any)["channel"])

	_, err = tool.Call(context.Background(), []byte(`{"text":" "}`))
	require.Error(t, err)

	_, err = tool.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	_, err = NewTool(nil).Call(
		context.Background(),
		[]byte(`{"text":"hi"}`),
	)
	require.Error(t, err)
}

func TestTool_Call_SendsFilesWithoutText(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	sender := &stubSender{id: "telegram"}
	router.RegisterMessageSender(sender)

	tool := NewTool(router)
	result, err := tool.Call(
		context.Background(),
		[]byte(`{
			"channel":"telegram",
			"target":"100",
			"files":["a.pdf"],
			"media":["a.pdf","images/p1.png"],
			"file":"audio.wav"
		}`),
	)
	require.NoError(t, err)
	require.Equal(t, "100", sender.target)
	require.Equal(t, "", sender.text)
	require.Equal(
		t,
		[]string{
			"audio.wav",
			"a.pdf",
			filepath.ToSlash("images/p1.png"),
		},
		[]string{
			sender.files[0].Path,
			filepath.ToSlash(sender.files[1].Path),
			sender.files[2].Path,
		},
	)
	require.Equal(t, 3, result.(map[string]any)["files_sent"])
}

func TestTool_Call_PropagatesAsVoice(t *testing.T) {
	t.Parallel()

	router := NewRouter()
	sender := &stubSender{id: "telegram"}
	router.RegisterMessageSender(sender)

	tool := NewTool(router)
	_, err := tool.Call(
		context.Background(),
		[]byte(`{
			"channel":"telegram",
			"target":"100",
			"file":"reply.mp3",
			"as_voice":true
		}`),
	)
	require.NoError(t, err)
	require.Len(t, sender.files, 1)
	require.True(t, sender.files[0].AsVoice)
}

func TestTool_Call_ExpandsDirectoryAndGlob(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "out")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	pdf := filepath.Join(dir, "page-1.pdf")
	png := filepath.Join(dir, "page-2.png")
	require.NoError(t, os.WriteFile(pdf, []byte("pdf"), 0o600))
	require.NoError(t, os.WriteFile(png, []byte("png"), 0o600))

	router := NewRouter()
	sender := &stubSender{id: "telegram"}
	router.RegisterMessageSender(sender)

	tool := NewTool(router)
	args, err := json.Marshal(map[string]any{
		"channel": "telegram",
		"target":  "100",
		"files":   []string{dir, filepath.Join(root, "*.missing")},
		"media":   []string{filepath.Join(dir, "*.png")},
	})
	require.NoError(t, err)
	_, err = tool.Call(context.Background(), args)
	require.NoError(t, err)
	require.Len(t, sender.files, 3)
	require.Equal(t, pdf, sender.files[0].Path)
	require.Equal(t, png, sender.files[1].Path)
	require.Equal(t, filepath.Join(root, "*.missing"), sender.files[2].Path)
}
