//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// InjectFault returns a deep-copied snapshot with one deterministic regression.
func InjectFault(input Snapshot, kind FaultKind) (Snapshot, error) {
	output, err := cloneSnapshot(input)
	if err != nil {
		return Snapshot{}, err
	}
	var inject func(*Snapshot) error
	switch kind {
	case FaultEventContent:
		inject = injectEventContent
	case FaultEventOrder:
		inject = injectEventOrder
	case FaultToolArguments:
		inject = injectToolArguments
	case FaultStateValue:
		inject = injectStateValue
	case FaultMemoryContent:
		inject = injectMemoryContent
	case FaultSummaryText:
		inject = injectSummaryText
	case FaultSummaryMissing:
		inject = injectSummaryMissing
	case FaultSummaryFilterKey:
		inject = injectSummaryFilterKey
	case FaultTrackPayload:
		inject = injectTrackPayload
	case FaultDuplicateEvent:
		inject = injectDuplicateEvent
	default:
		return Snapshot{}, fmt.Errorf("unknown fault %q", kind)
	}
	if err := inject(&output); err != nil {
		return Snapshot{}, err
	}
	output.Backend += "-faulted"
	return output, nil
}

func injectEventContent(output *Snapshot) error {
	message, err := firstEventMessage(output.Events)
	if err != nil {
		return err
	}
	message["content"] = "injected-content-drift"
	return nil
}

func injectEventOrder(output *Snapshot) error {
	if len(output.Events) < 2 {
		return errors.New("event order fault requires two events")
	}
	output.Events[0], output.Events[1] = output.Events[1], output.Events[0]
	return nil
}

func injectToolArguments(output *Snapshot) error {
	return mutateToolArguments(output.Events)
}

func injectStateValue(output *Snapshot) error {
	return mutateState(output.State)
}

func injectMemoryContent(output *Snapshot) error {
	if len(output.Memories) == 0 {
		return errors.New("memory fault requires a memory")
	}
	memoryValue, ok := output.Memories[0]["memory"].(map[string]any)
	if !ok {
		return errors.New("memory payload is missing")
	}
	memoryValue["memory"] = "injected-memory-drift"
	return nil
}

func injectSummaryText(output *Snapshot) error {
	summary, err := firstSummary(output.Summaries)
	if err != nil {
		return err
	}
	summary["text"] = "injected-summary-drift"
	return nil
}

func injectSummaryMissing(output *Snapshot) error {
	key, _, err := firstSummaryEntry(output.Summaries)
	if err != nil {
		return err
	}
	delete(output.Summaries, key)
	return nil
}

func injectSummaryFilterKey(output *Snapshot) error {
	key, summary, err := firstSummaryEntry(output.Summaries)
	if err != nil {
		return err
	}
	delete(output.Summaries, key)
	output.Summaries["wrong/filter"] = summary
	return nil
}

func injectTrackPayload(output *Snapshot) error {
	trackEvent, err := firstTrackEvent(output.Tracks)
	if err != nil {
		return err
	}
	payload, ok := trackEvent["payload"].(map[string]any)
	if !ok {
		return errors.New("track payload is missing")
	}
	payload["status"] = "injected-track-drift"
	return nil
}

func injectDuplicateEvent(output *Snapshot) error {
	if len(output.Events) == 0 {
		return errors.New("duplicate fault requires an event")
	}
	output.Events = append(output.Events, cloneJSONMap(output.Events[0]))
	return nil
}

func cloneSnapshot(input Snapshot) (Snapshot, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Snapshot{}, err
	}
	var output Snapshot
	if err := decodeJSON(raw, &output); err != nil {
		return Snapshot{}, err
	}
	return output, nil
}

func firstEventMessage(events []CanonicalMap) (map[string]any, error) {
	for _, evt := range events {
		choices, ok := evt["choices"].([]any)
		if !ok {
			continue
		}
		for _, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]any)
			if !ok {
				continue
			}
			message, ok := choice["message"].(map[string]any)
			if ok {
				return message, nil
			}
		}
	}
	return nil, errors.New("event content fault requires a message")
}

func mutateToolArguments(events []CanonicalMap) error {
	for _, evt := range events {
		choices, _ := evt["choices"].([]any)
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]any)
			message, _ := choice["message"].(map[string]any)
			calls, _ := message["tool_calls"].([]any)
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, ok := call["function"].(map[string]any)
				if !ok {
					continue
				}
				function["arguments"] = `{"city":"wrong"}`
				return nil
			}
		}
	}
	return errors.New("tool argument fault requires a tool call")
}

func mutateState(state map[string]map[string]string) error {
	for _, scope := range []string{"session", "user", "app"} {
		values := state[scope]
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			values[keys[0]] = `"injected-state-drift"`
			return nil
		}
	}
	return errors.New("state fault requires a state value")
}

func firstSummary(summaries map[string]CanonicalMap) (CanonicalMap, error) {
	_, summary, err := firstSummaryEntry(summaries)
	return summary, err
}

func firstSummaryEntry(summaries map[string]CanonicalMap) (string, CanonicalMap, error) {
	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		if summaries[key] == nil {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", nil, errors.New("summary fault requires a summary")
	}
	return keys[0], summaries[keys[0]], nil
}

func firstTrackEvent(tracks map[string][]CanonicalMap) (CanonicalMap, error) {
	keys := make([]string, 0, len(tracks))
	for key := range tracks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(tracks[key]) > 0 {
			return tracks[key][0], nil
		}
	}
	return nil, errors.New("track fault requires a track event")
}
