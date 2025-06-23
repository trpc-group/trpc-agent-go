// Package session provides state management functionality.
package session

// State prefix constants for different scope levels
const (
	StateAppPrefix  = "app:"
	StateUserPrefix = "user:"
	StateTempPrefix = "temp:"
)

// State maintains the current value and the pending-commit delta.
type State struct {
	// value stores the current committed state
	value StateMap
	// delta stores the pending changes that haven't been committed
	delta StateMap
}

// NewState creates a new empty State.
func NewState() *State {
	return &State{
		value: make(StateMap),
		delta: make(StateMap),
	}
}

// Set sets the value of a key in the state.
func (s *State) Set(key string, value interface{}) {
	s.value[key] = value
	s.delta[key] = value
}
