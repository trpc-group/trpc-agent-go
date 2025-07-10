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
	// Value stores the current committed state
	Value StateMap `json:"value"`
	// Delta stores the pending changes that haven't been committed
	Delta StateMap `json:"delta"`
}

// NewState creates a new empty State.
func NewState() *State {
	return &State{
		Value: make(StateMap),
		Delta: make(StateMap),
	}
}

// Set sets the value of a key in the state.
func (s *State) Set(key string, value []byte) {
	s.Value[key] = value
	s.Delta[key] = value
}

// Get gets the value of a key in the state.
// Will return the delta value if it exists, otherwise the value.
func (s *State) Get(key string) (any, bool) {
	v, ok := s.Delta[key]
	if ok {
		return v, true
	}
	if v, ok = s.Value[key]; ok {
		return v, true
	}
	return nil, false
}
