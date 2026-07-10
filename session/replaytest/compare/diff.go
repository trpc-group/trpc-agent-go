package compare

import (
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
)

func MakeDiff(a, b *normalize.SnapShot) map[string]string {
	diff := make(map[string]string)

	if a == b {
		return diff
	}
	if a == nil {
		diff["snapshot"] = "a is nil"
		return diff
	}
	if b == nil {
		diff["snapshot"] = "b is nil"
		return diff
	}

	if a.SessionId != b.SessionId {
		diff["session_id"] = fmt.Sprintf("a: %s, b: %s", a.SessionId, b.SessionId)
	}

	if len(a.Events) != len(b.Events) {
		diff["events_len"] = fmt.Sprintf("a: %d, b: %d", len(a.Events), len(b.Events))
	}

	n := len(a.Events)
	if len(b.Events) < n {
		n = len(b.Events)
	}

	if !reflect.DeepEqual(a.State, b.State) {
		diff["state"] = fmt.Sprintf("a: %+v, b: %+v", a.State, b.State)
	}

	for i := 0; i < n; i++ {
		if a.Events[i] != b.Events[i] {
			diff[fmt.Sprintf("event_%d", i)] = fmt.Sprintf("a: %+v, b: %+v", a.Events[i], b.Events[i])
		}
	}

	return diff
}
