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
	StateKeyCommand   = "__command__"
	StateKeyResumeMap = "__resume_map__"
)

// Checkpoint Metadata.Source enumeration values
const (
	SourceInput     = "input"
	SourceLoop      = "loop"
	SourceInterrupt = "interrupt"
)

// Channel conventions (input channel prefix)
const (
	ChannelInputPrefix = "input:"
)

// Event metadata keys (used in checkpoint events).
const (
	EventKeySource      = "source"
	EventKeyStep        = "step"
	EventKeyDuration    = "duration"
	EventKeyBytes       = "bytes"
	EventKeyWritesCount = "writes_count"
)
