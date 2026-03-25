//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package programsession

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type fakeProgramSession struct {
	polls []codeexecutor.ProgramPoll
	idx   int
}

func (f *fakeProgramSession) ID() string { return "sess" }

func (f *fakeProgramSession) Poll(_ *int) codeexecutor.ProgramPoll {
	if len(f.polls) == 0 {
		return codeexecutor.ProgramPoll{}
	}
	if f.idx >= len(f.polls) {
		return f.polls[len(f.polls)-1]
	}
	p := f.polls[f.idx]
	f.idx++
	return p
}

func (*fakeProgramSession) Log(*int, *int) codeexecutor.ProgramLog {
	return codeexecutor.ProgramLog{}
}

func (*fakeProgramSession) Write(string, bool) error { return nil }
func (*fakeProgramSession) Kill(time.Duration) error { return nil }
func (*fakeProgramSession) Close() error             { return nil }

func TestWaitForProgramOutput_CollectsChunksUntilExit(t *testing.T) {
	exitCode := 0
	proc := &fakeProgramSession{
		polls: []codeexecutor.ProgramPoll{
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "hello ",
				Offset:     0,
				NextOffset: 6,
			},
			{
				Status:     codeexecutor.ProgramStatusExited,
				Output:     "world",
				Offset:     6,
				NextOffset: 11,
				ExitCode:   &exitCode,
			},
		},
	}

	poll := WaitForProgramOutput(proc, 20*time.Millisecond, PollLineLimit(0))
	require.Equal(t, codeexecutor.ProgramStatusExited, poll.Status)
	require.Equal(t, "hello world", poll.Output)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 11, poll.NextOffset)
	require.NotNil(t, poll.ExitCode)
	require.Equal(t, 0, *poll.ExitCode)
}

func TestWaitForProgramOutput_SettlesAfterOutputWithoutExit(t *testing.T) {
	proc := &fakeProgramSession{
		polls: []codeexecutor.ProgramPoll{
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "chunk",
				Offset:     4,
				NextOffset: 9,
			},
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "",
				Offset:     9,
				NextOffset: 9,
			},
		},
	}

	start := time.Now()
	poll := WaitForProgramOutput(proc, 0, PollLineLimit(5))
	require.Equal(t, codeexecutor.ProgramStatusRunning, poll.Status)
	require.Equal(t, "chunk", poll.Output)
	require.Equal(t, 4, poll.Offset)
	require.Equal(t, 9, poll.NextOffset)
	require.False(t, time.Since(start) < DefaultPollSettle)
}

func TestWaitForProgramOutput_RefreshesSettleWindowForContinuedOutput(t *testing.T) {
	proc := &fakeProgramSession{
		polls: []codeexecutor.ProgramPoll{
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "a",
				Offset:     0,
				NextOffset: 1,
			},
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "b",
				Offset:     1,
				NextOffset: 2,
			},
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "c",
				Offset:     2,
				NextOffset: 3,
			},
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "d",
				Offset:     3,
				NextOffset: 4,
			},
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "",
				Offset:     4,
				NextOffset: 4,
			},
		},
	}

	poll := WaitForProgramOutput(proc, 0, PollLineLimit(5))
	require.Equal(t, codeexecutor.ProgramStatusRunning, poll.Status)
	require.Equal(t, "abcd", poll.Output)
	require.Equal(t, 0, poll.Offset)
	require.Equal(t, 4, poll.NextOffset)
}

func TestYieldDurationAndPollLineLimit(t *testing.T) {
	require.Equal(
		t,
		250*time.Millisecond,
		YieldDuration(0, 250),
	)
	require.Equal(
		t,
		250*time.Millisecond,
		YieldDuration(-10, 250),
	)

	limit := PollLineLimit(0)
	require.NotNil(t, limit)
	require.Equal(t, DefaultPollLines, *limit)

	limit = PollLineLimit(7)
	require.NotNil(t, limit)
	require.Equal(t, 7, *limit)
}

type fakeStateProgramSession struct {
	fakeProgramSession
	state codeexecutor.ProgramState
}

func (f *fakeStateProgramSession) State() codeexecutor.ProgramState {
	return f.state
}

func TestState(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		state, ok := State(&fakeProgramSession{})
		require.False(t, ok)
		require.Equal(t, codeexecutor.ProgramState{}, state)
	})

	t.Run("provider", func(t *testing.T) {
		exitCode := 7
		want := codeexecutor.ProgramState{
			Status:   codeexecutor.ProgramStatusExited,
			ExitCode: &exitCode,
		}
		state, ok := State(&fakeStateProgramSession{state: want})
		require.True(t, ok)
		require.Equal(t, want.Status, state.Status)
		require.NotNil(t, state.ExitCode)
		require.Equal(t, exitCode, *state.ExitCode)
	})
}

func TestWaitForProgramOutput_ReturnsOnYieldDeadlineWithoutOutput(t *testing.T) {
	proc := &fakeProgramSession{
		polls: []codeexecutor.ProgramPoll{
			{
				Status:     codeexecutor.ProgramStatusRunning,
				Output:     "",
				Offset:     5,
				NextOffset: 5,
			},
		},
	}

	poll := WaitForProgramOutput(proc, 10*time.Millisecond, nil)
	require.Equal(t, codeexecutor.ProgramStatusRunning, poll.Status)
	require.Empty(t, poll.Output)
	require.Equal(t, 5, poll.Offset)
	require.Equal(t, 5, poll.NextOffset)
}
