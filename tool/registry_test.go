package tool

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()

	assert.NotNil(t, registry)
	assert.NotNil(t, registry.toolMap)
	assert.NotNil(t, registry.factories)
	assert.Empty(t, registry.toolMap)
	assert.Empty(t, registry.factories)
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	t.Run("RegisterValidTool", func(t *testing.T) {
		tool := NewBaseTool("test-tool", "A test tool", nil)
		err := registry.Register(tool)

		assert.NoError(t, err)

		// Verify the tool was registered
		registeredTool, exists := registry.toolMap["test-tool"]
		assert.True(t, exists)
		assert.Equal(t, tool, registeredTool)
	})

	t.Run("RegisterNilTool", func(t *testing.T) {
		err := registry.Register(nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be nil")
	})

	t.Run("RegisterEmptyNameTool", func(t *testing.T) {
		tool := NewBaseTool("", "Empty name tool", nil)
		err := registry.Register(tool)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name cannot be empty")
	})

	t.Run("RegisterDuplicateTool", func(t *testing.T) {
		// Register a tool
		tool1 := NewBaseTool("duplicate", "First tool", nil)
		err := registry.Register(tool1)
		assert.NoError(t, err)

		// Try to register another tool with the same name
		tool2 := NewBaseTool("duplicate", "Second tool", nil)
		err = registry.Register(tool2)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}

func TestRegistry_RegisterFactory(t *testing.T) {
	registry := NewRegistry()

	t.Run("RegisterValidFactory", func(t *testing.T) {
		factory := func() (Tool, error) {
			return NewBaseTool("factory-tool", "A factory-created tool", nil), nil
		}

		err := registry.RegisterFactory("factory-tool", factory)

		assert.NoError(t, err)

		// Verify the factory was registered
		registeredFactory, exists := registry.factories["factory-tool"]
		assert.True(t, exists)
		assert.NotNil(t, registeredFactory)
	})

	t.Run("RegisterEmptyNameFactory", func(t *testing.T) {
		factory := func() (Tool, error) {
			return NewBaseTool("some-tool", "A tool", nil), nil
		}

		err := registry.RegisterFactory("", factory)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name cannot be empty")
	})

	t.Run("RegisterNilFactory", func(t *testing.T) {
		err := registry.RegisterFactory("nil-factory", nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "factory cannot be nil")
	})

	t.Run("RegisterDuplicateFactory", func(t *testing.T) {
		// Register a factory
		factory1 := func() (Tool, error) {
			return NewBaseTool("dup-factory-tool", "First factory tool", nil), nil
		}
		err := registry.RegisterFactory("dup-factory", factory1)
		assert.NoError(t, err)

		// Try to register another factory with the same name
		factory2 := func() (Tool, error) {
			return NewBaseTool("dup-factory-tool2", "Second factory tool", nil), nil
		}
		err = registry.RegisterFactory("dup-factory", factory2)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry()

	// Register a direct tool
	directTool := NewBaseTool("direct-tool", "A direct tool", nil)
	_ = registry.Register(directTool)

	// Register a factory that succeeds
	successFactory := func() (Tool, error) {
		return NewBaseTool("factory-tool", "A factory tool", nil), nil
	}
	_ = registry.RegisterFactory("factory-tool", successFactory)

	// Register a factory that fails
	failFactory := func() (Tool, error) {
		return nil, errors.New("factory error")
	}
	_ = registry.RegisterFactory("fail-factory", failFactory)

	t.Run("GetDirectTool", func(t *testing.T) {
		tool, exists := registry.Get("direct-tool")

		assert.True(t, exists)
		assert.Equal(t, directTool, tool)
	})

	t.Run("GetFactoryTool", func(t *testing.T) {
		tool, exists := registry.Get("factory-tool")

		assert.True(t, exists)
		assert.NotNil(t, tool)
		assert.Equal(t, "factory-tool", tool.Name())

		// Verify the tool was cached
		_, ok := registry.toolMap["factory-tool"]
		assert.True(t, ok)
	})

	t.Run("GetFailingFactoryTool", func(t *testing.T) {
		tool, exists := registry.Get("fail-factory")

		assert.False(t, exists)
		assert.Nil(t, tool)

		// Verify the tool was not cached
		_, ok := registry.toolMap["fail-factory"]
		assert.False(t, ok)
	})

	t.Run("GetNonexistentTool", func(t *testing.T) {
		tool, exists := registry.Get("nonexistent")

		assert.False(t, exists)
		assert.Nil(t, tool)
	})
}

func TestRegistry_GetAll(t *testing.T) {
	registry := NewRegistry()

	// Start with an empty registry
	tools := registry.GetAll()
	assert.Empty(t, tools)

	// Add some tools
	tool1 := NewBaseTool("tool1", "Tool 1", nil)
	tool2 := NewBaseTool("tool2", "Tool 2", nil)
	_ = registry.Register(tool1)
	_ = registry.Register(tool2)

	// Test again
	tools = registry.GetAll()
	assert.Len(t, tools, 2)

	// Verify the tools are in the result
	var foundTool1, foundTool2 bool
	for _, tool := range tools {
		if tool.Name() == "tool1" {
			foundTool1 = true
		}
		if tool.Name() == "tool2" {
			foundTool2 = true
		}
	}
	assert.True(t, foundTool1)
	assert.True(t, foundTool2)
}

func TestRegistry_GetNames(t *testing.T) {
	registry := NewRegistry()

	// Start with an empty registry
	names := registry.GetNames()
	assert.Empty(t, names)

	// Add a direct tool
	tool1 := NewBaseTool("tool1", "Tool 1", nil)
	_ = registry.Register(tool1)

	// Add a factory
	_ = registry.RegisterFactory("factory-tool", func() (Tool, error) {
		return NewBaseTool("factory-tool", "Factory Tool", nil), nil
	})

	// Add a factory and create its tool
	_ = registry.RegisterFactory("lazy-tool", func() (Tool, error) {
		return NewBaseTool("lazy-tool", "Lazy Tool", nil), nil
	})
	_, _ = registry.Get("lazy-tool") // This instantiates the lazy tool

	// Test the names
	names = registry.GetNames()
	assert.Len(t, names, 3) // Should include both direct and factory tools

	// Verify the names
	assert.Contains(t, names, "tool1")
	assert.Contains(t, names, "factory-tool")
	assert.Contains(t, names, "lazy-tool")
}

func TestRegistry_Unregister(t *testing.T) {
	registry := NewRegistry()

	// Register some tools and factories
	tool := NewBaseTool("direct-tool", "Direct Tool", nil)
	_ = registry.Register(tool)

	_ = registry.RegisterFactory("factory-tool", func() (Tool, error) {
		return NewBaseTool("factory-tool", "Factory Tool", nil), nil
	})

	// Verify they exist
	_, existsDirect := registry.Get("direct-tool")
	assert.True(t, existsDirect)

	// Unregister the direct tool
	registry.Unregister("direct-tool")

	// Verify it's gone
	_, existsDirect = registry.Get("direct-tool")
	assert.False(t, existsDirect)

	// Unregister the factory
	registry.Unregister("factory-tool")

	// Verify it's gone
	_, existsFactory := registry.Get("factory-tool")
	assert.False(t, existsFactory)

	// Unregister a non-existent tool (should not panic)
	registry.Unregister("nonexistent")
}

func TestRegistry_Clear(t *testing.T) {
	registry := NewRegistry()

	// Register some tools and factories
	tool1 := NewBaseTool("tool1", "Tool 1", nil)
	tool2 := NewBaseTool("tool2", "Tool 2", nil)
	_ = registry.Register(tool1)
	_ = registry.Register(tool2)

	_ = registry.RegisterFactory("factory-tool", func() (Tool, error) {
		return NewBaseTool("factory-tool", "Factory Tool", nil), nil
	})

	// Verify they exist
	assert.Len(t, registry.GetAll(), 2)
	assert.Len(t, registry.GetNames(), 3)

	// Clear the registry
	registry.Clear()

	// Verify it's empty
	assert.Empty(t, registry.GetAll())
	assert.Empty(t, registry.GetNames())
}

func TestRegistry_CreateToolSet(t *testing.T) {
	registry := NewRegistry()

	// Register some tools
	tool1 := NewBaseTool("tool1", "Tool 1", nil)
	tool2 := NewBaseTool("tool2", "Tool 2", nil)
	_ = registry.Register(tool1)
	_ = registry.Register(tool2)

	// Create a tool set
	toolSet := registry.CreateToolSet()

	// Verify the tool set
	assert.NotNil(t, toolSet)
	assert.Equal(t, 2, toolSet.Size())

	// Verify the tools are in the set
	t1, exists1 := toolSet.Get("tool1")
	assert.True(t, exists1)
	assert.Equal(t, tool1, t1)

	t2, exists2 := toolSet.Get("tool2")
	assert.True(t, exists2)
	assert.Equal(t, tool2, t2)
}

func TestDefaultRegistry(t *testing.T) {
	// Test that the default registry exists
	assert.NotNil(t, DefaultRegistry)

	// Test operations on the default registry (just basic operations to ensure it works)
	// First clear it to start fresh
	DefaultRegistry.Clear()

	// Register a tool
	tool := NewBaseTool("default-test-tool", "Test tool in default registry", nil)
	err := DefaultRegistry.Register(tool)
	assert.NoError(t, err)

	// Verify it's there
	retrievedTool, exists := DefaultRegistry.Get("default-test-tool")
	assert.True(t, exists)
	assert.Equal(t, tool, retrievedTool)

	// Clean up
	DefaultRegistry.Clear()
}
