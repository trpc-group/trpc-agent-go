package graph

// Config map keys (used under config["configurable"])
const (
	CfgKeyConfigurable = "configurable"
	CfgKeyThreadID     = "thread_id"
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
