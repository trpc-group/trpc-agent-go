//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
)

// TestGenerateExampleReport regenerates the committed example report
// session_memory_summary_track_diff_report.json. It replays the eleven
// public cases plus one small order-note case against an in-memory candidate
// with two deterministic faults injected at the service boundary:
//
//   - a silently dropped write (the third event built in every case,
//     matched by its deterministic event ID so the choice is stable under
//     concurrent writers), producing real event/summary-dimension diffs
//     with field paths and both backends' values;
//   - a reversed memory listing, producing the one allowed_diff kind the
//     framework recognizes (memory return order) as a report note.
//
// The injection is deterministic, so regeneration reproduces the committed
// file byte for byte. The test only writes when REPLAY_REPORT_OUT is set:
//
//	cd session/replaytest && REPLAY_REPORT_OUT=session_memory_summary_track_diff_report.json \
//	  go test . -run TestGenerateExampleReport
//
// The comparison is expected to fail cases; that is the point of the
// example, so this test asserts on the report shape instead of passing.
func TestGenerateExampleReport(t *testing.T) {
	out := os.Getenv("REPLAY_REPORT_OUT")
	if out == "" {
		t.Skip("set REPLAY_REPORT_OUT to regenerate the example report")
	}

	ref := replaytest.NewInMemoryTarget("inmemory")
	defer ref.Close()
	inner := replaytest.NewInMemoryTarget("inmemory")
	defer inner.Close()
	cand := &wrapperTarget{
		Target: inner,
		wrap: func(s session.Service) session.Service {
			return &dropIDSuffixService{faultService: faultService{Service: s}, suffix: "-0003"}
		},
		memWrap: func(m memory.Service) memory.Service {
			return &reverseReadService{Service: m}
		},
	}

	ctx := context.Background()
	require.NoError(t, cand.Reset(ctx))
	rep, err := replaytest.RunPair(ctx, exampleCases(), ref, cand,
		replaytest.WithReportPath(out))
	require.NoError(t, err)
	require.Positive(t, rep.Totals.Fail,
		"the injected faults must fail at least one case")
	t.Logf("example report written to %s: %d pass, %d fail, %d unsupported",
		out, rep.Totals.Pass, rep.Totals.Fail, rep.Totals.Unsupported)
}

// exampleCases returns the public suite plus one small memory-only case
// ending with two distinct memories, so the reversed candidate listing
// surfaces the allowed_diff order note on a dedicated, easy-to-read example
// (the note also fires on case 5, whose final snapshot holds three
// memories).
func exampleCases() []replaytest.Case {
	orderNote := replaytest.Case{
		Name: "example/memory_order_note",
		Description: "two distinct memories listed in a different order by " +
			"the candidate; the content set matches, so the return-order " +
			"difference is reported as an allowed_diff note",
		NeedCaps: replaytest.Capability{Memory: true},
		Steps: []replaytest.Step{
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "示例记忆甲：用户喜欢乌龙茶",
				Topics:  []string{"preference", "tea"},
			}},
			{Op: replaytest.OpAddMemory, Memory: &replaytest.MemorySpec{
				Content: "示例记忆乙：用户的项目使用 PostgreSQL",
				Topics:  []string{"fact", "project"},
			}},
		},
	}
	return append(cases.All(), orderNote)
}

// dropIDSuffixService silently drops appends whose deterministic event ID
// ends with the given suffix (the runner assigns IDs as evt-<sid>-<seq>, so
// "-0003" is the third event built in each case). Matching on the ID
// instead of the call count keeps the injection deterministic under
// concurrent writers: goroutine interleaving changes which call arrives
// third, but never which event carries the ID.
type dropIDSuffixService struct {
	faultService
	suffix string
}

// AppendEvent implements session.Service.
func (s *dropIDSuffixService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if strings.HasSuffix(evt.ID, s.suffix) {
		return nil
	}
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

// reverseReadService reverses ReadMemories results, simulating a backend
// whose memory listing order differs from the reference. The content set is
// untouched, so the differ reports the order difference as an allowed note.
type reverseReadService struct {
	memory.Service
}

// ReadMemories implements memory.Service.
func (s *reverseReadService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	entries, err := s.Service.ReadMemories(ctx, userKey, limit)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}
