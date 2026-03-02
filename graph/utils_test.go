//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testStruct struct {
	Name  string
	Value int
	Data  []string
}

type cyclicStruct struct {
	Self     *cyclicStruct
	Name     string
	Time     time.Time
	TimePtr  *time.Time
	DTimePtr *DataTime
	DTime    DataTime
}

type DataTime time.Time

func newDataTime() DataTime {
	t := time.Now()
	return (DataTime)(t)
}

func TestDeepCopyAny(t *testing.T) {
	now := time.Now()
	dTime := newDataTime()
	cyclic1 := &cyclicStruct{
		Name:     "A",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic1.Self = cyclic1

	cyclic2 := cyclicStruct{
		Name:     "B",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic3 := &cyclicStruct{
		Name:     "C",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic2.Self = cyclic3
	cyclic3.Self = &cyclic2

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{
			name:  "nil value",
			input: nil,
			want:  nil,
		},
		{
			name:  "string",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "int",
			input: 42,
			want:  42,
		},
		{
			name:  "float64",
			input: 3.14,
			want:  3.14,
		},
		{
			name:  "bool",
			input: true,
			want:  true,
		},

		{
			name:  "[]string",
			input: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "[]int",
			input: []int{1, 2, 3},
			want:  []int{1, 2, 3},
		},
		{
			name:  "[]any with mixed types",
			input: []any{"a", 1, 3.14, true},
			want:  []any{"a", 1, 3.14, true},
		},

		{
			name: "map[string]any",
			input: map[string]any{
				"name": "test",
				"age":  25,
				"tags": []string{"a", "b"},
			},
			want: map[string]any{
				"name": "test",
				"age":  25,
				"tags": []string{"a", "b"},
			},
		},

		{
			name: "testStruct",
			input: testStruct{
				Name:  "test",
				Value: 100,
				Data:  []string{"x", "y", "z"},
			},
			want: testStruct{
				Name:  "test",
				Value: 100,
				Data:  []string{"x", "y", "z"},
			},
		},

		{
			name: "*testStruct",
			input: &testStruct{
				Name:  "pointer_test",
				Value: 200,
				Data:  []string{"p", "q", "r"},
			},
			want: &testStruct{
				Name:  "pointer_test",
				Value: 200,
				Data:  []string{"p", "q", "r"},
			},
		},

		{
			name:  "time.Time",
			input: now,
			want:  now,
		},
		{
			name:  "*time.Time",
			input: &now,
			want:  &now,
		},

		{
			name:  "cyclic self-reference",
			input: cyclic1,
			want:  cyclic1,
		},
		{
			name:  "cyclic cross-reference",
			input: cyclic2,
			want:  cyclic2,
		},

		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "empty map",
			input: map[string]int{},
			want:  map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copied := deepCopyAny(tt.input)
			assert.True(t, reflect.DeepEqual(copied, tt.want),
				"deepCopyAny(%v) = %v, want %v", tt.input, copied,
				tt.want)
		})
	}
}

func TestDeepCopyUnexportedFields(t *testing.T) {
	type privateStruct struct {
		PublicField  string
		privateField string
	}

	original := privateStruct{
		PublicField:  "public",
		privateField: "private",
	}

	copied := deepCopyAny(original)

	originalPublic := reflect.ValueOf(original).FieldByName("PublicField").String()
	copiedPublic := reflect.ValueOf(copied).FieldByName("PublicField").String()

	assert.Equal(t, originalPublic, copiedPublic,
		"Public field not copied correctly")

	copiedPrivate := reflect.ValueOf(copied).FieldByName("privateField")
	assert.Equal(t, "", copiedPrivate.String(),
		"Private field should not be copied")
}

func TestCopyTimeType(t *testing.T) {
	now := time.Now()
	dTime := newDataTime()

	tests := []struct {
		name     string
		input    reflect.Value
		validate func(t *testing.T, result any)
	}{
		{
			name:  "time.Time value",
			input: reflect.ValueOf(now),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				require.True(t, ok, "Expected time.Time, got %T", result)
				assert.True(t, resultTime.Equal(now),
					"Times should be equal: original %v, result %v",
					now, resultTime)
				modified := now.Add(time.Hour)
				assert.False(t, resultTime.Equal(modified),
					"Modifying original time affected the result")
			},
		},
		{
			name:  "custom time type (convertible to time.Time)",
			input: reflect.ValueOf(dTime),
			validate: func(t *testing.T, result any) {
				rt, ok := result.(DataTime)
				require.True(t, ok, "Expected DataTime, got %T", result)
				resultTime := time.Time(rt)
				assert.True(t, resultTime.Equal(time.Time(dTime)),
					"Times should be equal: original %v, result %v",
					dTime, resultTime)
			},
		},
		{
			name:  "non-time type (string)",
			input: reflect.ValueOf("not a time"),
			validate: func(t *testing.T, result any) {
				resultStr, ok := result.(string)
				require.True(t, ok, "Expected string, got %T", result)
				assert.Equal(t, "not a time", resultStr)
			},
		},
		{
			name:  "non-time type (int)",
			input: reflect.ValueOf(42),
			validate: func(t *testing.T, result any) {
				resultInt, ok := result.(int)
				require.True(t, ok, "Expected int, got %T", result)
				assert.Equal(t, 42, resultInt)
			},
		},
		{
			name:  "zero time",
			input: reflect.ValueOf(time.Time{}),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				require.True(t, ok, "Expected time.Time, got %T", result)
				assert.True(t, resultTime.IsZero(), "Expected zero time")
			},
		},
		{
			name: "time with location",
			input: reflect.ValueOf(
				time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC),
			),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				require.True(t, ok, "Expected time.Time, got %T", result)
				expected := time.Date(2023, 12, 25, 10, 30, 0, 0,
					time.UTC)
				assert.True(t, resultTime.Equal(expected),
					"Times should be equal: expected %v, result %v",
					expected, resultTime)
				assert.Equal(t, time.UTC, resultTime.Location(),
					"Location should be preserved")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := copyTime(tt.input)
			tt.validate(t, result)
		})
	}
}

type deepCopoerStruct struct {
	Name string
	age  int
}

func (d deepCopoerStruct) DeepCopy() any {
	return deepCopoerStruct{
		Name: d.Name,
		age:  d.age,
	}
}

func TestDeepCopyAny_DeepCopier(t *testing.T) {
	original := &deepCopoerStruct{
		Name: "Alice",
		age:  30,
	}

	copied := deepCopyAny(original).(deepCopoerStruct)

	assert.True(t, reflect.DeepEqual(copied, *original),
		"Copied value should be equal to original. Got %v, expected %v",
		copied, *original)

	tests := map[string]any{
		"simple": deepCopoerStruct{
			Name: "Bob",
			age:  25,
		},
		"list": []deepCopoerStruct{
			{
				Name: "Alice",
				age:  30,
			},
			{
				Name: "Bob",
				age:  25,
			},
		},
	}
	copyMap := deepCopyAny(tests)
	assert.True(t, reflect.DeepEqual(copyMap, tests),
		"Copied value should be equal to original")
}

// structWithChan contains a channel field to verify that deepCopyAny
// replaces non-serializable channel values with nil during deep copy.
type structWithChan struct {
	Name string
	Ch   chan int
}

// structWithFunc contains a function field.
type structWithFunc struct {
	Name   string
	Action func() string
}

// structWithNestedChan nests a channel inside a sub-struct.
type structWithNestedChan struct {
	Label string
	Inner structWithChan
}

func TestDeepCopyAny_ChannelAndFuncFields(t *testing.T) {
	t.Run("struct with chan field", func(t *testing.T) {
		ch := make(chan int, 1)
		orig := structWithChan{Name: "test", Ch: ch}
		copied := deepCopyAny(orig).(structWithChan)
		assert.Equal(t, "test", copied.Name)
		assert.Nil(t, copied.Ch)
	})

	t.Run("pointer to struct with chan field", func(t *testing.T) {
		ch := make(chan int, 1)
		orig := &structWithChan{Name: "ptr", Ch: ch}
		copied := deepCopyAny(orig).(*structWithChan)
		assert.Equal(t, "ptr", copied.Name)
		assert.Nil(t, copied.Ch)
	})

	t.Run("struct with func field", func(t *testing.T) {
		orig := structWithFunc{
			Name:   "fn",
			Action: func() string { return "hello" },
		}
		copied := deepCopyAny(orig).(structWithFunc)
		assert.Equal(t, "fn", copied.Name)
		assert.Nil(t, copied.Action)
	})

	t.Run("nested struct with chan", func(t *testing.T) {
		ch := make(chan int)
		orig := structWithNestedChan{
			Label: "outer",
			Inner: structWithChan{Name: "inner", Ch: ch},
		}
		copied := deepCopyAny(orig).(structWithNestedChan)
		assert.Equal(t, "outer", copied.Label)
		assert.Equal(t, "inner", copied.Inner.Name)
		assert.Nil(t, copied.Inner.Ch)
	})

	t.Run("map containing struct with chan", func(t *testing.T) {
		ch := make(chan int)
		orig := map[string]any{
			"data": structWithChan{Name: "m", Ch: ch},
			"text": "hello",
		}
		copied := deepCopyAny(orig).(map[string]any)
		inner := copied["data"].(structWithChan)
		assert.Equal(t, "m", inner.Name)
		assert.Nil(t, inner.Ch)
		assert.Equal(t, "hello", copied["text"])
	})

	t.Run("bare channel value", func(t *testing.T) {
		ch := make(chan string, 1)
		copied := deepCopyAny(ch)
		assert.Nil(t, copied)
	})

	t.Run("bare func value", func(t *testing.T) {
		fn := func() {}
		copied := deepCopyAny(fn)
		assert.Nil(t, copied)
	})

	t.Run("send-only channel", func(t *testing.T) {
		ch := make(chan<- int)
		copied := deepCopyAny(ch)
		assert.Nil(t, copied)
	})
}

func BenchmarkDeepCopyAny(b *testing.B) {
	complexData := map[string]any{
		"users": []map[string]any{
			{
				"name": "Alice",
				"age":  30,
				"tags": []string{"admin", "user"},
			},
			{
				"name": "Bob",
				"age":  25,
				"tags": []string{"user"},
			},
		},
		"metadata": map[string]any{
			"version": "1.0",
			"config":  []int{1, 2, 3, 4, 5},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deepCopyAny(complexData)
	}
}

// ---------- jsonSafeCopy tests ----------

func TestJSONSafeCopy_Nil(t *testing.T) {
	assert.Nil(t, jsonSafeCopy(nil))
}

func TestJSONSafeCopy_Primitives(t *testing.T) {
	assert.Equal(t, "hello", jsonSafeCopy("hello"))
	assert.Equal(t, 42, jsonSafeCopy(42))
	assert.Equal(t, 3.14, jsonSafeCopy(3.14))
	assert.Equal(t, true, jsonSafeCopy(true))
}

func TestJSONSafeCopy_MapDeepCopy(t *testing.T) {
	orig := map[string]any{"k": []string{"a", "b"}}
	copied := jsonSafeCopy(orig).(map[string]any)
	orig["k"].([]string)[0] = "mutated"
	// jsonSafeFastPath handles []string natively.
	assert.Equal(t, "a", copied["k"].([]string)[0])
}

func TestJSONSafeCopy_StructWithChan(t *testing.T) {
	type s struct {
		Name string
		Ch   chan int
	}
	orig := s{Name: "x", Ch: make(chan int)}
	result := jsonSafeCopy(orig)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "x", m["Name"])
	_, has := m["Ch"]
	assert.False(t, has)
}

func TestJSONSafeCopy_StructWithoutUnsafeFields(t *testing.T) {
	type safe struct {
		A string
		B int
	}
	orig := safe{A: "ok", B: 1}
	result := jsonSafeCopy(orig)

	// Struct without unsafe fields should be preserved as-is.
	s, ok := result.(safe)
	require.True(t, ok)
	assert.Equal(t, safe{A: "ok", B: 1}, s)
}

func TestJSONSafeCopy_IgnoresUnsafeFieldByJSONTag(t *testing.T) {
	type taggedSafe struct {
		Name string
		Ch   chan int `json:"-"`
	}
	orig := taggedSafe{Name: "ok", Ch: make(chan int)}
	result := jsonSafeCopy(orig)

	copied, ok := result.(taggedSafe)
	require.True(t, ok)
	assert.Equal(t, "ok", copied.Name)
	assert.Nil(t, copied.Ch)
}

func TestJSONSafeCopy_PointerToStructWithChan(t *testing.T) {
	type s struct {
		Name string
		Ch   chan int
	}
	orig := &s{Name: "ptr", Ch: make(chan int)}
	result := jsonSafeCopy(orig)

	// Pointer dereferences to map since struct has chan.
	m, ok := result.(*map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ptr", (*m)["Name"])
}

func TestJSONSafeCopy_NestedStructsWithChan(t *testing.T) {
	type inner struct {
		Val int
		Ch  chan string
	}
	type outer struct {
		Label string
		Inner inner
	}
	orig := outer{
		Label: "o",
		Inner: inner{Val: 5, Ch: make(chan string)},
	}
	result := jsonSafeCopy(orig)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "o", m["Label"])

	innerMap, ok := m["Inner"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 5, innerMap["Val"])
	_, has := innerMap["Ch"]
	assert.False(t, has)
}

func TestJSONSafeCopy_BareChan(t *testing.T) {
	ch := make(chan int, 1)
	assert.Nil(t, jsonSafeCopy(ch))
}

func TestJSONSafeCopy_BareFunc(t *testing.T) {
	fn := func() {}
	assert.Nil(t, jsonSafeCopy(fn))
}

func TestJSONSafeCopy_JSONTagRespected(t *testing.T) {
	type tagged struct {
		Exported string `json:"exported_name"`
		Ignored  string `json:"-"`
		Ch       chan int
	}
	orig := tagged{Exported: "v", Ignored: "skip"}
	result := jsonSafeCopy(orig)

	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "v", m["exported_name"])
	_, hasIgnored := m["Ignored"]
	assert.False(t, hasIgnored)
}

func TestJSONSafeCopy_MapWithNilAndUnsafeValues(t *testing.T) {
	t.Run("fast path map[string]any keeps nil and drops unsafe", func(t *testing.T) {
		orig := map[string]any{
			"keep_nil":  nil,
			"drop_chan": make(chan int),
			"drop_func": func() {},
		}

		copied := jsonSafeCopy(orig).(map[string]any)
		val, ok := copied["keep_nil"]
		require.True(t, ok)
		assert.Nil(t, val)
		_, hasChan := copied["drop_chan"]
		assert.False(t, hasChan)
		_, hasFunc := copied["drop_func"]
		assert.False(t, hasFunc)
	})

	t.Run("reflect map path handles nil interface and drops unsafe", func(t *testing.T) {
		orig := map[int]any{
			1: nil,
			2: make(chan int),
			3: func() {},
			4: "ok",
		}

		copied := jsonSafeCopy(orig).(map[string]any)
		// Key 1 should exist with nil and must not panic.
		val, ok := copied["1"]
		require.True(t, ok)
		assert.Nil(t, val)
		// Unsafe values should be removed on reflect-map path.
		_, hasChan := copied["2"]
		assert.False(t, hasChan)
		_, hasFunc := copied["3"]
		assert.False(t, hasFunc)
		assert.Equal(t, "ok", copied["4"])
	})
}

func TestJSONSafeCopy_Cycles(t *testing.T) {
	t.Run("map[string]any self cycle", func(t *testing.T) {
		orig := map[string]any{}
		orig["self"] = orig

		copied := jsonSafeCopy(orig).(map[string]any)
		self, ok := copied["self"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t,
			reflect.ValueOf(copied).Pointer(),
			reflect.ValueOf(self).Pointer(),
		)
	})

	t.Run("[]any self cycle", func(t *testing.T) {
		orig := make([]any, 1)
		orig[0] = orig

		copied := jsonSafeCopy(orig).([]any)
		require.Len(t, copied, 1)
		inner, ok := copied[0].([]any)
		require.True(t, ok)
		require.Len(t, inner, 1)
	})
}

func TestJSONSafeCopy_TimePreserved(t *testing.T) {
	now := time.Now()
	result := jsonSafeCopy(now)
	rt, ok := result.(time.Time)
	require.True(t, ok)
	assert.True(t, rt.Equal(now))
}

func TestJSONSafeCopy_SliceWithMixed(t *testing.T) {
	ch := make(chan int)
	orig := []any{"a", 1, ch}
	result := jsonSafeCopy(orig).([]any)
	assert.Equal(t, "a", result[0])
	assert.Equal(t, 1, result[1])
	// Channel element becomes nil.
	assert.Nil(t, result[2])
}

// ---------- additional coverage tests ----------

func TestJSONSafeCopy_DeepCopierInterface(t *testing.T) {
	// Covers deepCopyByInterface success path inside
	// jsonSafeCopyWithVisited (L327-329).
	orig := deepCopoerStruct{Name: "dc", age: 99}
	result := jsonSafeCopy(orig)
	dc, ok := result.(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "dc", dc.Name)
	assert.Equal(t, 99, dc.age)
}

func TestJSONSafeCopy_DeepCopierViaReflect(t *testing.T) {
	// Covers deepCopyByReflectValue DeepCopier path (L252-254)
	// and jsonSafeReflect DeepCopier branch (L398-400).
	// The wrapper has an unsafe field (Ch) so jsonSafeCopyStruct
	// calls structToJSONSafeMap which recurses jsonSafeReflect
	// on the Inner field — hitting deepCopyByReflectValue.
	type wrapper struct {
		Inner deepCopoerStruct
		Ch    chan int
	}
	orig := wrapper{
		Inner: deepCopoerStruct{Name: "w", age: 7},
		Ch:    make(chan int),
	}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	dc, ok := m["Inner"].(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "w", dc.Name)
}

func TestJSONSafeCopy_NilSliceAny(t *testing.T) {
	// Covers jsonSafeFastPath nil []any branch (L357-359).
	var s []any
	result := jsonSafeCopy(s)
	assert.Nil(t, result)
}

func TestJSONSafeCopy_IntAndFloat64Slices(t *testing.T) {
	// Covers jsonSafeFastPath []int (L374-377) and
	// []float64 (L378-381) branches.
	ints := []int{10, 20, 30}
	resultInts := jsonSafeCopy(ints)
	assert.Equal(t, []int{10, 20, 30}, resultInts)

	floats := []float64{1.1, 2.2}
	resultFloats := jsonSafeCopy(floats)
	assert.Equal(t, []float64{1.1, 2.2}, resultFloats)
}

func TestJSONSafeCopy_ArrayWithUnsafe(t *testing.T) {
	// Covers jsonSafeReflect Array branch (L413-414) and
	// jsonSafeCopyArray (L494-503, was 0%).
	type s struct {
		Items [2]chan int
	}
	orig := s{Items: [2]chan int{make(chan int), make(chan int)}}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	items, ok := m["Items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 2)
	// Channels become nil.
	assert.Nil(t, items[0])
	assert.Nil(t, items[1])
}

func TestJSONSafeCopy_NilPointerInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyPointer nil branch (L429-431)
	// via jsonSafe path (struct has chan -> unsafe -> map).
	type s struct {
		Ptr *int
		Ch  chan int
	}
	orig := s{Ptr: nil, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Nil(t, m["Ptr"])
}

func TestJSONSafeCopy_PointerToChanStruct(t *testing.T) {
	// Covers jsonSafeCopyPointer inner==nil path (L440-442)
	// when pointed-to value converts to nil.
	ch := make(chan int)
	result := jsonSafeCopy(&ch)
	assert.Nil(t, result)
}

func TestJSONSafeCopy_PointerCycleInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyPointer cache hit (L433-435).
	// The struct has a chan field so it goes through jsonSafe path.
	type node struct {
		Name string
		Next *node
		Ch   chan int
	}
	a := &node{Name: "a", Ch: make(chan int)}
	b := &node{Name: "b", Ch: make(chan int), Next: a}
	a.Next = b

	result := jsonSafeCopy(a)
	require.NotNil(t, result)
}

func TestJSONSafeCopy_NilMapInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyMap nil branch (L454-456)
	// via jsonSafe path.
	type s struct {
		M  map[int]string
		Ch chan int
	}
	orig := s{M: nil, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Nil(t, m["M"])
}

func TestJSONSafeCopy_MapCacheHitInUnsafe(t *testing.T) {
	// Covers jsonSafeCopyMap cache hit (L458-460).
	// Two struct fields reference the same underlying map so
	// the second visit returns from cache.
	type s struct {
		A  map[int]string
		B  map[int]string
		Ch chan int
	}
	shared := map[int]string{1: "one"}
	orig := s{A: shared, B: shared, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	a, ok := m["A"].(map[string]any)
	require.True(t, ok)
	b, ok := m["B"].(map[string]any)
	require.True(t, ok)
	// Both should point to the same map (cache hit).
	assert.Equal(t,
		reflect.ValueOf(a).Pointer(),
		reflect.ValueOf(b).Pointer(),
	)
}

func TestJSONSafeCopy_NilSliceInUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopySlice nil branch (L478-480)
	// via jsonSafe path.
	type s struct {
		Items []int
		Ch    chan int
	}
	orig := s{Items: nil, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Nil(t, m["Items"])
}

func TestJSONSafeCopy_SliceCacheHitInUnsafe(t *testing.T) {
	// Covers jsonSafeCopySlice cache hit (L482-484).
	// Two struct fields reference the same slice.
	type s struct {
		A  []int
		B  []int
		Ch chan int
	}
	shared := []int{1, 2, 3}
	orig := s{A: shared, B: shared, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	a, ok := m["A"].([]any)
	require.True(t, ok)
	b, ok := m["B"].([]any)
	require.True(t, ok)
	assert.Equal(t,
		reflect.ValueOf(a).Pointer(),
		reflect.ValueOf(b).Pointer(),
	)
}

func TestJSONSafeCopy_TimeViaUnsafeStruct(t *testing.T) {
	// Covers jsonSafeCopyStruct time branch (L512-514).
	// The outer struct has a chan field, so structToJSONSafeMap
	// is called, and the inner time.Time field goes through
	// jsonSafeReflect -> jsonSafeCopyStruct which checks isTimeType.
	type s struct {
		T  time.Time
		Ch chan int
	}
	now := time.Now()
	orig := s{T: now, Ch: make(chan int)}
	result := jsonSafeCopy(orig)
	m, ok := result.(map[string]any)
	require.True(t, ok)
	rt, ok := m["T"].(time.Time)
	require.True(t, ok)
	assert.True(t, rt.Equal(now))
}

func TestJSONSafeReflect_InvalidValue(t *testing.T) {
	// Covers jsonSafeReflect invalid value (L395-397).
	visited := make(map[uintptr]any)
	result := jsonSafeReflect(reflect.Value{}, visited)
	assert.Nil(t, result)
}

func TestMapValueIsJSONUnsafe_InvalidAndNilInterface(t *testing.T) {
	// Covers mapValueIsJSONUnsafe invalid value (L567-569).
	assert.False(t, mapValueIsJSONUnsafe(reflect.Value{}))
	// Covers nil interface in interface (L571-572).
	var iface any
	rv := reflect.ValueOf(&iface).Elem()
	assert.False(t, mapValueIsJSONUnsafe(rv))
}

func TestCopyInterface_DeepCopier(t *testing.T) {
	// Covers copyInterface DeepCopier branch (L104-106).
	var iface any = &deepCopoerStruct{Name: "if", age: 1}
	rv := reflect.ValueOf(&iface).Elem()
	visited := make(map[uintptr]any)
	result := copyInterface(rv, visited)
	dc, ok := result.(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "if", dc.Name)
}

func TestCopyPointer_DeepCopier(t *testing.T) {
	// Covers copyPointer DeepCopier branch (L118-120).
	orig := &deepCopoerStruct{Name: "cp", age: 2}
	rv := reflect.ValueOf(orig)
	visited := make(map[uintptr]any)
	result := copyPointer(rv, visited)
	dc, ok := result.(deepCopoerStruct)
	require.True(t, ok)
	assert.Equal(t, "cp", dc.Name)
}

func TestCopyMap_CacheHit(t *testing.T) {
	// Covers copyMap cache hit (L133-135).
	m := map[string]int{"a": 1}
	rv := reflect.ValueOf(m)
	visited := make(map[uintptr]any)
	visited[rv.Pointer()] = m
	result := copyMap(rv, visited)
	assert.Equal(t, m, result)
}

func TestCopySlice_CacheHit(t *testing.T) {
	// Covers copySlice cache hit (L151-153).
	s := []int{1, 2, 3}
	rv := reflect.ValueOf(s)
	visited := make(map[uintptr]any)
	visited[rv.Pointer()] = s
	result := copySlice(rv, visited)
	assert.Equal(t, s, result)
}

func TestCopyStruct_ConvertibleAndFallback(t *testing.T) {
	// Covers copyStruct ConvertibleTo (L201-203) branch.
	// deepCopyReflect on an int32 field returns int (via
	// rv.Interface()), which is not directly AssignableTo
	// int32 but is ConvertibleTo int32.
	type myInt int32
	type s struct {
		V myInt
	}
	orig := s{V: 42}
	visited := make(map[uintptr]any)
	result := copyStruct(reflect.ValueOf(orig), visited)
	res, ok := result.(s)
	require.True(t, ok)
	assert.Equal(t, myInt(42), res.V)
}

func TestDeepCopyByReflectValue_Invalid(t *testing.T) {
	// Covers deepCopyByReflectValue invalid path (L249-251).
	out, ok := deepCopyByReflectValue(reflect.Value{})
	assert.Nil(t, out)
	assert.False(t, ok)
}

func TestHasJSONUnsafeField(t *testing.T) {
	type safe struct {
		A string
		B int
	}
	type withChan struct {
		A  string
		Ch chan int
	}
	type nested struct {
		Inner withChan
	}
	type ignoredUnsafe struct {
		A  string
		Ch chan int `json:"-"`
	}
	type selfRef struct {
		Name string
		Next *selfRef
	}
	type withSliceChan struct {
		Items []chan int
	}
	type withArrayChan struct {
		Items [2]chan int
	}
	type withMapChanValue struct {
		Items map[string]chan int
	}
	type withMapChanKey struct {
		Items map[chan int]string
	}

	assert.False(t, hasJSONUnsafeField(reflect.TypeOf(safe{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withChan{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(nested{})))
	assert.False(t, hasJSONUnsafeField(reflect.TypeOf(ignoredUnsafe{})))
	assert.False(t, hasJSONUnsafeField(reflect.TypeOf(selfRef{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withSliceChan{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withArrayChan{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withMapChanValue{})))
	assert.True(t, hasJSONUnsafeField(reflect.TypeOf(withMapChanKey{})))
}
