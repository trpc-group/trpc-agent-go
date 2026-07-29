//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// OutputResponseProcessor processes final responses and handles output_key and output_schema functionality.
type OutputResponseProcessor struct {
	outputKey    string
	outputSchema map[string]any
}

// NewOutputResponseProcessor creates a new instance of OutputResponseProcessor.
func NewOutputResponseProcessor(
	outputKey string,
	outputSchema map[string]any,
) *OutputResponseProcessor {
	return &OutputResponseProcessor{
		outputKey:    outputKey,
		outputSchema: outputSchema,
	}
}

// ProcessResponse processes the model response and handles output_key and output_schema functionality.
// This mimics the behavior of adk-python's output processing using event.actions.state_delta pattern.
func (p *OutputResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil || !rsp.IsFinalResponse() ||
		(invocation.StructuredOutput == nil && invocation.StructuredOutputType == nil &&
			p.outputKey == "" && p.outputSchema == nil) {
		return
	}
	// Only process complete (non-partial) responses.
	// Extract text content from the response.
	content, ok := p.extractFinalContent(rsp)
	if !ok {
		return
	}
	jsonObject, ok := extractFirstJSONObject(content)

	if ok {
		// 1) Emit structured output payload if configured.
		p.emitStructuredOutput(ctx, invocation, jsonObject, ch)
	}

	// 2) Handle output_key functionality (raw persistence, optional schema validation).
	p.handleOutputKey(ctx, invocation, content, jsonObject, ch)
}

// extractFinalContent returns the final text content if response is complete.
func (p *OutputResponseProcessor) extractFinalContent(rsp *model.Response) (string, bool) {
	if rsp == nil || rsp.IsPartial {
		return "", false
	}
	if len(rsp.Choices) == 0 || rsp.Choices[0].Message.Content == "" {
		return "", false
	}
	return rsp.Choices[0].Message.Content, true
}

// emitStructuredOutput emits a structured output payload event when structured output is requested.
//
// If StructuredOutputType is set, the payload is unmarshaled into that Go type (typed mode).
// Otherwise, if StructuredOutput is set, the payload is unmarshaled into an untyped value (map/slice/etc).
func (p *OutputResponseProcessor) emitStructuredOutput(
	ctx context.Context, invocation *agent.Invocation, jsonObject string, ch chan<- *event.Event,
) {
	// Case 1: Typed struct via WithStructuredOutputJSON
	if invocation.StructuredOutputType != nil {
		var instance any
		if invocation.StructuredOutputType.Kind() == reflect.Pointer {
			instance = reflect.New(invocation.StructuredOutputType.Elem()).Interface()
		} else {
			instance = reflect.New(invocation.StructuredOutputType).Interface()
		}
		if err := unmarshalLenient(jsonObject, instance); err != nil {
			log.ErrorfContext(
				ctx,
				"Structured output unmarshal failed: %v; payload: %s",
				err, truncateJSON(jsonObject, 600),
			)
			return
		}
		typedEvt := event.New(
			invocation.InvocationID,
			invocation.AgentName,
			event.WithObject(model.ObjectTypeStateUpdate),
			event.WithStructuredOutputPayload(instance),
		)
		log.DebugContext(ctx, "Emitted typed structured output payload event.")
		agent.EmitEvent(ctx, invocation, ch, typedEvt)
		return
	}

	// Case 2: Untyped payload via WithStructuredOutputJSONSchema
	if invocation.StructuredOutput == nil {
		return
	}
	var parsed any
	if err := unmarshalLenient(jsonObject, &parsed); err != nil {
		log.ErrorfContext(
			ctx,
			"Structured output unmarshal failed: %v; payload: %s",
			err, truncateJSON(jsonObject, 600),
		)
		return
	}
	untypedEvt := event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStructuredOutputPayload(parsed),
	)
	log.DebugContext(ctx, "Emitted untyped structured output payload event.")
	agent.EmitEvent(ctx, invocation, ch, untypedEvt)
}

// handleOutputKey validates and emits state delta for output_key/output_schema cases.
func (p *OutputResponseProcessor) handleOutputKey(ctx context.Context, invocation *agent.Invocation, content string,
	jsonObject string, ch chan<- *event.Event) {
	if p.outputKey == "" && p.outputSchema == nil {
		return
	}
	result := content
	// If output_schema is present, ensure content is JSON.
	if p.outputSchema != nil {
		if jsonObject == "" {
			return
		}
		var parsedJSON any
		if err := unmarshalLenient(jsonObject, &parsedJSON); err != nil {
			log.WarnfContext(
				ctx,
				"Failed to parse output as JSON for output_schema "+
					"validation: %v",
				err,
			)
			return
		}
		// Store the (possibly repaired) JSON string.
		result = sanitizeJSONControlChars(jsonObject)
	}
	// Create a state delta event instead of directly modifying session.
	stateDelta := map[string][]byte{
		p.outputKey: []byte(result),
	}
	// Create and emit an event with state delta for the runner to process.
	stateEvent := event.New(invocation.InvocationID, invocation.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(stateDelta),
	)
	stateEvent.RequiresCompletion = true

	log.DebugfContext(
		ctx,
		"Emitted state delta event with key '%s'.",
		p.outputKey,
	)
	if err := agent.EmitEvent(ctx, invocation, ch, stateEvent); err != nil {
		return
	}

	// Ensure that the state delta is synchronized to the local session before executing the next agent.
	// maybe the next agent need to use delta state before executing the flow.
	completionID := agent.GetAppendEventNoticeKey(stateEvent.ID)
	if err := invocation.AddNoticeChannelAndWait(ctx, completionID,
		agent.WaitNoticeWithoutTimeout); err != nil {
		log.WarnfContext(
			ctx,
			"Failed to add notice channel for completion ID %s: %v",
			completionID,
			err,
		)
	}
}

// unmarshalLenient unmarshals JSON into v. It applies three best-effort repairs
// for common LLM output defects, retrying after each:
//
//  1. Raw (unescaped) control characters inside string literals, e.g. literal
//     newlines in a string value.
//  2. String values emitted without surrounding quotes, e.g.
//     {"reason": 回复结构完整}. This is typical of models running under
//     response_format=json_object (DeepSeek variants), which are not
//     schema-validated by the API.
//  3. A stray token that appears where only a delimiter (',' '}' ']') is valid,
//     i.e. right after a completed value, e.g. {"score":1 2}.
//
// Repairs only run when strict parsing fails, so already-valid JSON is never
// touched.
func unmarshalLenient(jsonObject string, v any) error {
	strictErr := json.Unmarshal([]byte(jsonObject), v)
	if strictErr == nil {
		return nil
	}
	if repaired := sanitizeJSONControlChars(jsonObject); repaired != jsonObject {
		if err := json.Unmarshal([]byte(repaired), v); err == nil {
			return nil
		}
	}
	if quoted := quoteUnquotedStrings(jsonObject); quoted != jsonObject {
		if err := json.Unmarshal([]byte(sanitizeJSONControlChars(quoted)), v); err == nil {
			return nil
		}
	}
	// Final fallback: drop a stray token sitting where only a delimiter is valid
	// (e.g. {"score":1 2}). Retry a few times in case several such tokens exist.
	s := jsonObject
	for attempt := 0; attempt < 4; attempt++ {
		repaired, ok := repairStrayTokenAtSyntaxError(s)
		if !ok {
			break
		}
		s = repaired
		if err := json.Unmarshal([]byte(sanitizeJSONControlChars(s)), v); err == nil {
			return nil
		}
	}
	return strictErr
}

// repairStrayTokenAtSyntaxError inspects the first JSON syntax error in s and,
// when the error is a stray token appearing where only a delimiter is expected
// (immediately after a completed value), deletes that token and returns the
// repaired string.
//
// Go's json.SyntaxError.Offset points at the delimiter (or char) the parser
// choked on, while the offending token sits immediately before it. We therefore
// walk backwards from the offset to locate the stray run and remove it.
// Object-key positions (right after '{' or ',') and missing-comma cases
// (the run contains ':' or '"') are intentionally left untouched, so genuinely
// malformed input still fails as before.
func repairStrayTokenAtSyntaxError(s string) (string, bool) {
	var syntaxErr *json.SyntaxError
	// Use json.RawMessage purely to surface the syntax error offset.
	if err := json.Unmarshal([]byte(s), new(json.RawMessage)); err == nil {
		return "", false
	} else if !errors.As(err, &syntaxErr) {
		return "", false
	}
	off := int(syntaxErr.Offset)
	if off <= 0 || off >= len(s) {
		return "", false
	}
	// End of the junk: skip whitespace backwards from the choking position.
	e := off
	for e > 0 && isJSONSpace(s[e-1]) {
		e--
	}
	// Walk backwards to the start of the junk: it begins right after a value
	// boundary ('}', ']', '"', ')', a digit/letter ending a literal) or a
	// structural delimiter, or after the whitespace that separates it from the
	// preceding value.
	start := e
	for start > 0 {
		c := s[start-1]
		if c == '}' || c == ']' || c == '"' || c == ')' ||
			c == ',' || c == ':' || c == '{' || c == '[' {
			break
		}
		if isJSONSpace(c) {
			break
		}
		start--
	}
	if start >= e {
		return "", false
	}
	// Refuse to delete a run that sits at an object-key or array-element
	// position (right after '{', ',' or '['): that is an unquoted key, not a
	// stray token following a value. Deleting it would silently turn invalid
	// input into "{}".
	if start > 0 {
		pb := s[start-1]
		if pb == '{' || pb == ',' || pb == '[' {
			return "", false
		}
	}
	junk := s[start:e]
	// Refuse to touch runs that look like a missing comma between fields
	// (e.g. {"a":1 "b":3}); deleting them would drop real data.
	if strings.ContainsAny(junk, `:"`) {
		return "", false
	}
	return s[:start] + s[e:], true
}

// isJSONSpace reports whether c is JSON insignificant whitespace.
func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// quoteUnquotedStrings best-effort repairs a common LLM JSON defect where
// string values are emitted without surrounding double quotes, for example
// {"reason": 回复结构完整}. Only bare values that are not already valid JSON
// literals (string/number/true/false/null/object/array) are quoted. Non-ASCII
// bytes (e.g. UTF-8 CJK text) are never interpreted as structural characters,
// so this is effectively a no-op for already-valid JSON.
func quoteUnquotedStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ':' {
			b.WriteByte(c)
			// Locate the value start, copying any whitespace between ':' and
			// the value so positions stay aligned.
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				b.WriteByte(s[j])
				j++
			}
			if j < len(s) {
				vc := s[j]
				if vc != '"' && vc != '{' && vc != '[' && vc != '}' && vc != ']' && vc != ',' {
					// Collect the bare token up to the next top-level delimiter.
					k := j
					abort := false
					for k < len(s) {
						d := s[k]
						if d == ',' || d == '}' || d == ']' {
							break
						}
						if d == '"' || d == '{' || d == '[' {
							// A structural char inside the "value" means this is
							// not a simple unquoted value (e.g. a missing comma
							// between fields). Leave it untouched.
							abort = true
							break
						}
						k++
					}
					if abort {
						b.WriteString(s[j:k])
						i = k - 1
						continue
					}
					token := s[j:k]
					if !isValidJSONLiteral(token) {
						b.WriteByte('"')
						for m := 0; m < len(token); m++ {
							if token[m] == '"' || token[m] == '\\' {
								b.WriteByte('\\')
							}
							b.WriteByte(token[m])
						}
						b.WriteByte('"')
						i = k - 1
						continue
					}
					// Already a valid literal (number/true/false/null): keep it.
					b.WriteString(token)
					i = k - 1
					continue
				}
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// isValidJSONLiteral reports whether tok is a valid JSON literal: a JSON number
// or one of true/false/null. Surrounding whitespace is ignored.
func isValidJSONLiteral(tok string) bool {
	t := strings.TrimSpace(tok)
	if t == "" {
		return false
	}
	if t == "true" || t == "false" || t == "null" {
		return true
	}
	if _, err := strconv.ParseFloat(t, 64); err == nil {
		return true
	}
	return false
}

// sanitizeJSONControlChars escapes raw control characters (U+0000..U+001F)
// that appear inside JSON string literals, which strict JSON parsers reject.
// Characters outside string literals are left untouched.
func sanitizeJSONControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escaped := false
	changed := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			case c < 0x20:
				switch c {
				case '\n':
					b.WriteString(`\n`)
				case '\r':
					b.WriteString(`\r`)
				case '\t':
					b.WriteString(`\t`)
				default:
					fmt.Fprintf(&b, `\u%04x`, c)
				}
				changed = true
				continue
			}
		} else if c == '"' {
			inString = true
		}
		b.WriteByte(c)
	}
	if !changed {
		return s
	}
	return b.String()
}

// truncateJSON returns s unchanged if it is short enough, otherwise a prefix of
// at most n bytes followed by an ellipsis. Used to keep diagnostic logs bounded.
func truncateJSON(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// extractFirstJSONObject tries to extract the first balanced top-level JSON object from s.
func extractFirstJSONObject(s string) (string, bool) {
	start := findJSONStart(s)
	if start == -1 {
		return "", false
	}
	return scanBalancedJSON(s, start)
}

// findJSONStart finds the index of the first opening bracket in s.
func findJSONStart(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			return i
		}
	}
	return -1
}

// scanBalancedJSON scans a string for a balanced JSON object.
func scanBalancedJSON(s string, start int) (string, bool) {
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			default:
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			top := stack[len(stack)-1]
			if (top == '{' && c == '}') || (top == '[' && c == ']') {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return s[start : i+1], true
				}
			} else {
				return "", false
			}
		default:
		}
	}
	return "", false
}
