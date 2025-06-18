package event

// Action represents the actions attached to an event.
// This corresponds to the Python EventActions class.
type Action struct {
	// SkipSummarization if true, it won't call model to summarize function response.
	// Only used for function_response event.
	SkipSummarization *bool `json:"skipSummarization,omitempty"`

	// StateDelta indicates that the event is updating the state with the given delta.
	StateDelta map[string]interface{} `json:"stateDelta,omitempty"`

	// TransferToAgent if set, the event transfers to the specified agent.
	TransferToAgent *string `json:"transferToAgent,omitempty"`
} 