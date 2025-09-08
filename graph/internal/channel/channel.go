//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package channel provides a channel implementation for the graph.
package channel

import (
	"sync"
)

// Behavior represents the type of channel behavior.
type Behavior int

const (
	// BehaviorLastValue stores only the last value sent to the channel.
	BehaviorLastValue Behavior = iota
	// BehaviorTopic accumulates multiple values (pub/sub).
	BehaviorTopic
	// BehaviorEphemeral stores values temporarily for one step.
	BehaviorEphemeral
	// BehaviorBarrier waits for multiple inputs before proceeding.
	BehaviorBarrier
)

// Channel represents a communication channel between nodes in Pregel-style execution.
type Channel struct {
	mu              sync.RWMutex
	Name            string
	Behavior        Behavior
	Value           any
	Values          []any
	Subscribers     []string
	BarrierSet      map[string]bool
	Version         int64
	Available       bool
	LastUpdatedStep int
}

// NewChannel creates a new channel with the specified behavior.
func NewChannel(name string, channelBehavior Behavior) *Channel {
	return &Channel{
		Name:       name,
		Behavior:   channelBehavior,
		Values:     make([]any, 0),
		BarrierSet: make(map[string]bool),
		Available:  false,
	}
}

// Update updates the channel with new values.
func (c *Channel) Update(values []any, step int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.Behavior {
	case BehaviorLastValue:
		if len(values) > 0 {
			c.Value = values[len(values)-1]
			c.Version++
			c.Available = true
			c.LastUpdatedStep = step
			return true
		}
	case BehaviorTopic:
		c.Values = append(c.Values, values...)
		c.Version++
		c.Available = true
		c.LastUpdatedStep = step
		return true
	case BehaviorEphemeral:
		if len(values) > 0 {
			c.Value = values[0]
			c.Version++
			c.Available = true
			c.LastUpdatedStep = step
			return true
		}
	case BehaviorBarrier:
		for _, value := range values {
			if sender, ok := value.(string); ok {
				c.BarrierSet[sender] = true
			}
		}
		c.Version++
		c.Available = true
		c.LastUpdatedStep = step
		return true
	}
	return false
}

// Get retrieves the current value from the channel.
func (c *Channel) Get() any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	switch c.Behavior {
	case BehaviorLastValue, BehaviorEphemeral:
		return c.Value
	case BehaviorTopic:
		return c.Values
	case BehaviorBarrier:
		return c.BarrierSet
	}
	return nil
}

// Consume consumes the channel value (for ephemeral channels).
func (c *Channel) Consume() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Behavior == BehaviorEphemeral {
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

// IsUpdatedInStep returns true if the channel was updated in the specified step.
func (c *Channel) IsUpdatedInStep(step int) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LastUpdatedStep == step
}

// ClearStepMark clears the step update mark, typically called after checkpoint creation.
func (c *Channel) ClearStepMark() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastUpdatedStep = 0
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

// Manager manages all channels in the graph.
type Manager struct {
	channels map[string]*Channel
	mu       sync.RWMutex
}

// NewChannelManager creates a new channel manager.
func NewChannelManager() *Manager {
	return &Manager{
		channels: make(map[string]*Channel),
	}
}

// AddChannel adds a channel to the manager.
func (m *Manager) AddChannel(name string, channelBehavior Behavior) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[name] = NewChannel(name, channelBehavior)
}

// GetChannel retrieves a channel by name.
func (m *Manager) GetChannel(name string) (*Channel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	channel, exists := m.channels[name]
	return channel, exists
}

// GetAllChannels returns all channels.
func (m *Manager) GetAllChannels() map[string]*Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*Channel)
	for k, v := range m.channels {
		result[k] = v
	}
	return result
}
