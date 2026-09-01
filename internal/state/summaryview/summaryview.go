//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package summaryview stores the model-visible session history used by
// request-driven session summarization.
package summaryview

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/statecopy"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const stateKey = "trpc_agent.summary.model_visible_view"

// Binding reasons explain why a model-visible view is or is not bound to the
// final model request. They are stable, low-cardinality diagnostic values
// recorded where the binding decision is made, because a later reader cannot
// reconstruct which stage invalidated the binding.
const (
	// BindingReasonBound marks a view bound to the model request.
	BindingReasonBound = "bound"
	// BindingReasonAbsent marks a missing model-visible view.
	BindingReasonAbsent = "absent"
	// BindingReasonNotFinalized marks a projection that has not yet been
	// finalized against a model request.
	BindingReasonNotFinalized = "not_finalized"
	// BindingReasonInvalidated marks a view invalidated explicitly, for
	// example after provider-side token tailoring rewrote the request.
	BindingReasonInvalidated = "invalidated"
	// BindingReasonRequestMismatch marks a view whose projected items could
	// not all be located in the final model request.
	BindingReasonRequestMismatch = "request_mismatch"
	// BindingReasonTransformMismatch marks a view whose projected items or
	// whose transform provenance did not match the transform input.
	BindingReasonTransformMismatch = "transform_mismatch"
	// BindingReasonRebaseFailed marks a view whose items could not be mapped
	// onto the transform output.
	BindingReasonRebaseFailed = "rebase_failed"
)

type contextKey struct{}

// invocationState keeps an immutable snapshot opaque to Invocation.View's
// generic state cloner. Mutations must replace the holder instead of changing
// the stored view in place.
type invocationState struct {
	view               *View
	bindingInvalidated bool
}

// Boundary identifies the latest stored event completely represented by the
// model request prefix ending at an item.
type Boundary struct {
	EventID   string
	Timestamp time.Time
}

// IsZero reports whether the boundary identifies no stored event.
func (b Boundary) IsZero() bool {
	return b.EventID == "" && b.Timestamp.IsZero()
}

// Item maps one model-visible history message to its stored-event boundary.
type Item struct {
	Message        model.Message
	EffectiveEvent event.Event
	Boundary       Boundary
	RequestIndex   int
}

// View is a snapshot of the session-derived history in one model request.
// It intentionally remains unchanged when model responses are appended after
// the request, so asynchronous summary checks use only content the model has
// actually seen.
type View struct {
	SessionID              string
	FilterKey              string
	Items                  []Item
	PreviousSummary        string
	PreviousSummaryInItems bool
	RequestTokens          int
	ContentRequestLength   int
	Bound                  bool
	// BindingReason explains the current value of Bound. It is set at every
	// stage that decides or invalidates the binding.
	BindingReason string
}

// AttachProjection stores the content processor's pre-finalization projection.
func AttachProjection(inv *agent.Invocation, view *View) {
	if inv == nil || view == nil {
		return
	}
	next := cloneView(view)
	setBindingReason(next, BindingReasonNotFinalized)
	inv.SetState(stateKey, &invocationState{view: next})
}

// setBindingReason records why view is or is not bound. unbound is the reason
// that applies when the binding attempt at this stage did not succeed.
func setBindingReason(view *View, unbound string) {
	if view == nil {
		return
	}
	if view.Bound {
		view.BindingReason = BindingReasonBound
		return
	}
	view.BindingReason = unbound
}

// Clear removes the current projection and finalized view.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(stateKey)
}

// Finalize binds projected history items to their positions in the final model
// request and records the request token count. RequestTokens remains useful
// when binding is unavailable, but unbound items must not be treated as content
// proven to be visible to the model.
func Finalize(inv *agent.Invocation, req *model.Request, requestTokens int) {
	if inv == nil || req == nil {
		return
	}
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.view == nil {
		return
	}
	view := state.view
	next := cloneView(view)
	next.RequestTokens = requestTokens
	next.Bound = !state.bindingInvalidated && bindItems(next, req.Messages)
	if !state.bindingInvalidated {
		// An already invalidated view keeps the reason recorded by the stage
		// that invalidated it.
		setBindingReason(next, BindingReasonRequestMismatch)
	}
	inv.SetState(stateKey, &invocationState{
		view:               next,
		bindingInvalidated: state.bindingInvalidated,
	})
}

// RebaseAfterTransform maps a projected view through a message transform.
// When non-nil, sourceIndexes must contain one input-message index for every
// output message. A nil slice means output message i came from input message i.
// The view remains unbound when the provenance does not completely represent
// every projected history item.
func RebaseAfterTransform(
	inv *agent.Invocation,
	before []model.Message,
	after []model.Message,
	sourceIndexes []int,
) bool {
	if inv == nil {
		return false
	}
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.view == nil {
		return false
	}
	if state.bindingInvalidated {
		return false
	}
	boundItems, boundBefore := bindItemsForTransform(state.view, before)
	boundBefore = boundBefore && sourceIndexesMatch(
		sourceIndexes,
		len(after),
		len(before),
	)
	if !boundBefore {
		next := cloneView(state.view)
		next.ContentRequestLength = len(after)
		storeInvalidated(inv, next, BindingReasonTransformMismatch)
		return false
	}
	transformed, ok := rebaseItems(
		boundItems,
		after,
		sourceIndexes,
		len(before),
	)
	if !ok {
		next := cloneView(state.view)
		next.ContentRequestLength = len(after)
		storeInvalidated(inv, next, BindingReasonRebaseFailed)
		return false
	}
	next := *state.view
	next.Items = transformed
	next.ContentRequestLength = len(after)
	next.Bound = true
	next.BindingReason = BindingReasonBound
	inv.SetState(stateKey, &invocationState{view: &next})
	return true
}

func bindItemsForTransform(view *View, messages []model.Message) ([]Item, bool) {
	if view == nil || len(view.Items) == 0 || len(messages) == 0 {
		return nil, false
	}
	items := make([]Item, len(view.Items))
	shift := len(messages) - view.ContentRequestLength
	previous := -1
	for i := range view.Items {
		index := findItem(
			messages,
			view.Items[i].Message,
			view.Items[i].RequestIndex,
			shift,
			previous+1,
		)
		if index < 0 {
			return nil, false
		}
		items[i] = view.Items[i]
		items[i].RequestIndex = index
		previous = index
	}
	return items, true
}

func rebaseItems(
	items []Item,
	after []model.Message,
	sourceIndexes []int,
	beforeLength int,
) ([]Item, bool) {
	itemBySource, ok := indexItemsBySource(items)
	if !ok {
		return nil, false
	}
	remaining, ok := countOutputsByItem(
		sourceIndexes,
		len(after),
		beforeLength,
		itemBySource,
		len(items),
	)
	if !ok {
		return nil, false
	}

	transformed := make([]Item, 0, len(after))
	completed := make([]bool, len(items))
	frontier := 0
	for outputIndex := range after {
		sourceIndex := sourceIndexForOutput(sourceIndexes, outputIndex)
		itemIndex, exists := itemBySource[sourceIndex]
		if !exists {
			continue
		}
		item := items[itemIndex]
		item.Message = statecopy.Message(after[outputIndex])
		item.EffectiveEvent = cloneEvent(item.EffectiveEvent)
		setEffectiveMessage(&item.EffectiveEvent, item.Message)
		item.RequestIndex = outputIndex
		item.Boundary = Boundary{}
		transformed = append(transformed, item)

		remaining[itemIndex]--
		completed[itemIndex] = remaining[itemIndex] == 0
		safeBoundary := advanceCompletedFrontier(items, completed, &frontier)
		if !safeBoundary.IsZero() {
			transformed[len(transformed)-1].Boundary = safeBoundary
		}
	}
	return transformed, frontier == len(items) && len(transformed) > 0
}

func indexItemsBySource(items []Item) (map[int]int, bool) {
	itemBySource := make(map[int]int, len(items))
	for i := range items {
		requestIndex := items[i].RequestIndex
		if _, exists := itemBySource[requestIndex]; exists {
			return nil, false
		}
		itemBySource[requestIndex] = i
	}
	return itemBySource, true
}

func countOutputsByItem(
	sourceIndexes []int,
	outputCount int,
	beforeLength int,
	itemBySource map[int]int,
	itemCount int,
) ([]int, bool) {
	if !sourceIndexesMatch(sourceIndexes, outputCount, beforeLength) {
		return nil, false
	}
	remaining := make([]int, itemCount)
	for outputIndex := 0; outputIndex < outputCount; outputIndex++ {
		sourceIndex := sourceIndexForOutput(sourceIndexes, outputIndex)
		if sourceIndex < 0 || sourceIndex >= beforeLength {
			return nil, false
		}
		if itemIndex, exists := itemBySource[sourceIndex]; exists {
			remaining[itemIndex]++
		}
	}
	for _, count := range remaining {
		if count == 0 {
			return nil, false
		}
	}
	return remaining, true
}

func sourceIndexesMatch(
	sourceIndexes []int,
	outputCount int,
	inputCount int,
) bool {
	if sourceIndexes == nil {
		return outputCount == inputCount
	}
	return len(sourceIndexes) == outputCount
}

func sourceIndexForOutput(sourceIndexes []int, outputIndex int) int {
	if sourceIndexes == nil {
		return outputIndex
	}
	return sourceIndexes[outputIndex]
}

func advanceCompletedFrontier(
	items []Item,
	completed []bool,
	frontier *int,
) Boundary {
	var safeBoundary Boundary
	for *frontier < len(items) && completed[*frontier] {
		if !items[*frontier].Boundary.IsZero() {
			safeBoundary = items[*frontier].Boundary
		}
		*frontier++
	}
	return safeBoundary
}

// InvalidateBinding prevents the current projected view from being used as
// proof of model-visible history.
func InvalidateBinding(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.view == nil {
		return
	}
	storeInvalidated(inv, cloneView(state.view), BindingReasonInvalidated)
}

func storeInvalidated(inv *agent.Invocation, view *View, reason string) {
	view.Bound = false
	view.BindingReason = reason
	inv.SetState(stateKey, &invocationState{
		view:               view,
		bindingInvalidated: true,
	})
}

// Snapshot returns an isolated copy of the latest model-visible view.
func Snapshot(inv *agent.Invocation) (*View, bool) {
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.view == nil {
		return nil, false
	}
	return cloneView(state.view), true
}

// ContextWithView attaches an isolated model-visible view to ctx.
func ContextWithView(ctx context.Context, view *View) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if view == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, cloneView(view))
}

// FromContext returns an isolated model-visible view attached to ctx.
func FromContext(ctx context.Context) (*View, bool) {
	if ctx == nil {
		return nil, false
	}
	view, ok := ctx.Value(contextKey{}).(*View)
	if !ok || view == nil {
		return nil, false
	}
	return cloneView(view), true
}

// Binding describes the model-visible view state used by summary diagnostics.
// It carries only counts and stable reason values, never message content.
type Binding struct {
	// Present reports whether a model-visible view was available.
	Present bool
	// Bound reports whether the view items are proven to map to the model
	// request that the model actually saw.
	Bound bool
	// Reason is the stable binding reason recorded by the stage that decided
	// the binding. It is BindingReasonAbsent when no view is present.
	Reason string
	// Items is the number of model-visible history items in the view.
	Items int
	// RequestTokens is the token count recorded for the request the view was
	// finalized against. It remains meaningful when Bound is false.
	RequestTokens int
}

// BindingFromContext reports the binding state of the model-visible view
// attached to ctx. It reads the stored snapshot without copying view items so
// diagnostics never add a history copy. A missing view reports Present=false
// with BindingReasonAbsent.
func BindingFromContext(ctx context.Context) Binding {
	if ctx == nil {
		return Binding{Reason: BindingReasonAbsent}
	}
	view, ok := ctx.Value(contextKey{}).(*View)
	if !ok || view == nil {
		return Binding{Reason: BindingReasonAbsent}
	}
	return bindingFromView(view)
}

// BindingFromInvocation reports the binding state stored on inv. GetStateValue
// holds the invocation lock only while retrieving the immutable
// invocationState pointer; field reads then use that snapshot, which is never
// mutated in place. The result does not copy view items. A missing invocation
// or view reports Present=false with BindingReasonAbsent. Reason is always one
// of the closed binding-reason constants.
func BindingFromInvocation(inv *agent.Invocation) Binding {
	if inv == nil {
		return Binding{Reason: BindingReasonAbsent}
	}
	state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
	if !ok || state == nil || state.view == nil {
		return Binding{Reason: BindingReasonAbsent}
	}
	return bindingFromView(state.view)
}

func bindingFromView(view *View) Binding {
	if view == nil {
		return Binding{Reason: BindingReasonAbsent}
	}
	return Binding{
		Present:       true,
		Bound:         view.Bound,
		Reason:        view.bindingReason(),
		Items:         len(view.Items),
		RequestTokens: view.RequestTokens,
	}
}

// bindingReason normalizes views built before a binding decision was recorded.
func (v *View) bindingReason() string {
	if v.Bound {
		return BindingReasonBound
	}
	if v.BindingReason == "" {
		return BindingReasonNotFinalized
	}
	return v.BindingReason
}

// Events returns the model-visible history as event-shaped callback input.
func (v *View) Events() []event.Event {
	if v == nil || len(v.Items) == 0 {
		return nil
	}
	events := make([]event.Event, len(v.Items))
	for i := range v.Items {
		events[i] = cloneEvent(v.Items[i].EffectiveEvent)
	}
	return events
}

// PrefixBoundary returns the latest stored-event boundary in the first count
// items. Context-only anchors covered by an older summary have zero boundaries
// and are ignored.
func (v *View) PrefixBoundary(count int) (Boundary, bool) {
	if v == nil || count <= 0 {
		return Boundary{}, false
	}
	if count > len(v.Items) {
		count = len(v.Items)
	}
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	return v.BoundaryForItems(indexes)
}

// BoundaryForItems returns the latest stored-event boundary represented by
// the selected view item indexes.
func (v *View) BoundaryForItems(indexes []int) (Boundary, bool) {
	if v == nil {
		return Boundary{}, false
	}
	for i := len(indexes) - 1; i >= 0; i-- {
		index := indexes[i]
		if index < 0 || index >= len(v.Items) {
			return Boundary{}, false
		}
		if !v.Items[index].Boundary.IsZero() {
			return v.Items[index].Boundary, true
		}
	}
	return Boundary{}, false
}

// PrefixMessages returns the parent request prefix containing fixed messages
// followed by exactly the first count model-visible history items. It returns
// false when the projection could not be bound safely to the parent request.
func (v *View) PrefixMessages(parent []model.Message, count int) ([]model.Message, bool) {
	if v == nil || !v.Bound || count <= 0 || count > len(v.Items) {
		return nil, false
	}
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	return v.MessagesForItems(parent, indexes)
}

// MessagesForItems returns the fixed request prefix followed by the selected
// model-visible history items in their original order.
func (v *View) MessagesForItems(
	parent []model.Message,
	indexes []int,
) ([]model.Message, bool) {
	if v == nil || !v.Bound || len(v.Items) == 0 || len(indexes) == 0 {
		return nil, false
	}
	first := v.Items[0].RequestIndex
	if first < 0 || first > len(parent) {
		return nil, false
	}
	messages := make([]model.Message, 0, first+len(indexes))
	messages = append(messages, parent[:first]...)
	previous := first - 1
	previousItem := -1
	for _, itemIndex := range indexes {
		if itemIndex <= previousItem || itemIndex < 0 || itemIndex >= len(v.Items) {
			return nil, false
		}
		index := v.Items[itemIndex].RequestIndex
		if index <= previous || index < first || index >= len(parent) {
			return nil, false
		}
		messages = append(messages, parent[index])
		previous = index
		previousItem = itemIndex
	}
	return messages, true
}

func bindItems(view *View, messages []model.Message) bool {
	if view == nil || len(view.Items) == 0 || len(messages) == 0 {
		return false
	}
	shift := len(messages) - view.ContentRequestLength
	previous := -1
	for i := range view.Items {
		item := &view.Items[i]
		index := findItem(messages, item.Message, item.RequestIndex, shift, previous+1)
		if index < 0 {
			return false
		}
		item.RequestIndex = index
		item.Message = statecopy.Message(messages[index])
		setEffectiveMessage(&item.EffectiveEvent, item.Message)
		previous = index
	}
	return true
}

func findItem(
	messages []model.Message,
	want model.Message,
	original int,
	shift int,
	start int,
) int {
	for _, candidate := range []int{original + shift, original} {
		if candidate >= start && candidate < len(messages) &&
			messageIdentityMatches(messages[candidate], want) {
			return candidate
		}
	}
	for i := start; i < len(messages); i++ {
		if messageIdentityMatches(messages[i], want) {
			return i
		}
	}
	return -1
}

func messageIdentityMatches(got, want model.Message) bool {
	if reflect.DeepEqual(got, want) {
		return true
	}
	if got.Role != want.Role {
		return false
	}
	if want.ToolID != "" || got.ToolID != "" {
		return want.ToolID != "" && got.ToolID == want.ToolID
	}
	wantIDs := toolCallIDs(want.ToolCalls)
	gotIDs := toolCallIDs(got.ToolCalls)
	if len(wantIDs) > 0 || len(gotIDs) > 0 {
		return reflect.DeepEqual(gotIDs, wantIDs)
	}
	return got.Content == want.Content &&
		got.ReasoningContent == want.ReasoningContent &&
		reflect.DeepEqual(got.ContentParts, want.ContentParts)
}

func toolCallIDs(calls []model.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	ids := make([]string, len(calls))
	for i := range calls {
		ids[i] = calls[i].ID
	}
	return ids
}

func cloneView(view *View) *View {
	if view == nil {
		return nil
	}
	cloned := *view
	cloned.Items = make([]Item, len(view.Items))
	for i := range view.Items {
		cloned.Items[i] = view.Items[i]
		cloned.Items[i].Message = statecopy.Message(view.Items[i].Message)
		cloned.Items[i].EffectiveEvent = cloneEvent(view.Items[i].EffectiveEvent)
	}
	return &cloned
}

func cloneEvent(evt event.Event) event.Event {
	cloned := evt
	if evt.Response != nil {
		cloned.Response = evt.Response.Clone()
		for i := range cloned.Response.Choices {
			choice := &cloned.Response.Choices[i]
			choice.Message = statecopy.Message(evt.Response.Choices[i].Message)
			choice.Delta = statecopy.Message(evt.Response.Choices[i].Delta)
			if evt.Response.Choices[i].FinishReason != nil {
				finishReason := *evt.Response.Choices[i].FinishReason
				choice.FinishReason = &finishReason
			}
		}
		if evt.Response.Error != nil {
			if evt.Response.Error.Param != nil {
				param := *evt.Response.Error.Param
				cloned.Response.Error.Param = &param
			}
			if evt.Response.Error.Code != nil {
				code := *evt.Response.Error.Code
				cloned.Response.Error.Code = &code
			}
		}
	}
	if evt.ParentMetadata != nil {
		parentMetadata := *evt.ParentMetadata
		cloned.ParentMetadata = &parentMetadata
	}
	if evt.LongRunningToolIDs != nil {
		cloned.LongRunningToolIDs = make(map[string]struct{}, len(evt.LongRunningToolIDs))
		for id := range evt.LongRunningToolIDs {
			cloned.LongRunningToolIDs[id] = struct{}{}
		}
	}
	if evt.StateDelta != nil {
		cloned.StateDelta = make(map[string][]byte, len(evt.StateDelta))
		for key, value := range evt.StateDelta {
			cloned.StateDelta[key] = append([]byte(nil), value...)
		}
	}
	if evt.Actions != nil {
		actions := *evt.Actions
		cloned.Actions = &actions
	}
	if evt.Extensions != nil {
		cloned.Extensions = make(map[string]json.RawMessage, len(evt.Extensions))
		for key, value := range evt.Extensions {
			cloned.Extensions[key] = append(json.RawMessage(nil), value...)
		}
	}
	return cloned
}

func setEffectiveMessage(evt *event.Event, message model.Message) {
	if evt == nil {
		return
	}
	if evt.Response == nil {
		evt.Response = &model.Response{}
	} else {
		evt.Response = evt.Response.Clone()
	}
	evt.Response.Choices = []model.Choice{{Message: statecopy.Message(message)}}
}
