package util

import (
	"reflect"
	"testing"
)

func TestNewSimplePathMatcher(t *testing.T) {
	tests := []struct {
		name string
		want *SimplePathMatcher
	}{
		{
			name: "create new matcher",
			want: &SimplePathMatcher{
				patterns: make([]PathPattern, 0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewSimplePathMatcher()
			if got == nil {
				t.Errorf("NewSimplePathMatcher() = nil, want non-nil")
				return
			}
			if got.patterns == nil {
				t.Errorf("NewSimplePathMatcher().patterns = nil, want non-nil")
			}
		})
	}
}

func TestSimplePathMatcher_On(t *testing.T) {
	type args struct {
		pattern  string
		callback PathMatcherCallback
	}
	tests := []struct {
		name string
		args args
		want int
	}{
		{
			name: "register simple pattern",
			args: args{
				pattern:  "$.data.name",
				callback: func(value interface{}, path []interface{}) {},
			},
			want: 1,
		},
		{
			name: "register array pattern",
			args: args{
				pattern:  "$.items[*].id",
				callback: func(value interface{}, path []interface{}) {},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSimplePathMatcher()
			got := m.On(tt.args.pattern, tt.args.callback)
			if got == nil {
				t.Errorf("SimplePathMatcher.On() = nil, want non-nil")
				return
			}
			if len(got.patterns) != tt.want {
				t.Errorf("SimplePathMatcher.On() patterns length = %v, want %v", len(got.patterns), tt.want)
			}
		})
	}
}

func TestSimplePathMatcher_OnKeyStart(t *testing.T) {
	type args struct {
		callback KeyStartCallback
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "register key start callback",
			args: args{
				callback: func(path []interface{}) {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSimplePathMatcher()
			m.OnKeyStart(tt.args.callback)
			if m.keyStartCallback == nil {
				t.Errorf("SimplePathMatcher.OnKeyStart() callback not set")
			}
		})
	}
}

func TestSimplePathMatcher_OnKeyComplete(t *testing.T) {
	type args struct {
		callback KeyCompleteCallback
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "register key complete callback",
			args: args{
				callback: func(path []interface{}, finalValue interface{}) {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSimplePathMatcher()
			m.OnKeyComplete(tt.args.callback)
			if m.keyCompleteCallback == nil {
				t.Errorf("SimplePathMatcher.OnKeyComplete() callback not set")
			}
		})
	}
}

func TestSimplePathMatcher_parsePath(t *testing.T) {
	type args struct {
		path string
	}
	tests := []struct {
		name string
		args args
		want []interface{}
	}{
		{
			name: "empty path",
			args: args{path: ""},
			want: []interface{}{"$"},
		},
		{
			name: "root path",
			args: args{path: "$"},
			want: []interface{}{"$"},
		},
		{
			name: "simple property",
			args: args{path: "$.name"},
			want: []interface{}{"name"},
		},
		{
			name: "nested property",
			args: args{path: "$.data.user.name"},
			want: []interface{}{"data", "user", "name"},
		},
		{
			name: "array wildcard",
			args: args{path: "$.items[*]"},
			want: []interface{}{"items", "*"},
		},
		{
			name: "array index",
			args: args{path: "$.items[0]"},
			want: []interface{}{"items", 0},
		},
		{
			name: "complex path",
			args: args{path: "$.data.items[*].name"},
			want: []interface{}{"data", "items", "*", "name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSimplePathMatcher()
			got := m.parsePath(tt.args.path)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SimplePathMatcher.parsePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimplePathMatcher_matchPath(t *testing.T) {
	type args struct {
		path    []interface{}
		pattern []interface{}
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "exact match",
			args: args{
				path:    []interface{}{"data", "name"},
				pattern: []interface{}{"data", "name"},
			},
			want: true,
		},
		{
			name: "wildcard match",
			args: args{
				path:    []interface{}{"items", 0, "name"},
				pattern: []interface{}{"items", "*", "name"},
			},
			want: true,
		},
		{
			name: "length mismatch",
			args: args{
				path:    []interface{}{"data", "name"},
				pattern: []interface{}{"data"},
			},
			want: false,
		},
		{
			name: "property mismatch",
			args: args{
				path:    []interface{}{"data", "name"},
				pattern: []interface{}{"data", "age"},
			},
			want: false,
		},
		{
			name: "array index match",
			args: args{
				path:    []interface{}{"items", 0},
				pattern: []interface{}{"items", 0},
			},
			want: true,
		},
		{
			name: "array index mismatch",
			args: args{
				path:    []interface{}{"items", 0},
				pattern: []interface{}{"items", 1},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSimplePathMatcher()
			got := m.matchPath(tt.args.path, tt.args.pattern)
			if got != tt.want {
				t.Errorf("SimplePathMatcher.matchPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimplePathMatcher_CheckPatterns(t *testing.T) {
	type args struct {
		path  []interface{}
		value interface{}
	}
	tests := []struct {
		name            string
		patterns        []string
		args            args
		wantCallbackRun bool
	}{
		{
			name:     "matching pattern triggers callback",
			patterns: []string{"$.data.name"},
			args: args{
				path:  []interface{}{"data", "name"},
				value: "test",
			},
			wantCallbackRun: true,
		},
		{
			name:     "non-matching pattern does not trigger callback",
			patterns: []string{"$.data.age"},
			args: args{
				path:  []interface{}{"data", "name"},
				value: "test",
			},
			wantCallbackRun: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSimplePathMatcher()
			callbackRun := false
			for _, pattern := range tt.patterns {
				m.On(pattern, func(value interface{}, path []interface{}) {
					callbackRun = true
				})
			}
			m.CheckPatterns(tt.args.path, tt.args.value)
			if callbackRun != tt.wantCallbackRun {
				t.Errorf("SimplePathMatcher.CheckPatterns() callback run = %v, want %v", callbackRun, tt.wantCallbackRun)
			}
		})
	}
}

func TestNewStreamingJsonParser(t *testing.T) {
	type args struct {
		matcher     *SimplePathMatcher
		realtime    bool
		incremental bool
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "create parser with realtime and incremental",
			args: args{
				matcher:     NewSimplePathMatcher(),
				realtime:    true,
				incremental: true,
			},
		},
		{
			name: "create parser without realtime",
			args: args{
				matcher:     NewSimplePathMatcher(),
				realtime:    false,
				incremental: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewStreamingJsonParser(tt.args.matcher, tt.args.realtime, tt.args.incremental)
			if got == nil {
				t.Errorf("NewStreamingJsonParser() = nil, want non-nil")
				return
			}
			if got.matcher != tt.args.matcher {
				t.Errorf("NewStreamingJsonParser().matcher = %v, want %v", got.matcher, tt.args.matcher)
			}
			if got.realtime != tt.args.realtime {
				t.Errorf("NewStreamingJsonParser().realtime = %v, want %v", got.realtime, tt.args.realtime)
			}
			if got.incremental != tt.args.incremental {
				t.Errorf("NewStreamingJsonParser().incremental = %v, want %v", got.incremental, tt.args.incremental)
			}
		})
	}
}

func TestStreamingJsonParser_Reset(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "reset parser state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			p.buffer = "test"
			p.isInString = true
			p.Reset()
			if p.buffer != "" {
				t.Errorf("StreamingJsonParser.Reset() buffer = %v, want empty", p.buffer)
			}
			if p.isInString {
				t.Errorf("StreamingJsonParser.Reset() isInString = true, want false")
			}
			if p.state != VALUE {
				t.Errorf("StreamingJsonParser.Reset() state = %v, want VALUE", p.state)
			}
		})
	}
}

func TestStreamingJsonParser_Write(t *testing.T) {
	type args struct {
		chunk string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name:    "parse simple string",
			args:    args{chunk: `"hello"`},
			wantErr: false,
		},
		{
			name:    "parse simple number",
			args:    args{chunk: `123`},
			wantErr: false,
		},
		{
			name:    "parse simple object",
			args:    args{chunk: `{"name":"test"}`},
			wantErr: false,
		},
		{
			name:    "parse simple array",
			args:    args{chunk: `[1,2,3]`},
			wantErr: false,
		},
		{
			name:    "parse boolean true",
			args:    args{chunk: `true`},
			wantErr: false,
		},
		{
			name:    "parse boolean false",
			args:    args{chunk: `false`},
			wantErr: false,
		},
		{
			name:    "parse null",
			args:    args{chunk: `null`},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			err := p.Write(tt.args.chunk)
			if (err != nil) != tt.wantErr {
				t.Errorf("StreamingJsonParser.Write() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStreamingJsonParser_handleEscapedChar(t *testing.T) {
	type fields struct {
		buffer string
	}
	type args struct {
		char rune
	}
	tests := []struct {
		name       string
		fields     fields
		args       args
		wantBuffer string
		wantErr    bool
	}{
		{
			name:       "escape newline",
			fields:     fields{buffer: ""},
			args:       args{char: 'n'},
			wantBuffer: "\n",
			wantErr:    false,
		},
		{
			name:       "escape tab",
			fields:     fields{buffer: ""},
			args:       args{char: 't'},
			wantBuffer: "\t",
			wantErr:    false,
		},
		{
			name:       "escape backslash",
			fields:     fields{buffer: ""},
			args:       args{char: '\\'},
			wantBuffer: "\\",
			wantErr:    false,
		},
		{
			name:       "escape quote",
			fields:     fields{buffer: ""},
			args:       args{char: '"'},
			wantBuffer: "\"",
			wantErr:    false,
		},
		{
			name:       "escape slash",
			fields:     fields{buffer: ""},
			args:       args{char: '/'},
			wantBuffer: "/",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			p.buffer = tt.fields.buffer
			p.isEscaped = true
			err := p.handleEscapedChar(tt.args.char)
			if (err != nil) != tt.wantErr {
				t.Errorf("StreamingJsonParser.handleEscapedChar() error = %v, wantErr %v", err, tt.wantErr)
			}
			if p.buffer != tt.wantBuffer {
				t.Errorf("StreamingJsonParser.handleEscapedChar() buffer = %v, want %v", p.buffer, tt.wantBuffer)
			}
			if p.isEscaped {
				t.Errorf("StreamingJsonParser.handleEscapedChar() isEscaped should be false")
			}
		})
	}
}

func TestStreamingJsonParser_handleUnicodeEscape(t *testing.T) {
	type fields struct {
		unicodeBuffer string
		unicodeCount  int
		buffer        string
	}
	type args struct {
		char rune
	}
	tests := []struct {
		name               string
		fields             fields
		args               args
		wantShouldContinue bool
		wantBuffer         string
		wantErr            bool
	}{
		{
			name: "first hex char",
			fields: fields{
				unicodeBuffer: "",
				unicodeCount:  1,
				buffer:        "",
			},
			args:               args{char: '0'},
			wantShouldContinue: true,
			wantBuffer:         "",
			wantErr:            false,
		},
		{
			name: "complete unicode sequence",
			fields: fields{
				unicodeBuffer: "004",
				unicodeCount:  4,
				buffer:        "",
			},
			args:               args{char: '1'},
			wantShouldContinue: true,
			wantBuffer:         "A",
			wantErr:            false,
		},
		{
			name: "invalid hex char",
			fields: fields{
				unicodeBuffer: "00",
				unicodeCount:  3,
				buffer:        "",
			},
			args:               args{char: 'g'},
			wantShouldContinue: false,
			wantBuffer:         "\\u00",
			wantErr:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			p.unicodeBuffer = tt.fields.unicodeBuffer
			p.unicodeCount = tt.fields.unicodeCount
			p.buffer = tt.fields.buffer
			p.isEscaped = true

			gotShouldContinue, err := p.handleUnicodeEscape(tt.args.char)
			if (err != nil) != tt.wantErr {
				t.Errorf("StreamingJsonParser.handleUnicodeEscape() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotShouldContinue != tt.wantShouldContinue {
				t.Errorf("StreamingJsonParser.handleUnicodeEscape() shouldContinue = %v, want %v", gotShouldContinue, tt.wantShouldContinue)
			}
			if p.buffer != tt.wantBuffer {
				t.Errorf("StreamingJsonParser.handleUnicodeEscape() buffer = %v, want %v", p.buffer, tt.wantBuffer)
			}
		})
	}
}

func TestStreamingJsonParser_GetResult(t *testing.T) {
	tests := []struct {
		name string
		json string
		want interface{}
	}{
		{
			name: "get simple object result",
			json: `{"name":"test"}`,
			want: map[string]interface{}{"name": "test"},
		},
		{
			name: "get array result",
			json: `[1,2,3]`,
			want: &[]interface{}{float64(1), float64(2), float64(3)},
		},
		{
			name: "get string result",
			json: `"hello"`,
			want: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			_ = p.Write(tt.json)
			got := p.GetResult()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("StreamingJsonParser.GetResult() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStreamingJsonParser_buildPathKey(t *testing.T) {
	type args struct {
		path []interface{}
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "simple path",
			args: args{path: []interface{}{"data", "name"}},
			want: "data.name",
		},
		{
			name: "path with array index",
			args: args{path: []interface{}{"items", 0, "name"}},
			want: "items.0.name",
		},
		{
			name: "empty path",
			args: args{path: []interface{}{}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			got := p.buildPathKey(tt.args.path)
			if got != tt.want {
				t.Errorf("StreamingJsonParser.buildPathKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStreamingJsonParser_notifyKeyStart(t *testing.T) {
	type args struct {
		path []interface{}
	}
	tests := []struct {
		name            string
		args            args
		wantCallbackRun bool
	}{
		{
			name:            "notify with callback set",
			args:            args{path: []interface{}{"data", "name"}},
			wantCallbackRun: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := NewSimplePathMatcher()
			callbackRun := false
			matcher.OnKeyStart(func(path []interface{}) {
				callbackRun = true
			})
			p := NewStreamingJsonParser(matcher, false, false)
			p.notifyKeyStart(tt.args.path)
			if callbackRun != tt.wantCallbackRun {
				t.Errorf("StreamingJsonParser.notifyKeyStart() callback run = %v, want %v", callbackRun, tt.wantCallbackRun)
			}
		})
	}
}

func TestStreamingJsonParser_notifyKeyComplete(t *testing.T) {
	type args struct {
		path       []interface{}
		finalValue interface{}
	}
	tests := []struct {
		name            string
		args            args
		wantCallbackRun bool
	}{
		{
			name: "notify with callback set",
			args: args{
				path:       []interface{}{"data", "name"},
				finalValue: "test",
			},
			wantCallbackRun: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := NewSimplePathMatcher()
			callbackRun := false
			matcher.OnKeyComplete(func(path []interface{}, finalValue interface{}) {
				callbackRun = true
			})
			p := NewStreamingJsonParser(matcher, false, false)
			p.pathValues[p.buildPathKey(tt.args.path)] = tt.args.finalValue
			p.notifyKeyComplete(tt.args.path)
			if callbackRun != tt.wantCallbackRun {
				t.Errorf("StreamingJsonParser.notifyKeyComplete() callback run = %v, want %v", callbackRun, tt.wantCallbackRun)
			}
		})
	}
}

func TestStreamingJsonParser_RealtimeIncremental(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		pattern     string
		realtime    bool
		incremental bool
		wantCalls   int
	}{
		{
			name:        "realtime incremental mode",
			json:        `{"message":"hello"}`,
			pattern:     "$.message",
			realtime:    true,
			incremental: true,
			wantCalls:   5,
		},
		{
			name:        "realtime cumulative mode",
			json:        `{"message":"hello"}`,
			pattern:     "$.message",
			realtime:    true,
			incremental: false,
			wantCalls:   6,
		},
		{
			name:        "non-realtime mode",
			json:        `{"message":"hello"}`,
			pattern:     "$.message",
			realtime:    false,
			incremental: false,
			wantCalls:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := NewSimplePathMatcher()
			callCount := 0
			matcher.On(tt.pattern, func(value interface{}, path []interface{}) {
				callCount++
			})
			p := NewStreamingJsonParser(matcher, tt.realtime, tt.incremental)
			_ = p.Write(tt.json)
			if callCount != tt.wantCalls {
				t.Errorf("StreamingJsonParser realtime/incremental mode callback calls = %v, want %v", callCount, tt.wantCalls)
			}
		})
	}
}

func TestStreamingJsonParser_ComplexJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "nested object",
			json:    `{"data":{"user":{"name":"test","age":30}}}`,
			wantErr: false,
		},
		{
			name:    "nested array",
			json:    `{"items":[{"id":1,"name":"item1"},{"id":2,"name":"item2"}]}`,
			wantErr: false,
		},
		{
			name:    "mixed types",
			json:    `{"string":"test","number":123,"boolean":true,"null":null,"array":[1,2,3],"object":{"key":"value"}}`,
			wantErr: false,
		},
		{
			name:    "escaped characters",
			json:    `{"text":"line1\nline2\ttab\"quote\\backslash"}`,
			wantErr: false,
		},
		{
			name:    "unicode escape",
			json:    `{"unicode":"\u0041\u0042\u0043"}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			err := p.Write(tt.json)
			if (err != nil) != tt.wantErr {
				t.Errorf("StreamingJsonParser.Write() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStreamingJsonParser_KeyStartAndComplete(t *testing.T) {
	tests := []struct {
		name             string
		json             string
		wantKeyStarts    int
		wantKeyCompletes int
	}{
		{
			name:             "simple object",
			json:             `{"name":"test","age":30}`,
			wantKeyStarts:    2,
			wantKeyCompletes: 2,
		},
		{
			name:             "nested object",
			json:             `{"user":{"name":"test"}}`,
			wantKeyStarts:    2,
			wantKeyCompletes: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := NewSimplePathMatcher()
			keyStartCount := 0
			keyCompleteCount := 0
			matcher.OnKeyStart(func(path []interface{}) {
				keyStartCount++
			})
			matcher.OnKeyComplete(func(path []interface{}, finalValue interface{}) {
				keyCompleteCount++
			})
			p := NewStreamingJsonParser(matcher, false, false)
			_ = p.Write(tt.json)
			if keyStartCount != tt.wantKeyStarts {
				t.Errorf("StreamingJsonParser key starts = %v, want %v", keyStartCount, tt.wantKeyStarts)
			}
			if keyCompleteCount != tt.wantKeyCompletes {
				t.Errorf("StreamingJsonParser key completes = %v, want %v", keyCompleteCount, tt.wantKeyCompletes)
			}
		})
	}
}

func TestStreamingJsonParser_WriteChunks(t *testing.T) {
	type args struct {
		chunks []string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "write object in chunks",
			args: args{
				chunks: []string{`{"na`, `me":"`, `test`, `"}`},
			},
			wantErr: false,
		},
		{
			name: "write array in chunks",
			args: args{
				chunks: []string{`[1,`, `2,`, `3]`},
			},
			wantErr: false,
		},
		{
			name: "write nested structure in chunks",
			args: args{
				chunks: []string{`{"data":`, `{"user":`, `"test"`, `}}`},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewStreamingJsonParser(NewSimplePathMatcher(), false, false)
			var err error
			for _, chunk := range tt.args.chunks {
				err = p.Write(chunk)
				if err != nil {
					break
				}
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("StreamingJsonParser.Write() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
