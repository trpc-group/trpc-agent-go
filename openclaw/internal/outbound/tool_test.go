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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type stubSender struct {
	id     string
	target string
	text   string
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

func TestTool_Call_UsesCurrentSession(t *testing.T) {
	router := NewRouter()
	sender := &stubSender{id: "telegram"}
	router.RegisterSender(sender)

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
