package filter

import (
	"fmt"
	"sync"
)

// GlobalFilterManager is a global instance for convenience.
var GlobalFilterManager = NewFilterManager()

// FilterManager manages registration and execution of filters.
type FilterManager struct {
	mu      sync.RWMutex
	filters map[InterceptionPoint][]Filter
}

// NewFilterManager creates a new FilterManager.
func NewFilterManager() *FilterManager {
	return &FilterManager{
		filters: make(map[InterceptionPoint][]Filter),
	}
}

// Register registers a filter for its supported interception points.
func (fm *FilterManager) Register(filterInstance Filter) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	supportedPoints := filterInstance.Type()
	if len(supportedPoints) == 0 {
		return fmt.Errorf("filter %T must support at least one interception point", filterInstance)
	}

	// Validate interception points.
	for _, point := range supportedPoints {
		if !isValidInterceptionPoint(point) {
			return fmt.Errorf("invalid interception point: %v", point)
		}
	}

	// Validate paired points for non-stream points.
	if err := validatePairedPoints(supportedPoints, filterInstance); err != nil {
		return err
	}

	// Register filter for each point.
	for _, point := range supportedPoints {
		fm.filters[point] = append(fm.filters[point], filterInstance)
	}

	fmt.Printf("Registered filter %T for points: %v\n", filterInstance, supportedPoints)
	return nil
}

// GetFilters returns all filters registered for a given interception point.
func (fm *FilterManager) GetFilters(point InterceptionPoint) []Filter {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	return fm.filters[point]
}

// Execute runs pre or post invoke methods of registered filters based on the interception point.
// Returns false if any pre-invoke filter returns false, otherwise true.
func (fm *FilterManager) Execute(point InterceptionPoint, ctx AgentContext) bool {
	filters := fm.GetFilters(point)
	for _, filterInstance := range filters {
		if isPrePoint(point) {
			if proceed, _ := filterInstance.PreInvoke(ctx, point); !proceed {
				return false
			}
		} else {
			filterInstance.PostInvoke(ctx, point)
		}
	}
	return true
}

// isValidInterceptionPoint checks if the point is a valid InterceptionPoint.
func isValidInterceptionPoint(point InterceptionPoint) bool {
	switch point {
	case PreLLMInvoke, PostLLMInvoke,
		PreToolInvoke, PostToolInvoke,
		PreAgentInvoke, PostAgentInvoke,
		PreAgentExecute, PostAgentExecute,
		AgentStreamInvoke, AgentStreamExecute:
		return true
	default:
		return false
	}
}

var prePoints = map[InterceptionPoint]struct{}{
	PreLLMInvoke:    {},
	PreToolInvoke:   {},
	PreAgentInvoke:  {},
	PreAgentExecute: {},
}

func isPrePoint(point InterceptionPoint) bool {
	_, ok := prePoints[point]
	return ok
}

// validatePairedPoints checks that non-stream points appear in pre/post pairs.
func validatePairedPoints(points []InterceptionPoint, filterInstance Filter) error {
	// Define all pre/post pairs.
	pairs := [][2]InterceptionPoint{
		{PreLLMInvoke, PostLLMInvoke},
		{PreToolInvoke, PostToolInvoke},
		{PreAgentInvoke, PostAgentInvoke},
		{PreAgentExecute, PostAgentExecute},
	}

	// Count occurrences for each point.
	count := make(map[InterceptionPoint]int)
	for _, p := range points {
		count[p]++
	}

	for _, pair := range pairs {
		pre, post := pair[0], pair[1]
		if count[pre] != count[post] {
			return fmt.Errorf("filter %T: %s/%s pre/post interception points must appear in pairs", filterInstance, pre, post)
		}
	}
	return nil
}
