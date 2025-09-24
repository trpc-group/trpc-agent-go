package evalset

// EvalMetric represents a metric used to evaluate a particular aspect of an eval case
type EvalMetric struct {
	// MetricName identifies the metric
	MetricName string `json:"metric_name"`

	// Threshold value for this metric
	Threshold float64 `json:"threshold"`

	// JudgeModelOptions for metrics that use LLM-as-Judge
	JudgeModelOptions *JudgeModelOptions `json:"judge_model_options,omitempty"`

	// Config contains metric-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`
}

// JudgeModelOptions contains options for LLM-as-Judge evaluation
type JudgeModelOptions struct {
	// JudgeModel name of the model to use
	JudgeModel string `json:"judge_model"`

	// Temperature for the judge model
	Temperature *float64 `json:"temperature,omitempty"`

	// MaxTokens for the judge model response
	MaxTokens *int `json:"max_tokens,omitempty"`

	// NumSamples number of times to sample the model
	NumSamples *int `json:"num_samples,omitempty"`

	// CustomPrompt custom prompt template
	CustomPrompt string `json:"custom_prompt,omitempty"`
}

// MetricInfo provides information about available metrics
type MetricInfo struct {
	// MetricName identifies the metric
	MetricName string `json:"metric_name"`

	// Description of what this metric evaluates
	Description string `json:"description"`

	// ValueInfo describes the nature of values for this metric
	ValueInfo MetricValueInfo `json:"value_info"`

	// RequiresJudgeModel whether this metric requires an LLM judge
	RequiresJudgeModel bool `json:"requires_judge_model"`

	// SupportedBy list of evaluators that support this metric
	SupportedBy []string `json:"supported_by"`
}

// MetricValueInfo describes the type and range of metric values
type MetricValueInfo struct {
	// Interval describes the valid range of values
	Interval *Interval `json:"interval,omitempty"`

	// Type describes the type of values (score, boolean, etc.)
	Type string `json:"type"`

	// Unit describes the unit of measurement
	Unit string `json:"unit,omitempty"`
}

// Interval represents a range of numeric values
type Interval struct {
	// MinValue is the minimum value in the range
	MinValue float64 `json:"min_value"`

	// MaxValue is the maximum value in the range
	MaxValue float64 `json:"max_value"`

	// OpenAtMin whether the interval is open at the minimum
	OpenAtMin bool `json:"open_at_min"`

	// OpenAtMax whether the interval is open at the maximum
	OpenAtMax bool `json:"open_at_max"`
}

// Contains checks if a value is within the interval
func (i *Interval) Contains(value float64) bool {
	if i.OpenAtMin && value <= i.MinValue {
		return false
	}
	if !i.OpenAtMin && value < i.MinValue {
		return false
	}
	if i.OpenAtMax && value >= i.MaxValue {
		return false
	}
	if !i.OpenAtMax && value > i.MaxValue {
		return false
	}
	return true
}
