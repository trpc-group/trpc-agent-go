//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- NamedToolSet and NamedTool tests (migrated) ---

// fakeTool implements tool.CallableTool and tool.StreamableTool for testing.
type fakeTool struct {
	decl       *tool.Declaration
	callResult any
	callErr    error
	stream     *tool.Stream
}

func (f *fakeTool) Declaration() *tool.Declaration                { return f.decl }
func (f *fakeTool) Call(_ context.Context, _ []byte) (any, error) { return f.callResult, f.callErr }
func (f *fakeTool) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	if f.stream == nil {
		f.stream = tool.NewStream(1)
	}
	return f.stream.Reader, nil
}

// simpleTool implements only tool.Tool (not callable/streamable) for negative paths.
type simpleTool struct {
	name         string
	desc         string
	inputSchema  *tool.Schema
	outputSchema *tool.Schema
}

func (s *simpleTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:         s.name,
		Description:  s.desc,
		InputSchema:  s.inputSchema,
		OutputSchema: s.outputSchema,
	}
}

// skipperTool implements tool.Tool and exposes SkipSummarization preference
// to validate NamedTool delegation behavior in tests.
type skipperTool struct {
	name string
	skip bool
}

func (s *skipperTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}

func (s *skipperTool) SkipSummarization() bool { return s.skip }

type metadataPolicyTool struct {
	name             string
	metadata         tool.ToolMetadata
	deferTool        bool
	decision         tool.PermissionDecision
	permissionCalled bool
}

func (m *metadataPolicyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: m.name}
}

func (m *metadataPolicyTool) ToolMetadata() tool.ToolMetadata {
	return m.metadata
}

func (m *metadataPolicyTool) ShouldDefer(context.Context) bool {
	return m.deferTool
}

func (m *metadataPolicyTool) CheckPermission(
	_ context.Context,
	_ *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	m.permissionCalled = true
	return m.decision, nil
}

type declarationWrapperTool struct {
	base tool.Tool
}

func (d *declarationWrapperTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "wrapped"}
}

func (d *declarationWrapperTool) originalTool() tool.Tool {
	return d.base
}

type callableOnlyTool struct {
	decl       *tool.Declaration
	callResult any
}

func (c *callableOnlyTool) Declaration() *tool.Declaration {
	return c.decl
}

func (c *callableOnlyTool) Call(context.Context, []byte) (any, error) {
	return c.callResult, nil
}

type streamOnlyTool struct {
	decl   *tool.Declaration
	stream *tool.Stream
}

func (s *streamOnlyTool) Declaration() *tool.Declaration {
	return s.decl
}

func (s *streamOnlyTool) StreamableCall(context.Context, []byte) (*tool.StreamReader, error) {
	if s.stream == nil {
		s.stream = tool.NewStream(1)
	}
	return s.stream.Reader, nil
}

type nonStreamInnerTool struct {
	*fakeTool
}

func (n *nonStreamInnerTool) StreamInner() bool {
	return false
}

type nilDeclarationTool struct{}

func (nilDeclarationTool) Declaration() *tool.Declaration {
	return nil
}

// fakeToolSet implements tool.ToolSet.
type fakeToolSet struct {
	name   string
	tools  []tool.Tool
	closed bool
}

func (f *fakeToolSet) Tools(context.Context) []tool.Tool { return f.tools }
func (f *fakeToolSet) Close() error                      { f.closed = true; return nil }
func (f *fakeToolSet) Name() string                      { return f.name }

func TestNamedToolSet_Idempotent(t *testing.T) {
	ts := &fakeToolSet{name: "fs"}
	nts := NewNamedToolSet(ts)
	// Calling again with an already wrapped toolset should return the same instance.
	nts2 := NewNamedToolSet(nts)
	require.Same(t, nts, nts2, "idempotent wrapper should be same instance")
}

func TestNamedToolSet_Tools_PrefixingAndPassthrough(t *testing.T) {
	// With a name, tool names should be prefixed.
	base := &fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&simpleTool{name: "read", desc: "read file"}},
	}
	nts := NewNamedToolSet(base)
	got := nts.Tools(context.Background())
	require.Len(t, got, 1)
	require.Equal(t, "fs_read", got[0].Declaration().Name)

	// Without a name, names should be unchanged.
	base2 := &fakeToolSet{name: "", tools: []tool.Tool{&simpleTool{name: "write", desc: "write file"}}}
	nts2 := NewNamedToolSet(base2)
	got2 := nts2.Tools(context.Background())
	require.Equal(t, "write", got2[0].Declaration().Name)
}

func TestNamedTool_OriginalAndCloseAndName(t *testing.T) {
	base := &fakeToolSet{name: "fs"}
	nts := NewNamedToolSet(base)
	// Wrap a single tool.
	t1 := &simpleTool{name: "copy", desc: "copy file"}
	base.tools = []tool.Tool{t1}
	got := nts.Tools(context.Background())
	nt, ok := got[0].(*NamedTool)
	require.True(t, ok, "expected NamedTool, got %T", got[0])
	require.Equal(t, t1, nt.Original())
	require.Equal(t, "fs", nt.ToolSetName())
	require.Equal(t, "fs", nts.Name())
	require.NoError(t, nts.Close())
	require.True(t, base.closed, "underlying Close() not called")
}

func TestNamedTool_CallAndStreamableCall(t *testing.T) {
	// Positive path via NamedToolSet wrapper.
	f := &fakeTool{decl: &tool.Declaration{Name: "sum"}, callResult: 42}
	nts := NewNamedToolSet(&fakeToolSet{name: "math", tools: []tool.Tool{f}})
	ts := nts.Tools(context.Background())
	nt, ok := ts[0].(*NamedTool)
	require.True(t, ok, "expected NamedTool, got %T", ts[0])
	v, err := nt.Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 42, v)

	r, err := nt.StreamableCall(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, f.stream, "stream should be initialized")
	f.stream.Writer.Send(tool.StreamChunk{Content: "ok"}, nil)
	chunk, recvErr := r.Recv()
	require.NoError(t, recvErr)
	require.Equal(t, "ok", chunk.Content)
	f.stream.Writer.Close()
}

func TestNamedTool_CallFailures(t *testing.T) {
	// Negative path through wrapper (not callable or streamable).
	nts := NewNamedToolSet(&fakeToolSet{name: "fs", tools: []tool.Tool{&simpleTool{name: "noop"}}})
	nt := nts.Tools(context.Background())[0].(*NamedTool)
	_, err := nt.Call(context.Background(), nil)
	require.EqualError(t, err, "tool is not callable")

	_, err = nt.StreamableCall(context.Background(), nil)
	require.EqualError(t, err, "tool is not streamable")
}

func TestNamedTool_SkipSummarizationDelegation(t *testing.T) {
	// Wrap with NamedToolSet so we can obtain a *NamedTool instance.
	nts := NewNamedToolSet(&fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&skipperTool{name: "raw", skip: true}},
	})
	t1 := nts.Tools(context.Background())[0].(*NamedTool)
	require.True(t, t1.SkipSummarization())

	nts2 := NewNamedToolSet(&fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&skipperTool{name: "raw", skip: false}},
	})
	t2 := nts2.Tools(context.Background())[0].(*NamedTool)
	require.False(t, t2.SkipSummarization())
}

func TestNamedTool_MetadataAndPermissionDelegation(t *testing.T) {
	const denyReason = "blocked"
	ctx := context.Background()
	original := &metadataPolicyTool{
		name: "raw",
		metadata: tool.ToolMetadata{
			ReadOnly:        true,
			ConcurrencySafe: true,
			SearchOrRead:    true,
			MaxResultSize:   128,
		},
		deferTool: true,
		decision:  tool.DenyPermission(denyReason),
	}
	nts := NewNamedToolSet(&fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{original},
	})
	nt := nts.Tools(ctx)[0].(*NamedTool)

	require.Equal(t, original.metadata, nt.ToolMetadata())
	require.True(t, nt.IsConcurrencySafe())
	require.True(t, nt.ShouldDefer(ctx))
	decision, err := nt.CheckPermission(ctx, &tool.PermissionRequest{})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, decision.Action)
	require.Equal(t, denyReason, decision.Reason)
	require.True(t, original.permissionCalled)

	plain := NewNamedToolSet(&fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&simpleTool{name: "plain"}},
	}).Tools(ctx)[0].(*NamedTool)
	require.Equal(t, tool.ToolMetadata{}, plain.ToolMetadata())
	require.False(t, plain.IsConcurrencySafe())
	require.False(t, plain.ShouldDefer(ctx))
	decision, err = plain.CheckPermission(ctx, &tool.PermissionRequest{})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionAllow, decision.Action)
}

func TestNewUnprefixedNamedTool(t *testing.T) {
	base := &simpleTool{name: "raw", desc: "raw tool"}
	named := NewUnprefixedNamedTool(base)
	require.Same(t, base, named.Original())
	require.Equal(t, "raw", named.Declaration().Name)
	require.Equal(t, "", named.ToolSetName())
	require.Same(t, base, ResolveSemantic(named))
}

func TestResolveDeclarationAndSemantic(t *testing.T) {
	base := &simpleTool{name: "raw"}
	named := NewNamedToolSet(&fakeToolSet{
		name:  "set",
		tools: []tool.Tool{base},
	}).Tools(context.Background())[0]
	wrapped := &declarationWrapperTool{base: named}
	require.Same(t, named, ResolveDeclaration(wrapped))
	require.Same(t, base, ResolveSemantic(wrapped))
	require.Nil(t, ResolveDeclaration(nil))
	require.Nil(t, ResolveSemantic(nil))
}

func TestApplyDeclarationsOverlaysMatchingTools(t *testing.T) {
	inputSchema := &tool.Schema{
		Type:       "object",
		Properties: map[string]*tool.Schema{"query": {Type: "string", Description: "old query"}},
	}
	outputSchema := &tool.Schema{Type: "string", Description: "old output"}
	base := []tool.Tool{
		&simpleTool{name: "plain", desc: "plain description", inputSchema: inputSchema, outputSchema: outputSchema},
		nilDeclarationTool{},
		nil,
	}
	declarations := []tool.Declaration{
		{Name: ""},
		{Name: "plain", Description: "patched description"},
		{Name: "missing", Description: "missing description"},
	}
	got := ApplyDeclarations(base, declarations)
	require.Len(t, got, 3)
	require.Equal(t, "patched description", got[0].Declaration().Description)
	require.Same(t, inputSchema, got[0].Declaration().InputSchema)
	require.Same(t, outputSchema, got[0].Declaration().OutputSchema)
	require.Same(t, base[0], ResolveDeclaration(got[0]))
	require.Same(t, base[0], ResolveSemantic(got[0]))
	require.Equal(t, base[1], got[1])
	require.Nil(t, got[2])
	require.Equal(t, base, ApplyDeclarations(base, nil))
	require.Equal(t, base, ApplyDeclarations(base, []tool.Declaration{{Name: ""}}))
	require.Nil(t, ApplyDeclarations(nil, declarations))
}

func TestApplyDeclarationsPreservesCallableAndStreamableCapabilities(t *testing.T) {
	ctx := context.Background()
	callableBase := &callableOnlyTool{
		decl:       &tool.Declaration{Name: "callable", Description: "old"},
		callResult: "called",
	}
	streamBase := &streamOnlyTool{
		decl: &tool.Declaration{Name: "streamable", Description: "old"},
	}
	bothBase := &fakeTool{
		decl:       &tool.Declaration{Name: "both", Description: "old"},
		callResult: "both-called",
	}
	base := []tool.Tool{callableBase, streamBase, bothBase}
	got := ApplyDeclarations(base, []tool.Declaration{
		{Name: "callable", Description: "new callable"},
		{Name: "streamable", Description: "new streamable"},
		{Name: "both", Description: "new both"},
	})
	require.Equal(t, "new callable", got[0].Declaration().Description)
	callable, ok := got[0].(tool.CallableTool)
	require.True(t, ok)
	callResult, err := callable.Call(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "called", callResult)
	_, ok = got[0].(tool.StreamableTool)
	require.False(t, ok)
	streamable, ok := got[1].(tool.StreamableTool)
	require.True(t, ok)
	reader, err := streamable.StreamableCall(ctx, nil)
	require.NoError(t, err)
	streamBase.stream.Writer.Send(tool.StreamChunk{Content: "streamed"}, nil)
	chunk, err := reader.Recv()
	require.NoError(t, err)
	require.Equal(t, "streamed", chunk.Content)
	streamBase.stream.Writer.Close()
	_, ok = got[1].(tool.CallableTool)
	require.False(t, ok)
	bothCallable, ok := got[2].(tool.CallableTool)
	require.True(t, ok)
	bothResult, err := bothCallable.Call(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "both-called", bothResult)
	bothStreamable, ok := got[2].(tool.StreamableTool)
	require.True(t, ok)
	bothReader, err := bothStreamable.StreamableCall(ctx, nil)
	require.NoError(t, err)
	bothBase.stream.Writer.Send(tool.StreamChunk{Content: "both-streamed"}, nil)
	bothChunk, err := bothReader.Recv()
	require.NoError(t, err)
	require.Equal(t, "both-streamed", bothChunk.Content)
	bothBase.stream.Writer.Close()
}

func TestApplyDeclarationsHonorsStreamInnerPreference(t *testing.T) {
	base := &nonStreamInnerTool{
		fakeTool: &fakeTool{
			decl:       &tool.Declaration{Name: "search", Description: "old"},
			callResult: "called",
		},
	}
	got := ApplyDeclarations([]tool.Tool{NewUnprefixedNamedTool(base)}, []tool.Declaration{
		{Name: "search", Description: "new"},
	})
	require.Len(t, got, 1)
	require.Equal(t, "new", got[0].Declaration().Description)
	_, streamable := got[0].(tool.StreamableTool)
	require.False(t, streamable)
	callable, ok := got[0].(tool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "called", result)
	require.Same(t, base, ResolveSemantic(got[0]))
}

func TestGenerateJSONSchema_Primitives(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected *tool.Schema
	}{
		{
			name:     "string type",
			input:    "",
			expected: &tool.Schema{Type: "string"},
		},
		{
			name:     "integer type",
			input:    int(0),
			expected: &tool.Schema{Type: "integer"},
		},
		{
			name:     "float type",
			input:    float64(0),
			expected: &tool.Schema{Type: "number"},
		},
		{
			name:     "boolean type",
			input:    false,
			expected: &tool.Schema{Type: "boolean"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := GenerateJSONSchema(reflect.TypeOf(tc.input))
			require.Equal(t, tc.expected.Type, result.Type)
		})
	}
}

func TestGenerateJSONSchema_ComplexTypes(t *testing.T) {
	t.Run("array type", func(t *testing.T) {
		input := []string{}
		result := GenerateJSONSchema(reflect.TypeOf(input))

		require.Equal(t, "array", result.Type)
		require.NotNil(t, result.Items)
		require.Equal(t, "string", result.Items.Type)
	})

	t.Run("map type", func(t *testing.T) {
		input := map[string]int{}
		result := GenerateJSONSchema(reflect.TypeOf(input))

		require.Equal(t, "object", result.Type)
		require.NotNil(t, result.AdditionalProperties)
		propSchema, ok := result.AdditionalProperties.(*tool.Schema)
		require.True(t, ok, "additionalProperties should be *tool.Schema")
		require.Equal(t, "integer", propSchema.Type)
	})

	t.Run("pointer type", func(t *testing.T) {
		var input *string
		result := GenerateJSONSchema(reflect.TypeOf(input))

		require.Equal(t, "string", result.Type)
	})
}

func TestGenerateJSONSchema_StructTypes(t *testing.T) {
	type TestStruct struct {
		Name       string  `json:"name"`
		Age        int     `json:"age"`
		Optional   *string `json:"optional,omitempty"`
		Ignored    string  `json:"-"`
		unexported string
	}

	t.Run("struct with fields", func(t *testing.T) {
		result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

		require.Equal(t, "object", result.Type)

		require.Len(t, result.Properties, 3)

		require.NotNil(t, result.Properties["name"])
		require.Equal(t, "string", result.Properties["name"].Type)

		require.NotNil(t, result.Properties["age"])
		require.Equal(t, "integer", result.Properties["age"].Type)

		require.NotNil(t, result.Properties["optional"])
		require.Equal(t, "string", result.Properties["optional"].Type)

		require.ElementsMatch(t, []string{"name", "age"}, result.Required)

		// Make sure ignored and unexported fields are not included
		require.Nil(t, result.Properties["Ignored"])
		require.Nil(t, result.Properties["unexported"])
	})
}

func TestGenerateJSONSchema_Nested(t *testing.T) {
	type Address struct {
		Street string `json:"street"`
		City   string `json:"city"`
	}

	type Person struct {
		Name    string   `json:"name"`
		Address Address  `json:"address"`
		Tags    []string `json:"tags"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(Person{}))

	require.NotNil(t, result.Properties["address"], "expected address property")

	addressProps := result.Properties["address"].Properties
	require.NotNil(t, addressProps, "expected address to have properties")

	require.NotNil(t, addressProps["street"])
	require.Equal(t, "string", addressProps["street"].Type)

	require.NotNil(t, result.Properties["tags"])
	require.Equal(t, "array", result.Properties["tags"].Type)

	require.NotNil(t, result.Properties["tags"].Items)
	require.Equal(t, "string", result.Properties["tags"].Items.Type)
}

func TestGenerateJSONSchema_PointerTypeFix(t *testing.T) {
	// Test that pointer types now generate standard schema format
	// instead of the problematic "object,null" format

	type TestRequest struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	var input *TestRequest
	result := GenerateJSONSchema(reflect.TypeOf(input))

	// Should generate "object" instead of "object,null"
	require.Equal(t, "object", result.Type)

	// Should have properties
	require.NotNil(t, result.Properties)

	// Check that properties are correctly generated
	require.NotNil(t, result.Properties["name"])
	require.Equal(t, "string", result.Properties["name"].Type)

	require.NotNil(t, result.Properties["age"])
	require.Equal(t, "integer", result.Properties["age"].Type)
}

func TestGenerateJSONSchema_JSONSchemaTag_Description(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name" jsonschema:"description=User's full name"`
		Age  int    `json:"age" jsonschema:"description=User's age in years"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check description for name field
	require.Equal(t, "User's full name", result.Properties["name"].Description)

	// Check description for age field
	require.Equal(t, "User's age in years", result.Properties["age"].Description)
}

func TestGenerateJSONSchema_DescriptionTag_Compat(t *testing.T) {
	type Inner struct {
		City    string `json:"city" description:"City name"`
		Country string `json:"country" jsonschema:"description=Country code" description:"Country name"`
	}

	type Flat struct {
		Name string `json:"name" description:"User full name"`
		Age  int    `json:"age" jsonschema:"description=Age via jsonschema" description:"Age via description"`
	}

	type Nested struct {
		Query   string `json:"query" description:"Search query"`
		Address Inner  `json:"address"`
	}

	tests := []struct {
		name   string
		input  reflect.Type
		checks map[string]string // field path -> expected description
	}{
		{
			name:  "description tag on flat fields",
			input: reflect.TypeOf(Flat{}),
			checks: map[string]string{
				"name": "User full name",
			},
		},
		{
			name:  "jsonschema description overrides description tag",
			input: reflect.TypeOf(Flat{}),
			checks: map[string]string{
				"age": "Age via jsonschema",
			},
		},
		{
			name:  "description tag on nested struct fields",
			input: reflect.TypeOf(Nested{}),
			checks: map[string]string{
				"query": "Search query",
			},
		},
		{
			name:  "description tag inside nested struct",
			input: reflect.TypeOf(Inner{}),
			checks: map[string]string{
				"city":    "City name",
				"country": "Country code",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateJSONSchema(tt.input)
			for field, expected := range tt.checks {
				require.Equal(t, expected, result.Properties[field].Description,
					"field %q description mismatch", field)
			}
		})
	}
}

func TestGenerateJSONSchema_DescriptionTag_NestedStruct(t *testing.T) {
	type Inner struct {
		Street string `json:"street" description:"Street address"`
		Zip    string `json:"zip" jsonschema:"description=Postal code" description:"Zip code"`
	}

	type Outer struct {
		Name    string `json:"name" description:"User name"`
		Address Inner  `json:"address" description:"Mailing address"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(Outer{}))

	// Outer-level fields.
	require.Equal(t, "User name", result.Properties["name"].Description)

	// The nested struct field itself should carry the outer description tag.
	addrSchema := result.Properties["address"]
	require.NotNil(t, addrSchema, "address schema should exist")
	require.Equal(t, "Mailing address", addrSchema.Description)

	// Inner-level fields inside the nested struct.
	require.Equal(t, "Street address", addrSchema.Properties["street"].Description)
	require.Equal(t, "Postal code", addrSchema.Properties["zip"].Description,
		"jsonschema description should take priority over description tag")
}

func TestGenerateJSONSchema_JSONSchemaTag_StringEnum(t *testing.T) {
	type TestStruct struct {
		Status string `json:"status" jsonschema:"enum=active,enum=inactive,enum=pending"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	statusSchema := result.Properties["status"]
	require.Len(t, statusSchema.Enum, 3)

	expectedEnums := []string{"active", "inactive", "pending"}
	for i, expected := range expectedEnums {
		require.Equal(t, expected, statusSchema.Enum[i])
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_Pattern(t *testing.T) {
	type TestStruct struct {
		UserID string `json:"user_id" jsonschema:"description=User ID,pattern=^[a-z0-9_-]+$"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	userIDSchema := result.Properties["user_id"]
	require.Equal(t, "string", userIDSchema.Type)
	require.Equal(t, "User ID", userIDSchema.Description)
	require.Equal(t, "^[a-z0-9_-]+$", userIDSchema.Pattern)
}

func TestGenerateJSONSchema_JSONSchemaTag_PatternPointerString(t *testing.T) {
	type TestStruct struct {
		UserID *string `json:"user_id,omitempty" jsonschema:"pattern=^[a-z0-9_-]+$"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	userIDSchema := result.Properties["user_id"]
	require.Equal(t, "string", userIDSchema.Type)
	require.Equal(t, "^[a-z0-9_-]+$", userIDSchema.Pattern)
}

func TestGenerateJSONSchema_JSONSchemaTag_PatternNonStringIgnored(t *testing.T) {
	type TestStruct struct {
		Count int `json:"count" jsonschema:"pattern=^[0-9]+$"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	countSchema := result.Properties["count"]
	require.Equal(t, "integer", countSchema.Type)
	require.Empty(t, countSchema.Pattern)
}

func TestGenerateJSONSchema_JSONSchemaTag_IntEnum(t *testing.T) {
	type TestStruct struct {
		Priority int `json:"priority" jsonschema:"enum=1,enum=2,enum=3"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	prioritySchema := result.Properties["priority"]
	require.Len(t, prioritySchema.Enum, 3)

	expectedEnums := []int64{1, 2, 3}
	for i, expected := range expectedEnums {
		require.Equal(t, expected, prioritySchema.Enum[i])
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_FloatEnum(t *testing.T) {
	type TestStruct struct {
		Rate float64 `json:"rate" jsonschema:"enum=1.5,enum=2.0,enum=3.5"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	rateSchema := result.Properties["rate"]
	require.Len(t, rateSchema.Enum, 3)

	expectedEnums := []float64{1.5, 2.0, 3.5}
	for i, expected := range expectedEnums {
		require.Equal(t, expected, rateSchema.Enum[i])
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_BoolEnum(t *testing.T) {
	type TestStruct struct {
		Enabled bool `json:"enabled" jsonschema:"enum=true,enum=false"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	enabledSchema := result.Properties["enabled"]
	require.Len(t, enabledSchema.Enum, 2)

	expectedEnums := []bool{true, false}
	for i, expected := range expectedEnums {
		require.Equal(t, expected, enabledSchema.Enum[i])
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_Required(t *testing.T) {
	type TestStruct struct {
		RequiredField    string `json:"required_field" jsonschema:"required"`
		OptionalField    string `json:"optional_field,omitempty"`
		NonOptionalField string `json:"non_optional_field"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check required fields
	expectedRequired := []string{"required_field", "non_optional_field"}
	require.Len(t, result.Required, len(expectedRequired))

	for _, expected := range expectedRequired {
		require.Contains(t, result.Required, expected)
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_Combined(t *testing.T) {
	type TestStruct struct {
		Status string `json:"status" jsonschema:"description=Current status,enum=active,enum=inactive,required"`
		Count  int    `json:"count,omitempty" jsonschema:"description=Item count,enum=10,enum=20,enum=30"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check status field
	statusSchema := result.Properties["status"]
	require.Equal(t, "Current status", statusSchema.Description)
	require.Len(t, statusSchema.Enum, 2)

	// Check count field
	countSchema := result.Properties["count"]
	require.Equal(t, "Item count", countSchema.Description)
	require.Len(t, countSchema.Enum, 3)

	// Check required fields (only status should be required)
	require.Len(t, result.Required, 1)
	require.Equal(t, "status", result.Required[0])
}

func TestGenerateJSONSchema_JSONSchemaTag_InvalidEnum(t *testing.T) {
	type TestStruct struct {
		InvalidInt string `json:"invalid_int" jsonschema:"enum=not_a_number"`
	}

	// This should continue processing despite the invalid enum error
	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Should return a struct schema with properties despite the error
	require.Equal(t, "object", result.Type)

	// Should have the field property even with invalid enum
	require.NotNil(t, result.Properties["invalid_int"])
	require.Equal(t, "string", result.Properties["invalid_int"].Type)
}

func TestGenerateJSONSchema_JSONSchemaTag_EdgeCases(t *testing.T) {
	type TestStruct struct {
		EmptyTag    string `json:"empty_tag" jsonschema:""`
		OnlyCommas  string `json:"only_commas" jsonschema:",,,"`
		SimpleTag   string `json:"simple" jsonschema:"description=Test Description,required"`
		SingleValue string `json:"single" jsonschema:"required"`
		NoEquals    string `json:"no_equals" jsonschema:"description"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check that description is set correctly without trimming
	require.Equal(t, "Test Description", result.Properties["simple"].Description)

	// Check required fields
	expectedRequired := []string{"simple", "single", "empty_tag", "only_commas", "no_equals"}
	require.Len(t, result.Required, len(expectedRequired))
}

func TestGenerateJSONSchema_JSONSchemaTag_UnsupportedEnumType(t *testing.T) {
	type CustomType struct {
		Value string
	}

	type TestStruct struct {
		Custom CustomType `json:"custom" jsonschema:"enum=value1,enum=value2"`
	}

	// This should continue processing despite the unsupported enum type error
	result := GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Should return a struct schema with properties despite the error
	require.Equal(t, "object", result.Type)

	// Should have the field property even with unsupported enum type
	require.NotNil(t, result.Properties["custom"])
	require.Equal(t, "object", result.Properties["custom"].Type)
}

func TestGenerateJSONSchema_RecursiveStructUsesDefs(t *testing.T) {
	require := require.New(t)

	type Node struct {
		Value string `json:"value"`
		Next  *Node  `json:"next,omitempty"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(Node{}))

	require.NotEmpty(
		result.Defs,
		"expected $defs to contain recursive struct schema",
	)

	nodeDef, ok := result.Defs["node"]
	require.True(ok, "expected $defs entry named node")

	nextSchema := result.Properties["next"]
	require.NotNil(nextSchema, "expected next property to be present")
	require.Equal(
		"#/$defs/node",
		nextSchema.Ref,
		"expected next to reference node definition",
	)

	valueSchema := nodeDef.Properties["value"]
	require.NotNil(valueSchema, "expected node definition to keep value")
	require.Equal("string", valueSchema.Type)
}

func TestGenerateJSONSchema_NonRecursiveNestedStructInline(t *testing.T) {
	require := require.New(t)

	type Address struct {
		City string `json:"city"`
	}

	type Person struct {
		Name   string   `json:"name"`
		Home   Address  `json:"home"`
		Office *Address `json:"office,omitempty"`
	}

	result := GenerateJSONSchema(reflect.TypeOf(Person{}))

	require.Empty(
		result.Defs,
		"expected no $defs for non recursive struct",
	)

	homeSchema := result.Properties["home"]
	require.NotNil(homeSchema, "expected home property schema")
	require.Empty(homeSchema.Ref, "expected home schema inline")

	citySchema := homeSchema.Properties["city"]
	require.NotNil(citySchema, "expected home.city property schema")
	require.Equal("string", citySchema.Type)

	required := make(map[string]bool, len(result.Required))
	for _, field := range result.Required {
		required[field] = true
	}

	require.True(required["name"], "expected name to be required")
	require.True(required["home"], "expected home to be required")
	require.False(required["office"], "office should not be required")
}

func TestGenerateJSONSchema_MapRecursiveValueUsesDefs(t *testing.T) {
	type Node struct {
		Next *Node `json:"next,omitempty"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(map[string]Node{}))

	require.Equal(t, "object", schema.Type)
	propSchema, ok := schema.AdditionalProperties.(*tool.Schema)
	require.True(t, ok, "additionalProperties should be *tool.Schema")
	require.Equal(t, "#/$defs/node", propSchema.Ref)
	require.NotEmpty(t, schema.Defs, "expected defs for recursive value")
	require.Contains(t, schema.Defs, "node")
}

func TestGenerateJSONSchema_SliceRecursionUsesDefs(t *testing.T) {
	type Tree struct {
		Children []Tree `json:"children"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(Tree{}))

	require.Contains(t, schema.Defs, "tree")
	children := schema.Properties["children"]
	require.NotNil(t, children)
	require.Equal(t, "array", children.Type)
	require.NotNil(t, children.Items)
	require.Equal(t, "#/$defs/tree", children.Items.Ref)
	require.Contains(t, schema.Required, "children")
}

func TestGenerateJSONSchema_UntaggedAndIgnoredFields(t *testing.T) {
	type Sample struct {
		Untagged int
		Ignored  string `json:"-"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(Sample{}))

	require.Contains(t, schema.Properties, "Untagged")
	require.NotContains(t, schema.Properties, "Ignored")
	require.Contains(t, schema.Required, "Untagged")
}

func TestGenerateJSONSchema_PrimitiveDefaultFallback(t *testing.T) {
	ifaceType := reflect.TypeOf((*any)(nil)).Elem()
	schema := GenerateJSONSchema(ifaceType)

	require.Equal(t, "object", schema.Type)
}

func TestGenerateJSONSchema_MapWithoutDefs(t *testing.T) {
	schema := GenerateJSONSchema(reflect.TypeOf(map[string]string{}))

	require.Equal(t, "object", schema.Type)
	require.Nil(t, schema.Defs)
	propSchema, ok := schema.AdditionalProperties.(*tool.Schema)
	require.True(t, ok)
	require.Equal(t, "string", propSchema.Type)
}

func TestGenerateJSONSchema_ParseJSONSchemaTagError(t *testing.T) {
	type BadEnum struct {
		Priority int `json:"priority" jsonschema:"enum=not_a_number"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(BadEnum{}))

	require.NotNil(t, schema.Properties["priority"])
	require.Equal(t, "integer", schema.Properties["priority"].Type)
	require.Contains(t, schema.Required, "priority")
}

func TestGenerateJSONSchema_PointerRequiredByTag(t *testing.T) {
	type WithPointer struct {
		Ptr *string `json:"ptr" jsonschema:"required"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(WithPointer{}))

	require.Contains(t, schema.Required, "ptr")
	require.Equal(t, "string", schema.Properties["ptr"].Type)
}

func TestGenerateJSONSchema_JSONSchemaTag_InvalidFloatAndBool(t *testing.T) {
	type BadTags struct {
		Rate    float64 `json:"rate" jsonschema:"enum=not_a_float"`
		Enabled bool    `json:"enabled" jsonschema:"enum=not_bool"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(BadTags{}))

	require.Equal(t, "number", schema.Properties["rate"].Type)
	require.Equal(t, "boolean", schema.Properties["enabled"].Type)
	require.ElementsMatch(t, []string{"rate", "enabled"}, schema.Required)
}

func TestCheckRecursionSliceArrayPtr(t *testing.T) {
	t.Parallel()

	type item struct{}
	type wrapper struct {
		Inner *item
	}
	type container struct {
		V item
	}

	target := reflect.TypeOf(item{})

	visited := make(map[reflect.Type]bool)
	require.True(t, checkRecursion(target, reflect.TypeOf([]item{}), visited))
	require.True(t, checkRecursion(target, reflect.TypeOf([1]item{}), visited))
	require.True(t, checkRecursion(target, reflect.TypeOf([]*item{}), visited))
	require.True(t, checkRecursion(target, reflect.TypeOf([]container{}), visited))
	require.True(t, checkRecursion(target, reflect.TypeOf(&item{}), visited))
	require.True(t, checkRecursion(target, reflect.TypeOf(&wrapper{}), visited))
}

func TestGenerateDefNameAnonymous(t *testing.T) {
	t.Parallel()

	anon := struct{ X int }{}
	require.Equal(t, "anonymousStruct", generateDefName(reflect.TypeOf(anon)))
}

func TestHandlePrimitiveTypeDefault(t *testing.T) {
	t.Parallel()

	ch := make(chan int)
	schema := handlePrimitiveType(reflect.TypeOf(ch))

	require.Equal(t, "object", schema.Type)
}

func TestAppendRequiredFieldRefNonPtr(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeOf(struct {
		Child int `json:"child"`
	}{}).FieldByName("Child")
	require.True(t, ok)

	required := appendRequiredField(
		nil, field, &tool.Schema{Ref: "#/$defs/child"}, "child", false,
	)
	require.Equal(t, []string{"child"}, required)
}

func TestGenerateJSONSchema_DefSchemaIsolation(t *testing.T) {
	t.Parallel()

	type Node struct {
		Value int   `json:"value"`
		Next  *Node `json:"next,omitempty"`
	}

	schema := GenerateJSONSchema(reflect.TypeOf(Node{}))

	defNode, ok := schema.Defs["node"]
	require.True(t, ok)
	require.Equal(t, "#/$defs/node", defNode.Properties["next"].Ref)
	require.Contains(t, defNode.Required, "value")
	require.Equal(t, "integer", defNode.Properties["value"].Type)

	// Mutate returned root schema and ensure defs stay unchanged.
	schema.Properties["next"] = &tool.Schema{Type: "string"}
	schema.Required = nil

	require.Equal(t, "#/$defs/node", defNode.Properties["next"].Ref)
	require.Contains(t, defNode.Required, "value")
}
