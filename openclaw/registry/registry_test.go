package registry

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func resetForTest(t *testing.T) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	channelFactories = map[string]ChannelFactory{}
	sessionFactories = map[string]SessionBackendFactory{}
	memoryFactories = map[string]MemoryBackendFactory{}
	toolFactories = map[string]ToolProviderFactory{}
	modelFactories = map[string]ModelFactory{}
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest(t)

	called := false
	var nilFactory ModelFactory
	require.Error(t, RegisterModel("TeSt", nilFactory))

	err := RegisterModel("TeSt", func(_ ModelSpec) (model.Model, error) {
		called = true
		return nil, nil
	})
	require.NoError(t, err)

	f, ok := LookupModel("test")
	require.True(t, ok)
	_, err = f(ModelSpec{})
	require.NoError(t, err)
	require.True(t, called)

	err = RegisterModel("test", func(_ ModelSpec) (model.Model, error) {
		return nil, nil
	})
	require.Error(t, err)
}

func TestTypes(t *testing.T) {
	resetForTest(t)

	require.NoError(t, RegisterModel("b", func(_ ModelSpec) (model.Model, error) {
		return nil, nil
	}))
	require.NoError(t, RegisterModel("a", func(_ ModelSpec) (model.Model, error) {
		return nil, nil
	}))

	require.Equal(t, []string{"a", "b"}, Types("model"))
}

func TestDecodeStrict(t *testing.T) {
	type cfg struct {
		A string `yaml:"a"`
	}

	var nodeOK yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("a: x"), &nodeOK))

	var got cfg
	require.NoError(t, DecodeStrict(&nodeOK, &got))
	require.Equal(t, "x", got.A)

	var nodeBad yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("b: x"), &nodeBad))

	require.Error(t, DecodeStrict(&nodeBad, &got))
}
