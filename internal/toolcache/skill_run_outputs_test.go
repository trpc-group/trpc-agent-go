package toolcache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestSkillRunOutputFilesFromContext_Sorted(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	StoreSkillRunOutputFilesFromContext(
		ctx,
		[]codeexecutor.File{
			{Name: "b.txt", Content: "b", MIMEType: "text/plain"},
			{Name: "a.txt", Content: "a", MIMEType: "text/plain"},
		},
	)

	files := SkillRunOutputFilesFromContext(ctx)
	require.Len(t, files, 2)
	require.Equal(t, "a.txt", files[0].Name)
	require.Equal(t, "a", files[0].Content)
	require.Equal(t, "b.txt", files[1].Name)
	require.Equal(t, "b", files[1].Content)
}
