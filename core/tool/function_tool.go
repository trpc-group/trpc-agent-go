package tool

import (
	"context"
	"fmt"
	"reflect"
)

// FunctionToolExecutor is a function that takes a context and arguments map and returns
// a result and an error.
type FunctionToolExecutor func(ctx context.Context, args map[string]interface{}) (interface{}, error)

// FunctionTool is a tool that executes a function.
type FunctionTool struct {
	BaseTool
	executor FunctionToolExecutor
}

// NewFunctionTool creates a new function tool with the given name, description, and executor.
func NewFunctionTool(name, description string, parameters map[string]interface{}, executor FunctionToolExecutor) *FunctionTool {
	return &FunctionTool{
		BaseTool: *NewBaseTool(name, description, parameters),
		executor: executor,
	}
}

// Execute executes the function with the given arguments.
func (t *FunctionTool) Execute(ctx context.Context, args map[string]interface{}) (*Result, error) {
	if t.executor == nil {
		return nil, fmt.Errorf("no executor provided for tool %s", t.Name())
	}

	output, err := t.executor(ctx, args)
	if err != nil {
		return nil, err
	}

	return NewResult(output), nil
}

// FunctionAdapter creates a FunctionToolExecutor from various function signatures.
// The function should have the following signature:
// - func(ctx context.Context, args map[string]interface{}) (interface{}, error)
// - func(args map[string]interface{}) (interface{}, error)
// - func(ctx context.Context, arg1 T1, arg2 T2, ...) (interface{}, error)
// - func(arg1 T1, arg2 T2, ...) (interface{}, error)
func FunctionAdapter(fn interface{}) (FunctionToolExecutor, error) {
	// Check if fn is a function
	fnVal := reflect.ValueOf(fn)
	if fnVal.Kind() != reflect.Func {
		return nil, fmt.Errorf("expected a function, got %T", fn)
	}

	fnType := fnVal.Type()
	numIn := fnType.NumIn()
	numOut := fnType.NumOut()

	// Check if the function returns at least one value
	if numOut < 1 {
		return nil, fmt.Errorf("function must return at least one value")
	}

	// Check if the last return value is an error
	if !fnType.Out(numOut-1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return nil, fmt.Errorf("last return value must be an error")
	}

	// Check for special case: already matches FunctionToolExecutor signature
	if numIn == 2 && fnType.In(0) == reflect.TypeOf((*context.Context)(nil)).Elem() &&
		fnType.In(1) == reflect.TypeOf(map[string]interface{}{}) &&
		numOut == 2 && fnType.Out(0).Kind() == reflect.Interface {
		
		// Instead of directly casting, create a wrapper function
		return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			// Call the original function using reflection
			out := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(args)})
			
			// Handle errors
			if !out[1].IsNil() {
				return nil, out[1].Interface().(error)
			}
			
			return out[0].Interface(), nil
		}, nil
	}

	// Create an adapter based on the function signature
	return createFunctionAdapter(fn, fnVal, fnType, numIn, numOut)
}

func createFunctionAdapter(fn interface{}, fnVal reflect.Value, fnType reflect.Type, numIn, numOut int) (FunctionToolExecutor, error) {
	// Check if first parameter is context.Context
	hasContext := numIn > 0 && fnType.In(0) == reflect.TypeOf((*context.Context)(nil)).Elem()

	// Create an adapter function
	return func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
		// Prepare input arguments
		in := make([]reflect.Value, numIn)
		
		// Set context if the function expects it
		argIndex := 0
		if hasContext {
			in[0] = reflect.ValueOf(ctx)
			argIndex = 1
		}

		// If there's only one arg after context and it's a map, pass the args directly
		if argIndex < numIn && numIn-argIndex == 1 && fnType.In(argIndex) == reflect.TypeOf(map[string]interface{}{}) {
			in[argIndex] = reflect.ValueOf(args)
		} else {
			// Otherwise, extract and convert the args based on parameter names
			for i := argIndex; i < numIn; i++ {
				paramType := fnType.In(i)
				paramName := fmt.Sprintf("arg%d", i-argIndex)
				
				// If the arg exists in the map
				if argVal, ok := args[paramName]; ok {
					// Try to convert the arg to the expected type
					val, err := convertArg(argVal, paramType)
					if err != nil {
						return nil, fmt.Errorf("error converting argument %s: %w", paramName, err)
					}
					in[i] = val
				} else {
					// If the arg is missing, use the zero value
					in[i] = reflect.Zero(paramType)
				}
			}
		}

		// Call the function
		out := fnVal.Call(in)

		// Handle the return values
		if len(out) == 0 {
			return nil, nil
		}

		// Check for error
		if err := out[numOut-1]; !err.IsNil() {
			return nil, err.Interface().(error)
		}

		// Return result
		if numOut == 1 { // Only error
			return nil, nil
		}
		return out[0].Interface(), nil
	}, nil
}

// convertArg attempts to convert a value to the expected type
func convertArg(value interface{}, targetType reflect.Type) (reflect.Value, error) {
	if value == nil {
		return reflect.Zero(targetType), nil
	}

	sourceValue := reflect.ValueOf(value)
	sourceType := sourceValue.Type()

	// If the source type is directly assignable to the target type
	if sourceType.AssignableTo(targetType) {
		return sourceValue, nil
	}

	// Try to convert between basic types
	switch targetType.Kind() {
	case reflect.String:
		return reflect.ValueOf(fmt.Sprintf("%v", value)), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Convert float to int if possible
		if sourceValue.Kind() == reflect.Float64 {
			return reflect.ValueOf(int64(sourceValue.Float())).Convert(targetType), nil
		}
	case reflect.Float32, reflect.Float64:
		// Convert int to float if possible
		if sourceValue.Kind() >= reflect.Int && sourceValue.Kind() <= reflect.Int64 {
			return reflect.ValueOf(float64(sourceValue.Int())).Convert(targetType), nil
		}
	}

	return reflect.Value{}, fmt.Errorf("cannot convert %T to %s", value, targetType)
} 