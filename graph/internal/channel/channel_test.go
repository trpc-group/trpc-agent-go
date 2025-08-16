//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package channel

import (
	"reflect"
	"testing"
)

func TestNewChannel(t *testing.T) {
	tests := []struct {
		name        string
		channelType Type
		expected    *Channel
	}{
		{
			name:        "TypeLastValue",
			channelType: TypeLastValue,
			expected: &Channel{
				Name:       "test",
				Type:       TypeLastValue,
				Values:     make([]any, 0),
				BarrierSet: make(map[string]bool),
				Available:  false,
			},
		},
		{
			name:        "TypeTopic",
			channelType: TypeTopic,
			expected: &Channel{
				Name:       "test",
				Type:       TypeTopic,
				Values:     make([]any, 0),
				BarrierSet: make(map[string]bool),
				Available:  false,
			},
		},
		{
			name:        "TypeEphemeral",
			channelType: TypeEphemeral,
			expected: &Channel{
				Name:       "test",
				Type:       TypeEphemeral,
				Values:     make([]any, 0),
				BarrierSet: make(map[string]bool),
				Available:  false,
			},
		},
		{
			name:        "TypeBarrier",
			channelType: TypeBarrier,
			expected: &Channel{
				Name:       "test",
				Type:       TypeBarrier,
				Values:     make([]any, 0),
				BarrierSet: make(map[string]bool),
				Available:  false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewChannel("test", tt.channelType)
			if ch.Name != tt.expected.Name {
				t.Errorf("NewChannel() name = %v, want %v", ch.Name, tt.expected.Name)
			}
			if ch.Type != tt.expected.Type {
				t.Errorf("NewChannel() type = %v, want %v", ch.Type, tt.expected.Type)
			}
			if ch.Available != tt.expected.Available {
				t.Errorf("NewChannel() available = %v, want %v", ch.Available, tt.expected.Available)
			}
			if ch.Version != 0 {
				t.Errorf("NewChannel() version = %v, want 0", ch.Version)
			}
		})
	}
}

func TestChannel_Update_TypeLastValue(t *testing.T) {
	ch := NewChannel("test", TypeLastValue)

	// Test empty values
	result := ch.Update([]any{})
	if result {
		t.Error("Update() should return false for empty values")
	}
	if ch.Available {
		t.Error("Channel should not be available after empty update")
	}

	// Test single value
	result = ch.Update([]any{"value1"})
	if !result {
		t.Error("Update() should return true for valid value")
	}
	if !ch.Available {
		t.Error("Channel should be available after update")
	}
	if ch.Value != "value1" {
		t.Errorf("Value = %v, want value1", ch.Value)
	}
	if ch.Version != 1 {
		t.Errorf("Version = %v, want 1", ch.Version)
	}

	// Test multiple values (should keep last)
	result = ch.Update([]any{"value2", "value3"})
	if !result {
		t.Error("Update() should return true for valid values")
	}
	if ch.Value != "value3" {
		t.Errorf("Value = %v, want value3", ch.Value)
	}
	if ch.Version != 2 {
		t.Errorf("Version = %v, want 2", ch.Version)
	}
}

func TestChannel_Update_TypeTopic(t *testing.T) {
	ch := NewChannel("test", TypeTopic)

	// Test empty values (TypeTopic returns true even for empty values)
	result := ch.Update([]any{})
	if !result {
		t.Error("Update() should return true for empty values in TypeTopic")
	}
	if !ch.Available {
		t.Error("Channel should be available after update")
	}
	if ch.Version != 1 {
		t.Errorf("Version = %v, want 1", ch.Version)
	}

	// Test single value
	result = ch.Update([]any{"value1"})
	if !result {
		t.Error("Update() should return true for valid value")
	}
	expected := []any{"value1"}
	if !reflect.DeepEqual(ch.Values, expected) {
		t.Errorf("Values = %v, want %v", ch.Values, expected)
	}

	// Test multiple values (should accumulate)
	result = ch.Update([]any{"value2", "value3"})
	if !result {
		t.Error("Update() should return true for valid values")
	}
	expected = []any{"value1", "value2", "value3"}
	if !reflect.DeepEqual(ch.Values, expected) {
		t.Errorf("Values = %v, want %v", ch.Values, expected)
	}
	if ch.Version != 3 {
		t.Errorf("Version = %v, want 3", ch.Version)
	}
}

func TestChannel_Update_TypeEphemeral(t *testing.T) {
	ch := NewChannel("test", TypeEphemeral)

	// Test empty values
	result := ch.Update([]any{})
	if result {
		t.Error("Update() should return false for empty values")
	}

	// Test single value
	result = ch.Update([]any{"value1"})
	if !result {
		t.Error("Update() should return true for valid value")
	}
	if !ch.Available {
		t.Error("Channel should be available after update")
	}
	if ch.Value != "value1" {
		t.Errorf("Value = %v, want value1", ch.Value)
	}

	// Test multiple values (should keep first)
	result = ch.Update([]any{"value2", "value3"})
	if !result {
		t.Error("Update() should return true for valid values")
	}
	if ch.Value != "value2" {
		t.Errorf("Value = %v, want value2", ch.Value)
	}
}

func TestChannel_Update_TypeBarrier(t *testing.T) {
	ch := NewChannel("test", TypeBarrier)

	// Test empty values (TypeBarrier returns true even for empty values)
	result := ch.Update([]any{})
	if !result {
		t.Error("Update() should return true for empty values in TypeBarrier")
	}
	if !ch.Available {
		t.Error("Channel should be available after update")
	}
	if ch.Version != 1 {
		t.Errorf("Version = %v, want 1", ch.Version)
	}

	// Test string values (should be added to barrier set)
	result = ch.Update([]any{"sender1", "sender2"})
	if !result {
		t.Error("Update() should return true for valid values")
	}
	expected := map[string]bool{"sender1": true, "sender2": true}
	if !reflect.DeepEqual(ch.BarrierSet, expected) {
		t.Errorf("BarrierSet = %v, want %v", ch.BarrierSet, expected)
	}

	// Test non-string values (should be ignored)
	result = ch.Update([]any{123, "sender3"})
	if !result {
		t.Error("Update() should return true for valid values")
	}
	expected = map[string]bool{"sender1": true, "sender2": true, "sender3": true}
	if !reflect.DeepEqual(ch.BarrierSet, expected) {
		t.Errorf("BarrierSet = %v, want %v", ch.BarrierSet, expected)
	}
}

func TestChannel_Get(t *testing.T) {
	tests := []struct {
		name        string
		channelType Type
		setup       func(*Channel)
		expected    any
	}{
		{
			name:        "TypeLastValue",
			channelType: TypeLastValue,
			setup: func(ch *Channel) {
				ch.Update([]any{"value1", "value2"})
			},
			expected: "value2",
		},
		{
			name:        "TypeTopic",
			channelType: TypeTopic,
			setup: func(ch *Channel) {
				ch.Update([]any{"value1", "value2"})
			},
			expected: []any{"value1", "value2"},
		},
		{
			name:        "TypeEphemeral",
			channelType: TypeEphemeral,
			setup: func(ch *Channel) {
				ch.Update([]any{"value1", "value2"})
			},
			expected: "value1",
		},
		{
			name:        "TypeBarrier",
			channelType: TypeBarrier,
			setup: func(ch *Channel) {
				ch.Update([]any{"sender1", "sender2"})
			},
			expected: map[string]bool{"sender1": true, "sender2": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewChannel("test", tt.channelType)
			tt.setup(ch)
			result := ch.Get()
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Get() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestChannel_Consume(t *testing.T) {
	// Test TypeEphemeral
	ch := NewChannel("test", TypeEphemeral)
	ch.Update([]any{"value1"})

	result := ch.Consume()
	if !result {
		t.Error("Consume() should return true for TypeEphemeral")
	}
	if ch.Value != nil {
		t.Error("Value should be nil after consume")
	}
	if ch.Available {
		t.Error("Channel should not be available after consume")
	}

	// Test other types
	ch2 := NewChannel("test2", TypeLastValue)
	ch2.Update([]any{"value1"})

	result = ch2.Consume()
	if result {
		t.Error("Consume() should return false for non-ephemeral types")
	}
	if ch2.Value == nil {
		t.Error("Value should not be nil for non-ephemeral types")
	}
}

func TestChannel_IsAvailable(t *testing.T) {
	ch := NewChannel("test", TypeLastValue)

	if ch.IsAvailable() {
		t.Error("Channel should not be available initially")
	}

	ch.Update([]any{"value1"})
	if !ch.IsAvailable() {
		t.Error("Channel should be available after update")
	}
}

func TestChannel_Finish(t *testing.T) {
	ch := NewChannel("test", TypeLastValue)
	ch.Update([]any{"value1"})

	result := ch.Finish()
	if !result {
		t.Error("Finish() should return true")
	}
	if ch.Available {
		t.Error("Channel should not be available after finish")
	}
}

func TestChannel_Acknowledge(t *testing.T) {
	ch := NewChannel("test", TypeLastValue)
	ch.Update([]any{"value1"})

	ch.Acknowledge()
	if ch.Available {
		t.Error("Channel should not be available after acknowledge")
	}
}

func TestNewChannelManager(t *testing.T) {
	manager := NewChannelManager()
	if manager == nil {
		t.Error("NewChannelManager() should not return nil")
	}
	if manager.channels == nil {
		t.Error("Manager channels should be initialized")
	}
}

func TestManager_AddChannel(t *testing.T) {
	manager := NewChannelManager()

	manager.AddChannel("test1", TypeLastValue)
	manager.AddChannel("test2", TypeTopic)

	if len(manager.channels) != 2 {
		t.Errorf("Expected 2 channels, got %d", len(manager.channels))
	}

	ch1, exists := manager.channels["test1"]
	if !exists {
		t.Error("Channel test1 should exist")
	}
	if ch1.Type != TypeLastValue {
		t.Errorf("Channel type = %v, want TypeLastValue", ch1.Type)
	}

	ch2, exists := manager.channels["test2"]
	if !exists {
		t.Error("Channel test2 should exist")
	}
	if ch2.Type != TypeTopic {
		t.Errorf("Channel type = %v, want TypeTopic", ch2.Type)
	}
}

func TestManager_GetChannel(t *testing.T) {
	manager := NewChannelManager()
	manager.AddChannel("test", TypeLastValue)

	// Test existing channel
	ch, exists := manager.GetChannel("test")
	if !exists {
		t.Error("GetChannel() should return true for existing channel")
	}
	if ch == nil {
		t.Error("GetChannel() should return non-nil channel")
	}
	if ch.Name != "test" {
		t.Errorf("Channel name = %v, want test", ch.Name)
	}

	// Test non-existing channel
	ch, exists = manager.GetChannel("nonexistent")
	if exists {
		t.Error("GetChannel() should return false for non-existing channel")
	}
	if ch != nil {
		t.Error("GetChannel() should return nil for non-existing channel")
	}
}

func TestManager_GetAllChannels(t *testing.T) {
	manager := NewChannelManager()
	manager.AddChannel("test1", TypeLastValue)
	manager.AddChannel("test2", TypeTopic)

	channels := manager.GetAllChannels()
	if len(channels) != 2 {
		t.Errorf("Expected 2 channels, got %d", len(channels))
	}

	if _, exists := channels["test1"]; !exists {
		t.Error("Channel test1 should exist in GetAllChannels")
	}
	if _, exists := channels["test2"]; !exists {
		t.Error("Channel test2 should exist in GetAllChannels")
	}

	// Test that returned map is a copy
	channels["test3"] = NewChannel("test3", TypeEphemeral)
	if len(manager.channels) != 2 {
		t.Error("Modifying returned map should not affect original")
	}
}

func TestChannel_Concurrency(t *testing.T) {
	ch := NewChannel("test", TypeTopic)

	// Test concurrent updates
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			ch.Update([]any{id})
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	values := ch.Get().([]any)
	if len(values) != 10 {
		t.Errorf("Expected 10 values, got %d", len(values))
	}
}

func TestManager_Concurrency(t *testing.T) {
	manager := NewChannelManager()

	// Test concurrent channel additions
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			manager.AddChannel("test"+string(rune(id)), TypeLastValue)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	channels := manager.GetAllChannels()
	if len(channels) != 10 {
		t.Errorf("Expected 10 channels, got %d", len(channels))
	}
}

func TestChannel_EdgeCases(t *testing.T) {
	// Test nil values
	ch := NewChannel("test", TypeLastValue)
	ch.Update([]any{nil})
	if ch.Value != nil {
		t.Error("Channel should handle nil values")
	}

	// Test large number of values
	ch2 := NewChannel("test2", TypeTopic)
	largeValues := make([]any, 1000)
	for i := range largeValues {
		largeValues[i] = i
	}
	ch2.Update(largeValues)

	values := ch2.Get().([]any)
	if len(values) != 1000 {
		t.Errorf("Expected 1000 values, got %d", len(values))
	}
}

func TestChannel_VersionIncrement(t *testing.T) {
	ch := NewChannel("test", TypeLastValue)

	// Initial version should be 0
	if ch.Version != 0 {
		t.Errorf("Initial version should be 0, got %d", ch.Version)
	}

	// Update should increment version
	ch.Update([]any{"value1"})
	if ch.Version != 1 {
		t.Errorf("Version should be 1 after first update, got %d", ch.Version)
	}

	// Another update should increment version again
	ch.Update([]any{"value2"})
	if ch.Version != 2 {
		t.Errorf("Version should be 2 after second update, got %d", ch.Version)
	}
}
