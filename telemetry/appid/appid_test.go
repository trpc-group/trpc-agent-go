//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package appid

import (
	"testing"
)

// containsPair reports whether infos has an entry with the given app
// and agent values.
func containsPair(infos []Info, app, agent string) bool {
	for _, it := range infos {
		if it.App == app && it.Agent == agent {
			return true
		}
	}
	return false
}

func TestRegisterDefaultsAndRunners(t *testing.T) {
	// Before any registration, defaults fall back to the process name.
	if DefaultApp() == "" {
		t.Fatalf("DefaultApp should not be empty before register")
	}
	if DefaultAgent() == "" {
		t.Fatalf("DefaultAgent should not be empty before register")
	}

	// First registration sets defaults.
	RegisterRunner("myapp", "myagent")
	if got := DefaultApp(); got != "myapp" {
		t.Fatalf("DefaultApp = %q, want %q", got, "myapp")
	}
	if got := DefaultAgent(); got != "myagent" {
		t.Fatalf("DefaultAgent = %q, want %q", got, "myagent")
	}

	// Later registrations do not change defaults.
	RegisterRunner("app2", "agent2")
	if got := DefaultApp(); got != "myapp" {
		t.Fatalf("DefaultApp changed to %q, want %q", got, "myapp")
	}
	if got := DefaultAgent(); got != "myagent" {
		t.Fatalf("DefaultAgent changed to %q, want %q", got, "myagent")
	}

	// Runners should contain at least the pairs we registered.
	got := Runners()
	if !containsPair(got, "myapp", "myagent") {
		t.Fatalf("Runners missing (myapp,myagent): %#v", got)
	}
	if !containsPair(got, "app2", "agent2") {
		t.Fatalf("Runners missing (app2,agent2): %#v", got)
	}
}
