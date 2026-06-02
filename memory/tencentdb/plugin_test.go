//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestInjectRecallContext(t *testing.T) {
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base"),
		model.NewUserMessage("hello"),
	}}
	injectRecallContext(req, &recallResponse{
		AppendSystemContext: "system recall",
		PrependContext:      "user recall",
	})

	require.Len(t, req.Messages, 3)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Equal(t, "base\n\nsystem recall", req.Messages[0].Content)
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
	assert.Equal(t, "user recall", req.Messages[1].Content)
	assert.Equal(t, "hello", req.Messages[2].Content)
}

func TestRecallPluginInjectsContext(t *testing.T) {
	var got recallRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, pathRecall, r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_ = json.NewEncoder(w).Encode(recallResponse{
			AppendSystemContext: "remembered system",
			PrependContext:      "remembered user context",
		})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL), WithRecallEnabled(true))
	require.NoError(t, err, "NewService")
	defer svc.Close()

	p := svc.Plugin()
	assert.Equal(t, "tencentdb_agent_memory", p.Name())
	mgr, err := pluginpkg.NewManager(p)
	require.NoError(t, err, "NewManager")
	callbacks := mgr.ModelCallbacks()
	require.NotNil(t, callbacks)
	require.Len(t, callbacks.BeforeModel, 1)

	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base"),
		model.NewUserMessage("what did I say?"),
	}}
	sess := &session.Session{ID: "s1", AppName: "app", UserID: "user"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{Session: sess}).Context
	_, err = callbacks.BeforeModel[0](ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	assert.Equal(t, recallRequest{Query: "what did I say?", SessionKey: "app:user:s1", UserID: "user"}, got)
	require.Len(t, req.Messages, 3)
	assert.Equal(t, "base\n\nremembered system", req.Messages[0].Content)
	assert.Equal(t, "remembered user context", req.Messages[1].Content)
}

func TestRecallAndPluginEdges(t *testing.T) {
	assert.Empty(t, latestUserText(nil))
	assert.Empty(t, latestUserText(&model.Request{Messages: []model.Message{model.NewSystemMessage("sys")}}))

	empty := &model.Request{}
	injectRecallContext(empty, &recallResponse{Context: "legacy context"})
	require.Len(t, empty.Messages, 1)
	assert.Equal(t, model.RoleSystem, empty.Messages[0].Role)

	noSystem := &model.Request{Messages: []model.Message{model.NewAssistantMessage("hi")}}
	injectRecallContext(noSystem, &recallResponse{AppendSystemContext: "sys", PrependContext: "ctx"})
	require.Len(t, noSystem.Messages, 3)
	assert.Equal(t, model.RoleSystem, noSystem.Messages[0].Role)
	assert.Equal(t, "sys", noSystem.Messages[0].Content)
	assert.Equal(t, "ctx", noSystem.Messages[2].Content)
	insertBeforeLatestUser(nil, model.NewUserMessage("ignored"))

	svc := &Service{opts: defaultOptions(), client: &gatewayClient{}}
	p := &recallPlugin{service: svc}
	_, err := p.beforeModel(context.Background(), nil)
	require.NoError(t, err)
	_, err = p.beforeModel(context.Background(), &model.BeforeModelArgs{})
	require.NoError(t, err)
	_, err = p.beforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{model.NewUserMessage("q")}},
	})
	require.NoError(t, err)
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{ID: "s", AppName: "app", UserID: "user"},
	}).Context
	_, err = p.beforeModel(ctx, &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{model.NewSystemMessage("sys")}},
	})
	require.NoError(t, err)
	badScope := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{ID: "s", AppName: "app"},
	}).Context
	_, err = p.beforeModel(badScope, &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{model.NewUserMessage("q")}},
	})
	require.NoError(t, err)
}
