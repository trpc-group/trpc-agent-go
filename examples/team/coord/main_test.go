package main

import "testing"

func TestBuildRunner_InvalidMemberHistory(t *testing.T) {
	runnerInstance, err := buildRunner(
		defaultModelName,
		defaultVariant,
		false,
		false,
		"unknown",
		false,
		false,
	)
	if err == nil {
		_ = runnerInstance.Close()
		t.Fatal("expected error, got nil")
	}
}

func TestBuildRunner_OK(t *testing.T) {
	runnerInstance, err := buildRunner(
		defaultModelName,
		defaultVariant,
		false,
		false,
		memberHistoryParent,
		false,
		false,
	)
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	_ = runnerInstance.Close()
}
