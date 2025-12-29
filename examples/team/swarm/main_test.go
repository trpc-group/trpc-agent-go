package main

import "testing"

func TestBuildRunner_OK(t *testing.T) {
	runnerInstance, err := buildRunner(
		defaultModelName,
		defaultVariant,
		false,
	)
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	_ = runnerInstance.Close()
}
