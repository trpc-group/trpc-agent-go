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
	"fmt"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
)

// DeliveryTarget identifies a channel-specific outbound destination.
type DeliveryTarget struct {
	Channel string `json:"channel,omitempty"`
	Target  string `json:"target,omitempty"`
}

// Router dispatches outbound messages to registered channels.
type Router struct {
	mu      sync.RWMutex
	senders map[string]channel.TextSender
}

// NewRouter creates an empty outbound router.
func NewRouter() *Router {
	return &Router{
		senders: make(map[string]channel.TextSender),
	}
}

// Register adds a channel sender when the channel implements TextSender.
func (r *Router) Register(ch channel.Channel) {
	sender, ok := ch.(channel.TextSender)
	if !ok {
		return
	}
	r.RegisterSender(sender)
}

// RegisterSender adds or replaces a sender for its channel id.
func (r *Router) RegisterSender(sender channel.TextSender) {
	if r == nil || sender == nil {
		return
	}
	id := strings.TrimSpace(sender.ID())
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.senders[id] = sender
}

// Channels returns the sorted list of registered channel ids.
func (r *Router) Channels() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.senders))
	for id := range r.senders {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SendText delivers plain text through the selected channel.
func (r *Router) SendText(
	ctx context.Context,
	target DeliveryTarget,
	text string,
) error {
	if r == nil {
		return fmt.Errorf("outbound: nil router")
	}

	channelID := strings.TrimSpace(target.Channel)
	if channelID == "" {
		return fmt.Errorf("outbound: empty channel")
	}

	r.mu.RLock()
	sender := r.senders[channelID]
	r.mu.RUnlock()
	if sender == nil {
		return fmt.Errorf(
			"outbound: unsupported channel: %s",
			channelID,
		)
	}
	return sender.SendText(ctx, target.Target, text)
}
