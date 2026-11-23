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
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestShutdownClosesTimedOutSessionsInParallel(t *testing.T) {
	server, err := New(&testRunner{})
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	closeStarted := make(chan string, 2)
	releaseClose := make(chan struct{})
	for _, name := range []string{"first", "second"} {
		protocolAgent.sessions[name] = &sessionState{
			id:         name,
			requestID:  "request-" + name,
			promptDone: make(chan struct{}),
			toolSets: []tool.ToolSet{&blockingToolSet{
				name:         name,
				closeStarted: closeStarted,
				releaseClose: releaseClose,
			}},
		}
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		protocolAgent.shutdownWithTimeout(10 * time.Millisecond)
	}()

	started := make(map[string]bool)
	for index := 0; index < 2; index++ {
		select {
		case name := <-closeStarted:
			started[name] = true
		case <-time.After(time.Second):
			t.Fatal("shutdown did not start closing sessions in parallel")
		}
	}
	assert.Equal(t, map[string]bool{"first": true, "second": true}, started)
	close(releaseClose)
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestCloseSessionForcesMCPCloseAfterPromptTimeout(t *testing.T) {
	server, err := New(&testRunner{})
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	closeStarted := make(chan string, 1)
	releaseClose := make(chan struct{})
	session := &sessionState{
		id:         "close-session",
		requestID:  "request-close-session",
		promptDone: make(chan struct{}),
		toolSets: []tool.ToolSet{&blockingToolSet{
			name:         "close-session",
			closeStarted: closeStarted,
			releaseClose: releaseClose,
		}},
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		protocolAgent.closeSessionWithTimeout(session, 10*time.Millisecond)
	}()

	select {
	case name := <-closeStarted:
		assert.Equal(t, "close-session", name)
	case <-time.After(time.Second):
		t.Fatal("session close did not force MCP close after timeout")
	}
	close(releaseClose)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("session close did not finish")
	}
}

func TestNewSessionRejectedAfterShutdownStarts(t *testing.T) {
	server, err := New(
		&testRunner{},
		withSessionIDGenerator(func() string { return "session-after-shutdown" }),
	)
	require.NoError(t, err)
	protocolAgent := newProtocolAgent(server)
	protocolAgent.shutdownWithTimeout(time.Second)

	_, err = protocolAgent.NewSession(
		context.Background(),
		acpsdk.NewSessionRequest{Cwd: "/workspace"},
	)
	require.ErrorContains(t, err, "shutting down")
	assert.Empty(t, protocolAgent.sessions)
	server.sessionMu.Lock()
	defer server.sessionMu.Unlock()
	assert.Empty(t, server.activeSessionIDs)
}

type blockingToolSet struct {
	name         string
	closeStarted chan<- string
	releaseClose <-chan struct{}
}

func (*blockingToolSet) Tools(context.Context) []tool.Tool {
	return nil
}

func (s *blockingToolSet) Close() error {
	s.closeStarted <- s.name
	<-s.releaseClose
	return nil
}

func (s *blockingToolSet) Name() string {
	return s.name
}
