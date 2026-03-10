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
	mu             sync.RWMutex
	textSenders    map[string]channel.TextSender
	messageSenders map[string]channel.MessageSender
}

// NewRouter creates an empty outbound router.
func NewRouter() *Router {
	return &Router{
		textSenders:    make(map[string]channel.TextSender),
		messageSenders: make(map[string]channel.MessageSender),
	}
}

// Register adds a channel sender when the channel implements TextSender.
func (r *Router) Register(ch channel.Channel) {
	if sender, ok := ch.(channel.TextSender); ok {
		r.RegisterSender(sender)
	}
	if sender, ok := ch.(channel.MessageSender); ok {
		r.RegisterMessageSender(sender)
	}
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
	r.textSenders[id] = sender
}

// RegisterMessageSender adds or replaces a media-capable sender.
func (r *Router) RegisterMessageSender(sender channel.MessageSender) {
	if r == nil || sender == nil {
		return
	}
	id := strings.TrimSpace(sender.ID())
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messageSenders[id] = sender
}

// Channels returns the sorted list of registered channel ids.
func (r *Router) Channels() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	index := make(map[string]struct{})
	for id := range r.textSenders {
		index[id] = struct{}{}
	}
	for id := range r.messageSenders {
		index[id] = struct{}{}
	}
	out := make([]string, 0, len(index))
	for id := range index {
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
	return r.SendMessage(ctx, target, channel.OutboundMessage{
		Text: text,
	})
}

// SendMessage delivers text and optional local files through the selected
// channel.
func (r *Router) SendMessage(
	ctx context.Context,
	target DeliveryTarget,
	msg channel.OutboundMessage,
) error {
	if r == nil {
		return fmt.Errorf("outbound: nil router")
	}

	channelID := strings.TrimSpace(target.Channel)
	if channelID == "" {
		return fmt.Errorf("outbound: empty channel")
	}

	r.mu.RLock()
	messageSender := r.messageSenders[channelID]
	textSender := r.textSenders[channelID]
	r.mu.RUnlock()
	if messageSender != nil {
		return messageSender.SendMessage(
			ctx,
			target.Target,
			msg,
		)
	}
	if textSender == nil {
		return fmt.Errorf(
			"outbound: unsupported channel: %s",
			channelID,
		)
	}
	if len(msg.Files) > 0 {
		return fmt.Errorf(
			"outbound: channel does not support file delivery: %s",
			channelID,
		)
	}
	return textSender.SendText(ctx, target.Target, msg.Text)
}
