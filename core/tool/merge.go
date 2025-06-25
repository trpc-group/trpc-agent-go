package tool

import (
	"reflect"
	"strings"
)

// Mergeable interface for custom types that want to define their own merging logic
type Mergeable interface {
	Merge(other any) any
}

// Merge merges a slice of items of the same type
// Supports: strings, numbers, byte slices, slices, arrays, maps, structs, and custom Mergeable types
func Merge[T any](ts []T) T {
	var zero T

	if len(ts) == 0 {
		return zero
	}

	if len(ts) == 1 {
		return ts[0]
	}

	// Handle the first element to determine the type and operation
	first := ts[0]
	firstValue := reflect.ValueOf(first)
	firstType := firstValue.Type()

	// Check if type implements Mergeable interface
	if _, ok := any(first).(Mergeable); ok {
		result := any(first)
		for i := 1; i < len(ts); i++ {
			if mergeable, ok := result.(Mergeable); ok {
				result = mergeable.Merge(ts[i])
			}
		}
		return result.(T)
	}

	switch firstType.Kind() {
	case reflect.String:
		return mergeStrings(ts)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return mergeInts(ts)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return mergeUints(ts)

	case reflect.Float32, reflect.Float64:
		return mergeFloats(ts)

	case reflect.Slice:
		return mergeSlices(ts)

	case reflect.Array:
		return mergeArrays(ts)

	case reflect.Map:
		return mergeMaps(ts)

	case reflect.Struct:
		return mergeStructs(ts)

	default:
		// For unsupported types, return the first element
		return first
	}
}

// mergeStrings concatenates string values
func mergeStrings[T any](ts []T) T {
	var builder strings.Builder
	for _, t := range ts {
		if str, ok := any(t).(string); ok {
			builder.WriteString(str)
		}
	}
	return any(builder.String()).(T)
}

// mergeInts sums integer values
func mergeInts[T any](ts []T) T {
	var sum int64
	for _, t := range ts {
		val := reflect.ValueOf(t)
		sum += val.Int()
	}

	// Convert back to the original type
	resultVal := reflect.ValueOf(sum).Convert(reflect.TypeOf(ts[0]))
	return resultVal.Interface().(T)
}

// mergeUints sums unsigned integer values
func mergeUints[T any](ts []T) T {
	var sum uint64
	for _, t := range ts {
		val := reflect.ValueOf(t)
		sum += val.Uint()
	}

	// Convert back to the original type
	resultVal := reflect.ValueOf(sum).Convert(reflect.TypeOf(ts[0]))
	return resultVal.Interface().(T)
}

// mergeFloats sums floating point values
func mergeFloats[T any](ts []T) T {
	var sum float64
	for _, t := range ts {
		val := reflect.ValueOf(t)
		sum += val.Float()
	}

	// Convert back to the original type
	resultVal := reflect.ValueOf(sum).Convert(reflect.TypeOf(ts[0]))
	return resultVal.Interface().(T)
}

// mergeSlices concatenates slice values
func mergeSlices[T any](ts []T) T {
	if len(ts) == 0 {
		var zero T
		return zero
	}

	firstVal := reflect.ValueOf(ts[0])
	elemType := firstVal.Type().Elem()

	// Handle special case for byte slices
	if elemType.Kind() == reflect.Uint8 {
		var result []byte
		for _, t := range ts {
			if bytes, ok := any(t).([]byte); ok {
				result = append(result, bytes...)
			}
		}
		return any(result).(T)
	}

	// Generic slice concatenation
	result := reflect.MakeSlice(firstVal.Type(), 0, 0)
	for _, t := range ts {
		val := reflect.ValueOf(t)
		if val.Kind() == reflect.Slice {
			for i := 0; i < val.Len(); i++ {
				result = reflect.Append(result, val.Index(i))
			}
		}
	}

	return result.Interface().(T)
}

// mergeArrays concatenates array values into a new array
func mergeArrays[T any](ts []T) T {
	if len(ts) == 0 {
		var zero T
		return zero
	}
	// Note: Arrays are fixed size, so we assume all arrays in ts are of the same type and size.
	// Plsease use slices if you need dynamic size.
	return ts[0]
}

// mergeMaps merges map values
func mergeMaps[T any](ts []T) T {
	if len(ts) == 0 {
		var zero T
		return zero
	}

	firstVal := reflect.ValueOf(ts[0])
	mapType := firstVal.Type()
	result := reflect.MakeMap(mapType)

	for _, t := range ts {
		val := reflect.ValueOf(t)
		if val.Kind() == reflect.Map {
			for _, key := range val.MapKeys() {
				result.SetMapIndex(key, val.MapIndex(key))
			}
		}
	}

	return result.Interface().(T)
}

// mergeStructs merges struct values by combining fields using field-by-field merging
func mergeStructs[T any](ts []T) T {
	if len(ts) == 0 {
		var zero T
		return zero
	}

	if len(ts) == 1 {
		return ts[0]
	}

	firstVal := reflect.ValueOf(ts[0])
	structType := firstVal.Type()

	// Create a new struct instance
	result := reflect.New(structType).Elem()

	// Process each field
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		fieldType := field.Type

		// Skip unexported fields
		if !field.IsExported() {
			// Copy the value from the first struct for unexported fields
			result.Field(i).Set(firstVal.Field(i))
			continue
		}

		// Collect all field values from all structs
		var fieldValues []reflect.Value
		for _, t := range ts {
			val := reflect.ValueOf(t)
			if val.Kind() == reflect.Struct {
				fieldVal := val.Field(i)
				fieldValues = append(fieldValues, fieldVal)
			}
		}

		if len(fieldValues) == 0 {
			continue
		}

		// Create a slice of the appropriate type and use Merge
		sliceType := reflect.SliceOf(fieldType)
		fieldSlice := reflect.MakeSlice(sliceType, len(fieldValues), len(fieldValues))

		for j, fv := range fieldValues {
			fieldSlice.Index(j).Set(fv)
		}

		// Convert fieldSlice to []any and use Merge directly
		anySlice := make([]any, fieldSlice.Len())
		for j := 0; j < fieldSlice.Len(); j++ {
			anySlice[j] = fieldSlice.Index(j).Interface()
		}

		mergedValue := Merge(anySlice)

		if mergedValue != nil {
			mergedVal := reflect.ValueOf(mergedValue)
			if mergedVal.Type().AssignableTo(fieldType) {
				result.Field(i).Set(mergedVal)
			} else if mergedVal.Type().ConvertibleTo(fieldType) {
				result.Field(i).Set(mergedVal.Convert(fieldType))
			} else {
				// Fallback: use the last value
				result.Field(i).Set(fieldValues[len(fieldValues)-1])
			}
		}
	}

	return result.Interface().(T)
}
