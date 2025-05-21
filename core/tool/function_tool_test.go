package tool

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewFunctionTool(t *testing.T) {
	// Define test executor function
	executor := func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		return "result", nil
	}

	// Define test parameters
	parameters := map[string]interface{}{
		"param1": map[string]interface{}{
			"type":        "string",
			"description": "First parameter",
			"required":    true,
		},
	}

	// Create a new FunctionTool
	tool := NewFunctionTool("test-tool", "A test function tool", parameters, executor)

	// Verify the tool was created correctly
	assert.Equal(t, "test-tool", tool.Name())
	assert.Equal(t, "A test function tool", tool.Description())
	assert.Equal(t, parameters, tool.Parameters())
	assert.NotNil(t, tool.executor)
}

func TestFunctionTool_Execute(t *testing.T) {
	t.Run("SuccessfulExecution", func(t *testing.T) {
		// Define test executor function
		executor := func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			return "result", nil
		}

		// Create a new FunctionTool
		tool := NewFunctionTool("test-tool", "A test function tool", nil, executor)

		// Execute the tool
		result, err := tool.Execute(context.Background(), nil)

		// Verify the execution
		assert.NoError(t, err)
		assert.Equal(t, "result", result.Output)
	})

	t.Run("ExecutionError", func(t *testing.T) {
		// Define test executor function that returns an error
		executor := func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			return nil, errors.New("execution error")
		}

		// Create a new FunctionTool
		tool := NewFunctionTool("test-tool", "A test function tool", nil, executor)

		// Execute the tool
		result, err := tool.Execute(context.Background(), nil)

		// Verify the execution
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, "execution error", err.Error())
	})

	t.Run("NilExecutor", func(t *testing.T) {
		// Create a FunctionTool with nil executor
		tool := &FunctionTool{
			BaseTool: *NewBaseTool("test-tool", "A test function tool", nil),
			executor: nil,
		}

		// Execute the tool
		result, err := tool.Execute(context.Background(), nil)

		// Verify the execution
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "no executor provided")
	})
}

func TestFunctionAdapter(t *testing.T) {
	t.Run("AdapterWithCreateFunctionAdapter", func(t *testing.T) {
		// Function with context and map args
		fn := func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			return "result", nil
		}

		// Adapt the function
		adapter, err := FunctionAdapter(fn)

		// Verify the adapter
		assert.NoError(t, err)
		assert.NotNil(t, adapter)

		// Test the adapter
		result, err := adapter(context.Background(), nil)
		assert.NoError(t, err)
		assert.Equal(t, "result", result)
	})

	t.Run("FunctionWithoutContext", func(t *testing.T) {
		// Function without context
		fn := func(args map[string]interface{}) (interface{}, error) {
			return "no context", nil
		}

		// Adapt the function
		adapter, err := FunctionAdapter(fn)

		// Verify the adapter
		assert.NoError(t, err)
		assert.NotNil(t, adapter)

		// Test the adapter
		result, err := adapter(context.Background(), nil)
		assert.NoError(t, err)
		assert.Equal(t, "no context", result)
	})

	t.Run("FunctionWithTypedArgs", func(t *testing.T) {
		// Function with typed arguments
		fn := func(ctx context.Context, name string, age int) (interface{}, error) {
			return name + ":" + string(rune(age)), nil
		}

		// Adapt the function
		adapter, err := FunctionAdapter(fn)

		// Verify the adapter
		assert.NoError(t, err)
		assert.NotNil(t, adapter)

		// Test the adapter with correct args
		args := map[string]interface{}{
			"arg0": "John",
			"arg1": 65, // ASCII 'A'
		}
		result, err := adapter(context.Background(), args)
		assert.NoError(t, err)
		assert.Equal(t, "John:A", result)
	})

	t.Run("FunctionWithTypedArgsWithoutContext", func(t *testing.T) {
		// Function with typed arguments but no context
		fn := func(name string, age int) (interface{}, error) {
			return name + ":" + string(rune(age)), nil
		}

		// Adapt the function
		adapter, err := FunctionAdapter(fn)

		// Verify the adapter
		assert.NoError(t, err)
		assert.NotNil(t, adapter)

		// Test the adapter with correct args
		args := map[string]interface{}{
			"arg0": "Jane",
			"arg1": 66, // ASCII 'B'
		}
		result, err := adapter(context.Background(), args)
		assert.NoError(t, err)
		assert.Equal(t, "Jane:B", result)
	})

	t.Run("ErrorReturningFunction", func(t *testing.T) {
		// Function that returns an error
		fn := func() (interface{}, error) {
			return nil, errors.New("test error")
		}

		// Adapt the function
		adapter, err := FunctionAdapter(fn)

		// Verify the adapter
		assert.NoError(t, err)
		assert.NotNil(t, adapter)

		// Test the adapter
		result, err := adapter(context.Background(), nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, "test error", err.Error())
	})

	t.Run("NonFunctionInput", func(t *testing.T) {
		// Not a function
		notFn := "not a function"

		// Try to adapt
		adapter, err := FunctionAdapter(notFn)

		// Verify the result
		assert.Error(t, err)
		assert.Nil(t, adapter)
		assert.Contains(t, err.Error(), "expected a function")
	})

	t.Run("FunctionWithNoReturns", func(t *testing.T) {
		// Function with no return values
		fn := func() {}

		// Try to adapt
		adapter, err := FunctionAdapter(fn)

		// Verify the result
		assert.Error(t, err)
		assert.Nil(t, adapter)
		assert.Contains(t, err.Error(), "must return at least one value")
	})

	t.Run("FunctionWithoutErrorReturn", func(t *testing.T) {
		// Function that doesn't return an error
		fn := func() string {
			return "no error"
		}

		// Try to adapt
		adapter, err := FunctionAdapter(fn)

		// Verify the result
		assert.Error(t, err)
		assert.Nil(t, adapter)
		assert.Contains(t, err.Error(), "last return value must be an error")
	})
}

func TestConvertArg(t *testing.T) {
	t.Run("NilValue", func(t *testing.T) {
		// Test with nil value
		targetType := reflect.TypeOf("")
		result, err := convertArg(nil, targetType)

		// Verify the result
		assert.NoError(t, err)
		assert.True(t, result.IsZero())
		assert.Equal(t, "", result.String())
	})

	t.Run("DirectAssignable", func(t *testing.T) {
		// Test with directly assignable types
		targetType := reflect.TypeOf("")
		value := "test"
		result, err := convertArg(value, targetType)

		// Verify the result
		assert.NoError(t, err)
		assert.Equal(t, value, result.Interface())
	})

	t.Run("ConvertToString", func(t *testing.T) {
		// Test conversion to string
		targetType := reflect.TypeOf("")
		value := 123
		result, err := convertArg(value, targetType)

		// Verify the result
		assert.NoError(t, err)
		assert.Equal(t, "123", result.Interface())
	})

	t.Run("ConvertFloatToInt", func(t *testing.T) {
		// Test conversion from float to int
		targetType := reflect.TypeOf(int(0))
		value := 123.45
		result, err := convertArg(value, targetType)

		// Verify the result
		assert.NoError(t, err)
		assert.Equal(t, 123, result.Interface())
	})

	t.Run("ConvertIntToFloat", func(t *testing.T) {
		// Test conversion from int to float
		targetType := reflect.TypeOf(float64(0))
		value := 123
		result, err := convertArg(value, targetType)

		// Verify the result
		assert.NoError(t, err)
		assert.Equal(t, float64(123), result.Interface())
	})

	t.Run("IncompatibleTypes", func(t *testing.T) {
		// Test with incompatible types
		targetType := reflect.TypeOf(struct{}{})
		value := "cannot convert to struct"
		result, err := convertArg(value, targetType)

		// Verify the result
		assert.Error(t, err)
		assert.Equal(t, reflect.Value{}, result)
		assert.Contains(t, err.Error(), "cannot convert")
	})
}
