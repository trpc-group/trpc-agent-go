//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

// Config map keys (used under config["configurable"])
const (
	CfgKeyConfigurable = "configurable"
	CfgKeyLineageID    = "lineage_id"
	CfgKeyCheckpointID = "checkpoint_id"
	CfgKeyCheckpointNS = "checkpoint_ns"
	CfgKeyResumeMap    = "resume_map"
)

// State map keys (stored into execution state)
const (
	StateKeyCommand        = "__command__"
	StateKeyResumeMap      = "__resume_map__"
	StateKeyNextNodes      = "__next_nodes__"
	StateKeyUsedInterrupts = "__used_interrupts__"
)

// Checkpoint Metadata.Source enumeration values
const (
	SourceInput     = "input"
	SourceLoop      = "loop"
	SourceInterrupt = "interrupt"
)

// Channel conventions (input channel prefix)
const (
	ChannelInputPrefix   = "input:"
	ChannelTriggerPrefix = "trigger:"
	ChannelBranchPrefix  = "branch:to:"
)

// Event metadata keys (used in checkpoint events).
const (
	EventKeySource      = "source"
	EventKeyStep        = "step"
	EventKeyDuration    = "duration"
	EventKeyBytes       = "bytes"
	EventKeyWritesCount = "writes_count"
)

// Common state field names (frequently used in examples and tests).
const (
	StateFieldCounter   = "counter"
	StateFieldStepCount = "step_count"
)
