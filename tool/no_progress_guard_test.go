package tool

import "testing"

func TestNoProgressGuardTriggersOnlyOnConsecutiveIdenticalObservations(t *testing.T) {
	guard := NewNoProgressGuard(2)
	if guard.Observe("lookup", map[string]any{"q": "x"}, "empty") {
		t.Fatal("first observation must not trigger")
	}
	if !guard.Observe("lookup", map[string]any{"q": "x"}, "empty") {
		t.Fatal("second identical observation must trigger")
	}
	if guard.Observe("lookup", map[string]any{"q": "x"}, "found") {
		t.Fatal("changed observation must reset the repeat count")
	}
}

func TestNoProgressGuardCanonicalizesJSONArguments(t *testing.T) {
	guard := NewNoProgressGuard(2)
	if guard.Observe("lookup", map[string]any{"a": 1, "b": 2}, "empty") {
		t.Fatal("first observation must not trigger")
	}
	if !guard.Observe("lookup", map[string]any{"b": 2, "a": 1}, "empty") {
		t.Fatal("equivalent JSON objects must have the same fingerprint")
	}
}

func TestNoProgressGuardReset(t *testing.T) {
	guard := NewNoProgressGuard(2)
	guard.Observe("lookup", nil, nil)
	guard.Reset()
	if guard.Observe("lookup", nil, nil) {
		t.Fatal("reset must clear repeat state")
	}
}
