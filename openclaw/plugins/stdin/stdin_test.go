//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package stdin

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

type stubGateway struct {
	reqs []gwclient.MessageRequest
}

func (g *stubGateway) SendMessage(
	_ context.Context,
	req gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	g.reqs = append(g.reqs, req)
	if req.Text == "fail" {
		return gwclient.MessageResponse{}, errors.New("boom")
	}
	if req.Text == "ignore" {
		return gwclient.MessageResponse{Ignored: true}, nil
	}
	return gwclient.MessageResponse{Reply: "ok"}, nil
}

func (g *stubGateway) Cancel(context.Context, string) (bool, error) {
	return false, nil
}

func TestInit_RegistersChannel(t *testing.T) {
	f, ok := registry.LookupChannel(pluginType)
	require.True(t, ok)
	require.NotNil(t, f)
}

func TestNewChannel_NilGatewayFails(t *testing.T) {
	t.Parallel()

	_, err := newChannel(
		registry.ChannelDeps{Gateway: nil},
		registry.PluginSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil gateway")
}

func TestNewChannel_DefaultFromAndID(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}
	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.Equal(t, pluginType, got.ID())
	require.Equal(t, defaultFrom, got.from)
}

func TestNewChannel_OverridesFromThreadAndID(t *testing.T) {
	t.Parallel()

	gw := &stubGateway{}

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(
		"from: u1\nthread: t1\n",
	), &node))

	ch, err := newChannel(
		registry.ChannelDeps{Gateway: gw},
		registry.PluginSpec{
			Name:   "c1",
			Config: &node,
		},
	)
	require.NoError(t, err)

	got, ok := ch.(*channel)
	require.True(t, ok)
	require.Equal(t, "c1", got.ID())
	require.Equal(t, "u1", got.from)
	require.Equal(t, "t1", got.thread)
}

func TestChannel_Run_SendsMessagesAndPrintsReply(t *testing.T) {
	gw := &stubGateway{}
	c := &channel{
		id:           "x",
		gw:           gw,
		from:         "u",
		thread:       "t",
		bufBytes:     defaultScannerBufBytes,
		maxLineBytes: defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	_, _ = io.WriteString(inW, "fail\nignore\nhello\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	require.Contains(t, string(out), "STDIN channel started.")
	require.Contains(t, string(out), "(ignored)")
	require.Contains(t, string(out), "ok")

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Contains(t, string(errOut), "boom")

	require.Len(t, gw.reqs, 3)
	require.Equal(t, "fail", gw.reqs[0].Text)
	require.Equal(t, "ignore", gw.reqs[1].Text)
	require.Equal(t, "hello", gw.reqs[2].Text)
	require.Equal(t, "u", gw.reqs[2].From)
	require.Equal(t, "t", gw.reqs[2].Thread)
}

func TestChannel_Run_AllowsLongLine(t *testing.T) {
	gw := &stubGateway{}
	c := &channel{
		id:           "x",
		gw:           gw,
		from:         "u",
		thread:       "t",
		bufBytes:     defaultScannerBufBytes,
		maxLineBytes: defaultScannerMaxBytes,
	}

	stdin := os.Stdin
	stdout := os.Stdout
	stderr := os.Stderr

	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdin = stdin
		os.Stdout = stdout
		os.Stderr = stderr
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	longLine := strings.Repeat("a", 70*1024)
	_, _ = io.WriteString(inW, longLine+"\n/quit\n")
	require.NoError(t, inW.Close())

	require.NoError(t, <-done)

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	_, err = io.ReadAll(outR)
	require.NoError(t, err)

	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)
	require.Empty(t, string(errOut))

	require.Len(t, gw.reqs, 1)
	require.Equal(t, longLine, gw.reqs[0].Text)
}
