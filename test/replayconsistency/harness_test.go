//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The comparator, the normalizer and the allow list decide what the whole
// suite can see, so they are tested directly. A bug in any of them makes every
// case above report agreement it never verified.

func TestCanonicalJSONIgnoresMemberOrder(t *testing.T) {
	a := canonicalJSON([]byte(`{"b":1,"a":{"d":4,"c":3}}`))
	b := canonicalJSON([]byte(`{"a":{"c":3,"d":4},"b":1}`))
	if a != b {
		t.Errorf("member order changed the canonical form:\n %s\n %s", a, b)
	}
}

func TestCanonicalJSONDistinguishesValues(t *testing.T) {
	if canonicalJSON([]byte(`{"a":1}`)) == canonicalJSON([]byte(`{"a":2}`)) {
		t.Error("different values produced the same canonical form")
	}
}

func TestCanonicalJSONPassesThroughInvalidInput(t *testing.T) {
	// A backend that corrupts a payload must surface as a payload difference,
	// not as a harness error that aborts the run.
	const broken = `{"a":`
	if got := canonicalJSON([]byte(broken)); got != broken {
		t.Errorf("invalid JSON = %q, want it returned verbatim", got)
	}
}

func TestOffsetFromDistinguishesZeroFromBase(t *testing.T) {
	base := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	if got := offsetFrom(time.Time{}, base); got != "" {
		t.Errorf("zero instant = %q, want empty so a dropped timestamp stays visible", got)
	}
	if got := offsetFrom(base, base); got != "0s" {
		t.Errorf("base instant = %q, want 0s", got)
	}
}

func TestOffsetFromTruncatesBelowPrecision(t *testing.T) {
	base := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	a := offsetFrom(base.Add(time.Second+400*time.Microsecond), base)
	b := offsetFrom(base.Add(time.Second+900*time.Microsecond), base)
	if a != b {
		t.Errorf("sub-millisecond difference survived normalization: %q vs %q", a, b)
	}
	if a != "1s" {
		t.Errorf("offset = %q, want 1s", a)
	}
}

func TestStateEntriesDistinguishNilFromEmpty(t *testing.T) {
	got := stateEntries(map[string][]byte{"nil": nil, "empty": []byte("")})
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	// Sorted by key: "empty" then "nil".
	if got[0].Key != "empty" || got[0].Nil {
		t.Errorf("empty value marked nil: %+v", got[0])
	}
	if got[1].Key != "nil" || !got[1].Nil {
		t.Errorf("nil value not marked nil: %+v", got[1])
	}
}

func TestStateEntriesAreOrderStable(t *testing.T) {
	in := map[string][]byte{"c": []byte("3"), "a": []byte("1"), "b": []byte("2")}
	first := stateEntries(in)
	for i := 0; i < 50; i++ {
		if got := stateEntries(in); !equalStateEntries(first, got) {
			t.Fatalf("map iteration order leaked into the projection: %+v vs %+v", first, got)
		}
	}
}

func equalStateEntries(a, b []StateEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCompareReportsNothingForIdenticalObservations(t *testing.T) {
	obs := sampleObservation("inmemory")
	other := sampleObservation("sqlite")
	if got := Compare("case", obs, other); len(got) != 0 {
		t.Errorf("identical observations produced %d divergences: %+v", len(got), got)
	}
}

func TestCompareIgnoresOnlyTheBackendName(t *testing.T) {
	// The backend field is the single tagged exclusion. If the tag were ever
	// applied more widely this test starts failing, which is the point.
	obs := sampleObservation("inmemory")
	other := sampleObservation("sqlite")
	other.Sessions[0].Events[0].Content = "changed"
	got := Compare("case", obs, other)
	if len(got) != 1 {
		t.Fatalf("got %d divergences, want 1: %+v", len(got), got)
	}
	if !strings.HasSuffix(got[0].Path, ".content") {
		t.Errorf("path = %q, want it to end at the changed field", got[0].Path)
	}
}

func TestCompareLocatesEachEntityKind(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Observation)
		wantPath string
	}{
		{
			name:     "event by index",
			mutate:   func(o *Observation) { o.Sessions[0].Events[0].ID = "other" },
			wantPath: `sessions[ref="app/u/s"].events[0].id`,
		},
		{
			name:     "summary by filter key",
			mutate:   func(o *Observation) { o.Sessions[0].Summaries[0].Text = "other" },
			wantPath: `summaries[filterKey="branch"].text`,
		},
		{
			name:     "track by name",
			mutate:   func(o *Observation) { o.Sessions[0].Tracks[0].Events[0].Payload = `{"x":2}` },
			wantPath: `tracks[track="timing"].events[0].payload`,
		},
		{
			name:     "memory by identifier",
			mutate:   func(o *Observation) { o.Memories.Entries[0].Content = "other" },
			wantPath: `memories.entries[id="mem-1"].content`,
		},
		{
			name:     "state by key",
			mutate:   func(o *Observation) { o.Sessions[0].State[0].Value = "other" },
			wantPath: `state[key="k"].value`,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			base := sampleObservation("inmemory")
			other := sampleObservation("sqlite")
			tt.mutate(other)
			got := Compare("case", base, other)
			if len(got) == 0 {
				t.Fatal("mutation produced no divergence")
			}
			if !strings.Contains(got[0].Path, tt.wantPath) {
				t.Errorf("path = %q, want it to contain %q", got[0].Path, tt.wantPath)
			}
		})
	}
}

func TestCompareReportsMissingKeyedElementRatherThanShifting(t *testing.T) {
	base := sampleObservation("inmemory")
	other := sampleObservation("sqlite")
	other.Sessions[0].Summaries = nil

	got := Compare("case", base, other)
	if len(got) != 1 {
		t.Fatalf("dropping a summary produced %d divergences, want 1: %+v", len(got), got)
	}
	if got[0].BackendValue != "<absent>" {
		t.Errorf("backend value = %q, want <absent>", got[0].BackendValue)
	}
	if !strings.Contains(got[0].Path, `summaries[filterKey="branch"]`) {
		t.Errorf("path = %q, want it to name the missing filter key", got[0].Path)
	}
}

func TestCompareReportsIndexedLengthChange(t *testing.T) {
	base := sampleObservation("inmemory")
	other := sampleObservation("sqlite")
	other.Sessions[0].Events = other.Sessions[0].Events[:0]

	var sawLength bool
	for _, d := range Compare("case", base, other) {
		if strings.HasSuffix(d.Path, "events.length") {
			sawLength = true
		}
	}
	if !sawLength {
		t.Error("a dropped event did not produce an events.length divergence")
	}
}

func TestCompareDetectsAddedFieldsAutomatically(t *testing.T) {
	// The fail-closed property: the comparator walks the projection by
	// reflection, so a field added to a view participates without anyone
	// remembering to extend a comparison function. OwnerRef is compared for
	// exactly that reason, and a summary surfacing under the wrong session is
	// only visible because of it.
	base := sampleObservation("inmemory")
	other := sampleObservation("sqlite")
	other.Sessions[0].Summaries[0].OwnerRef = "app/u/other-session"

	got := Compare("case", base, other)
	if len(got) != 1 || !strings.HasSuffix(got[0].Path, ".ownerRef") {
		t.Fatalf("summary ownership change not reported as a single ownerRef divergence: %+v", got)
	}
}

func TestAllowedDiffMatchingIsAnchored(t *testing.T) {
	tests := []struct {
		path string
		rule string
		want bool
	}{
		{"memories.readOrder", "memories.readOrder", true},
		{"memories.readOrder[0]", "memories.readOrder", true},
		{"memories.readOrder.length", "memories.readOrder", true},
		// A rule must not swallow a longer sibling name.
		{"memories.readOrderExtra", "memories.readOrder", false},
		{"memories.entries", "memories.readOrder", false},
	}
	for _, tt := range tests {
		if got := matchesRulePath(tt.path, tt.rule); got != tt.want {
			t.Errorf("matchesRulePath(%q, %q) = %v, want %v", tt.path, tt.rule, got, tt.want)
		}
	}
}

func TestClassifiedDifferencesAreNotFatal(t *testing.T) {
	allowed := Divergence{AllowedDiff: true}
	known := Divergence{Known: true}
	fresh := Divergence{}
	if allowed.Fatal() || known.Fatal() {
		t.Error("a classified difference should not fail a run")
	}
	if !fresh.Fatal() {
		t.Error("an unclassified difference must fail a run")
	}
}

func TestEveryClassificationCarriesAReason(t *testing.T) {
	for _, r := range AllowedDiffRules() {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("allowed diff rule %q has no reason", r.Path)
		}
	}
	for _, k := range KnownDivergences() {
		if strings.TrimSpace(k.Note) == "" {
			t.Errorf("known divergence %q has no note", k.Path)
		}
		if k.Backend == "" {
			t.Errorf("known divergence %q names no backend, so it would apply to all of them", k.Path)
		}
	}
}

func TestEventSpecRejectsInvalidJSON(t *testing.T) {
	base := time.Now()
	_, err := EventSpec{ID: "e", Role: "assistant", Extensions: map[string]string{"k": "{"}}.Build(base)
	if err == nil {
		t.Error("invalid extension JSON was accepted")
	}
	_, err = EventSpec{
		ID: "e", Role: "assistant",
		ToolCalls: []ToolCallSpec{{ID: "c", Name: "n", Arguments: "{"}},
	}.Build(base)
	if err == nil {
		t.Error("invalid tool call arguments were accepted")
	}
}

func TestEventSpecRejectsUnknownRole(t *testing.T) {
	if _, err := (EventSpec{ID: "e", Role: "narrator"}).Build(time.Now()); err == nil {
		t.Error("unknown role was accepted")
	}
}

func TestScenariosAreDistinctlyNamed(t *testing.T) {
	seen := make(map[string]struct{})
	for _, sc := range Scenarios() {
		if _, dup := seen[sc.Name]; dup {
			t.Errorf("duplicate scenario name %q would collide in the report", sc.Name)
		}
		seen[sc.Name] = struct{}{}
		if len(sc.Sessions) == 0 {
			t.Errorf("scenario %q observes no sessions, so it can compare nothing", sc.Name)
		}
		if sc.Description == "" {
			t.Errorf("scenario %q has no description", sc.Name)
		}
	}
	if len(seen) < 10 {
		t.Errorf("got %d public cases, want at least 10", len(seen))
	}
}

func TestReportMarshalRoundTrip(t *testing.T) {
	r := &Report{
		Mode:     "lightweight",
		Baseline: "inmemory",
		Backends: []string{"inmemory", "sqlite"},
		Summary:  Counts{Cases: 1, Divergences: 1, Allowed: 1},
		Cases: []ReportCase{{
			Case:        "c",
			Divergences: []Divergence{{Path: "p", AllowedDiff: true, Reason: "because"}},
		}},
	}
	data, err := r.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("report should end with a newline")
	}
	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Cases[0].Divergences[0].Reason != "because" {
		t.Error("explanation lost in the round trip")
	}
}

// sampleObservation builds a projection covering every entity kind the
// comparator knows how to locate.
func sampleObservation(backend string) *Observation {
	return &Observation{
		Backend: backend,
		Sessions: []SessionView{{
			Ref:    "app/u/s",
			Exists: true,
			State:  []StateEntry{{Key: "k", Value: "v"}},
			Events: []EventView{{
				Index: 0, ID: "e1", Author: "user", Role: "user", Content: "hello",
				ToolCalls: []ToolCallView{{ID: "c1", Name: "search", Arguments: `{"q":"x"}`}},
			}},
			Summaries: []SummaryView{{
				FilterKey: "branch", Text: "text", OwnerRef: "app/u/s",
			}},
			Tracks: []TrackView{{
				Track:  "timing",
				Events: []TrackEventView{{Index: 0, Payload: `{"x":1}`}},
			}},
		}},
		Memories: &MemoryView{
			Ref:       "app/u",
			Entries:   []MemoryEntryView{{ID: "mem-1", Content: "c", Topics: []string{"t"}}},
			ReadOrder: []string{"mem-1"},
		},
	}
}
