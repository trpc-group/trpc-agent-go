//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

package graph

import (
	"sync"
)

// ChannelType represents the type of channel behavior.
type ChannelType int

const (
	// ChannelTypeLastValue stores only the last value sent to the channel.
	ChannelTypeLastValue ChannelType = iota
	// ChannelTypeTopic accumulates multiple values (pub/sub).
	ChannelTypeTopic
	// ChannelTypeEphemeral stores values temporarily for one step.
	ChannelTypeEphemeral
	// ChannelTypeBarrier waits for multiple inputs before proceeding.
	ChannelTypeBarrier
)

// Channel represents a communication channel between nodes in Pregel-style execution.
type Channel struct {
	mu          sync.RWMutex
	Name        string
	Type        ChannelType
	Value       any
	Values      []any // For Topic channels
	Subscribers []string
	BarrierSet  map[string]bool // For barrier channels
	Version     int64
	Available   bool
}

// NewChannel creates a new channel with the specified type.
func NewChannel(name string, channelType ChannelType) *Channel {
	return &Channel{
		Name:       name,
		Type:       channelType,
		Values:     make([]any, 0),
		BarrierSet: make(map[string]bool),
		Available:  false,
	}
}

// Update updates the channel with new values.
func (c *Channel) Update(values []any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.Type {
	case ChannelTypeLastValue:
		if len(values) > 0 {
			c.Value = values[len(values)-1]
			c.Version++
			c.Available = true
			return true
		}
	case ChannelTypeTopic:
		c.Values = append(c.Values, values...)
		c.Version++
		c.Available = true
		return true
	case ChannelTypeEphemeral:
		if len(values) > 0 {
			c.Value = values[0]
			c.Version++
			c.Available = true
			return true
		}
	case ChannelTypeBarrier:
		for _, value := range values {
			if sender, ok := value.(string); ok {
				c.BarrierSet[sender] = true
			}
		}
		c.Version++
		c.Available = true
		return true
	}
	return false
}

// Get retrieves the current value from the channel.
func (c *Channel) Get() any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	switch c.Type {
	case ChannelTypeLastValue, ChannelTypeEphemeral:
		return c.Value
	case ChannelTypeTopic:
		return c.Values
	case ChannelTypeBarrier:
		return c.BarrierSet
	}
	return nil
}

// Consume consumes the channel value (for ephemeral channels).
func (c *Channel) Consume() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Type == ChannelTypeEphemeral {
		c.Value = nil
		c.Available = false
		return true
	}
	return false
}

// IsAvailable checks if the channel has data available.
func (c *Channel) IsAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Available
}

// Finish marks the channel as finished (for barrier channels).
func (c *Channel) Finish() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Available = false
	return true
}

// Acknowledge marks the channel as consumed for this step so it doesn't
// retrigger planning in the next step.
func (c *Channel) Acknowledge() {
	c.mu.Lock()
	c.Available = false
	c.mu.Unlock()
}

// ChannelManager manages all channels in the graph.
type ChannelManager struct {
	channels map[string]*Channel
	mu       sync.RWMutex
}

// NewChannelManager creates a new channel manager.
func NewChannelManager() *ChannelManager {
	return &ChannelManager{
		channels: make(map[string]*Channel),
	}
}

// AddChannel adds a channel to the manager.
func (cm *ChannelManager) AddChannel(name string, channelType ChannelType) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.channels[name] = NewChannel(name, channelType)
}

// GetChannel retrieves a channel by name.
func (cm *ChannelManager) GetChannel(name string) (*Channel, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	channel, exists := cm.channels[name]
	return channel, exists
}

// GetAllChannels returns all channels.
func (cm *ChannelManager) GetAllChannels() map[string]*Channel {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make(map[string]*Channel)
	for k, v := range cm.channels {
		result[k] = v
	}
	return result
}
