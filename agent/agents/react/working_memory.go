// Package react provides the ReAct agent implementation.
package react

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// WorkingMemoryItem represents a single item stored in working memory.
type WorkingMemoryItem struct {
	// ID is a unique identifier for the item.
	ID string `json:"id"`

	// Type categorizes the kind of information (e.g., "entity", "concept", "reference").
	Type string `json:"type"`

	// Name is a human-readable identifier for the item.
	Name string `json:"name"`

	// Content contains the actual information about the item.
	Content interface{} `json:"content"`

	// Metadata stores additional information about the item.
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// CreatedAt is when the item was first added to memory.
	CreatedAt int64 `json:"created_at"`

	// UpdatedAt is when the item was last modified.
	UpdatedAt int64 `json:"updated_at"`

	// AccessCount tracks how many times the item has been accessed.
	AccessCount int `json:"access_count"`

	// LastAccessedAt is when the item was last accessed.
	LastAccessedAt int64 `json:"last_accessed_at,omitempty"`

	// RelatedItems contains IDs of other items that are related to this one.
	RelatedItems []string `json:"related_items,omitempty"`
}

// ReactWorkingMemory extends the ReactMemory interface to provide a more
// sophisticated memory system for ReAct agents to maintain context effectively.
type ReactWorkingMemory interface {
	ReactMemory

	// StoreItem adds or updates an item in working memory.
	StoreItem(ctx context.Context, item *WorkingMemoryItem) error

	// RetrieveItem gets an item by ID from working memory.
	RetrieveItem(ctx context.Context, id string) (*WorkingMemoryItem, error)

	// RetrieveItemsByType gets all items of a specific type.
	RetrieveItemsByType(ctx context.Context, itemType string) ([]*WorkingMemoryItem, error)

	// RetrieveItemsByName gets all items with the given name (may be multiple with different types).
	RetrieveItemsByName(ctx context.Context, name string) ([]*WorkingMemoryItem, error)

	// SearchItems searches for items matching a query across name and content.
	SearchItems(ctx context.Context, query string) ([]*WorkingMemoryItem, error)

	// ListItems returns all items in working memory, optionally filtered and sorted.
	ListItems(ctx context.Context, filter map[string]interface{}, sortBy string) ([]*WorkingMemoryItem, error)

	// RemoveItem deletes an item from working memory.
	RemoveItem(ctx context.Context, id string) error

	// RelateItems creates a bidirectional relationship between two items.
	RelateItems(ctx context.Context, id1 string, id2 string) error

	// GetContext generates a context summary suitable for including in prompts.
	GetContext(ctx context.Context) string
}

// BaseReactWorkingMemory provides a basic implementation of the ReactWorkingMemory interface.
type BaseReactWorkingMemory struct {
	*BaseReactMemory
	items     map[string]*WorkingMemoryItem
	itemsMu   sync.RWMutex
	indexByID map[string]*WorkingMemoryItem
}

// NewBaseReactWorkingMemory creates a new BaseReactWorkingMemory.
func NewBaseReactWorkingMemory() *BaseReactWorkingMemory {
	return &BaseReactWorkingMemory{
		BaseReactMemory: NewBaseReactMemory(),
		items:           make(map[string]*WorkingMemoryItem),
		indexByID:       make(map[string]*WorkingMemoryItem),
	}
}

// StoreItem adds or updates an item in working memory.
func (m *BaseReactWorkingMemory) StoreItem(ctx context.Context, item *WorkingMemoryItem) error {
	if item == nil {
		return nil
	}

	// Fill in missing fields
	if item.ID == "" {
		item.ID = fmt.Sprintf("item-%d", time.Now().UnixNano())
	}

	now := time.Now().Unix()
	if item.CreatedAt == 0 {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	// Store the item
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	m.items[item.ID] = item
	m.indexByID[item.ID] = item

	return nil
}

// RetrieveItem gets an item by ID from working memory.
func (m *BaseReactWorkingMemory) RetrieveItem(ctx context.Context, id string) (*WorkingMemoryItem, error) {
	m.itemsMu.RLock()
	item, ok := m.indexByID[id]
	m.itemsMu.RUnlock()

	if !ok {
		return nil, nil
	}

	// Update access statistics
	m.itemsMu.Lock()
	item.AccessCount++
	item.LastAccessedAt = time.Now().Unix()
	m.itemsMu.Unlock()

	return item, nil
}

// RetrieveItemsByType gets all items of a specific type.
func (m *BaseReactWorkingMemory) RetrieveItemsByType(ctx context.Context, itemType string) ([]*WorkingMemoryItem, error) {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	var result []*WorkingMemoryItem
	for _, item := range m.items {
		if item.Type == itemType {
			result = append(result, item)
		}
	}

	return result, nil
}

// RetrieveItemsByName gets all items with the given name.
func (m *BaseReactWorkingMemory) RetrieveItemsByName(ctx context.Context, name string) ([]*WorkingMemoryItem, error) {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	var result []*WorkingMemoryItem
	for _, item := range m.items {
		if item.Name == name {
			result = append(result, item)
		}
	}

	return result, nil
}

// SearchItems searches for items matching a query across name and content.
func (m *BaseReactWorkingMemory) SearchItems(ctx context.Context, query string) ([]*WorkingMemoryItem, error) {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	var result []*WorkingMemoryItem
	for _, item := range m.items {
		// Simple substring search in name
		if containsSubstring(item.Name, query) {
			result = append(result, item)
			continue
		}

		// Check content if it's a string
		if contentStr, ok := item.Content.(string); ok {
			if containsSubstring(contentStr, query) {
				result = append(result, item)
				continue
			}
		}

		// Try to check content if it can be marshaled to JSON
		if contentBytes, err := json.Marshal(item.Content); err == nil {
			if containsSubstring(string(contentBytes), query) {
				result = append(result, item)
			}
		}
	}

	return result, nil
}

// containsSubstring is a case-insensitive substring check.
func containsSubstring(s, substr string) bool {
	s, substr = strings.ToLower(s), strings.ToLower(substr)
	return strings.Contains(s, substr)
}

// ListItems returns all items in working memory, optionally filtered and sorted.
func (m *BaseReactWorkingMemory) ListItems(
	ctx context.Context,
	filter map[string]interface{},
	sortBy string,
) ([]*WorkingMemoryItem, error) {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	// Collect all items matching the filter
	var result []*WorkingMemoryItem
	for _, item := range m.items {
		if matchesFilter(item, filter) {
			result = append(result, item)
		}
	}

	// Sort results if requested
	if sortBy != "" {
		sortItems(result, sortBy)
	}

	return result, nil
}

// matchesFilter checks if an item matches the given filter.
func matchesFilter(item *WorkingMemoryItem, filter map[string]interface{}) bool {
	if filter == nil {
		return true
	}

	for k, v := range filter {
		switch k {
		case "type":
			if typeVal, ok := v.(string); ok && item.Type != typeVal {
				return false
			}
		case "name":
			if nameVal, ok := v.(string); ok && item.Name != nameVal {
				return false
			}
		case "created_after":
			if tsVal, ok := v.(int64); ok && item.CreatedAt < tsVal {
				return false
			}
		case "created_before":
			if tsVal, ok := v.(int64); ok && item.CreatedAt > tsVal {
				return false
			}
		case "updated_after":
			if tsVal, ok := v.(int64); ok && item.UpdatedAt < tsVal {
				return false
			}
		case "updated_before":
			if tsVal, ok := v.(int64); ok && item.UpdatedAt > tsVal {
				return false
			}
		}
	}

	return true
}

// sortItems sorts items based on the specified field.
func sortItems(items []*WorkingMemoryItem, sortBy string) {
	switch sortBy {
	case "name":
		sort.Slice(items, func(i, j int) bool {
			return items[i].Name < items[j].Name
		})
	case "type":
		sort.Slice(items, func(i, j int) bool {
			return items[i].Type < items[j].Type
		})
	case "created_at":
		sort.Slice(items, func(i, j int) bool {
			return items[i].CreatedAt < items[j].CreatedAt
		})
	case "updated_at":
		sort.Slice(items, func(i, j int) bool {
			return items[i].UpdatedAt < items[j].UpdatedAt
		})
	case "access_count":
		sort.Slice(items, func(i, j int) bool {
			return items[i].AccessCount < items[j].AccessCount
		})
	case "last_accessed_at":
		sort.Slice(items, func(i, j int) bool {
			return items[i].LastAccessedAt < items[j].LastAccessedAt
		})
	}
}

// RemoveItem deletes an item from working memory.
func (m *BaseReactWorkingMemory) RemoveItem(ctx context.Context, id string) error {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	delete(m.items, id)
	delete(m.indexByID, id)

	// Also remove this ID from RelatedItems lists
	for _, item := range m.items {
		for i, relatedID := range item.RelatedItems {
			if relatedID == id {
				// Remove this ID by replacing it with the last element and trimming the slice
				item.RelatedItems[i] = item.RelatedItems[len(item.RelatedItems)-1]
				item.RelatedItems = item.RelatedItems[:len(item.RelatedItems)-1]
				break
			}
		}
	}

	return nil
}

// RelateItems creates a bidirectional relationship between two items.
func (m *BaseReactWorkingMemory) RelateItems(ctx context.Context, id1 string, id2 string) error {
	m.itemsMu.Lock()
	defer m.itemsMu.Unlock()

	// Check that both items exist
	item1, ok1 := m.indexByID[id1]
	item2, ok2 := m.indexByID[id2]
	if !ok1 || !ok2 {
		return fmt.Errorf("one or both items do not exist")
	}

	// Add relation from item1 to item2 if not already present
	hasRelation1to2 := false
	for _, relID := range item1.RelatedItems {
		if relID == id2 {
			hasRelation1to2 = true
			break
		}
	}
	if !hasRelation1to2 {
		item1.RelatedItems = append(item1.RelatedItems, id2)
	}

	// Add relation from item2 to item1 if not already present
	hasRelation2to1 := false
	for _, relID := range item2.RelatedItems {
		if relID == id1 {
			hasRelation2to1 = true
			break
		}
	}
	if !hasRelation2to1 {
		item2.RelatedItems = append(item2.RelatedItems, id1)
	}

	return nil
}

// GetContext generates a context summary suitable for including in prompts.
func (m *BaseReactWorkingMemory) GetContext(ctx context.Context) string {
	m.itemsMu.RLock()
	defer m.itemsMu.RUnlock()

	if len(m.items) == 0 {
		return "No context available."
	}

	var b strings.Builder
	b.WriteString("Working Memory Context:\n\n")

	// Group items by type
	itemsByType := make(map[string][]*WorkingMemoryItem)
	for _, item := range m.items {
		itemsByType[item.Type] = append(itemsByType[item.Type], item)
	}

	// Order types for consistent output
	types := make([]string, 0, len(itemsByType))
	for t := range itemsByType {
		types = append(types, t)
	}
	sort.Strings(types)

	// Output items by type
	for _, t := range types {
		items := itemsByType[t]
		b.WriteString(fmt.Sprintf("## %s\n", strings.Title(t)))

		// Sort items by name for consistent output
		sort.Slice(items, func(i, j int) bool {
			return items[i].Name < items[j].Name
		})

		for _, item := range items {
			b.WriteString(fmt.Sprintf("- %s: ", item.Name))

			// Format the content based on its type
			switch content := item.Content.(type) {
			case string:
				b.WriteString(content)
			case []interface{}:
				b.WriteString("[")
				for i, val := range content {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%v", val))
				}
				b.WriteString("]")
			case map[string]interface{}:
				b.WriteString("{")
				keys := make([]string, 0, len(content))
				for k := range content {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for i, k := range keys {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%s: %v", k, content[k]))
				}
				b.WriteString("}")
			default:
				b.WriteString(fmt.Sprintf("%v", content))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// Clear empties the memory.
func (m *BaseReactWorkingMemory) Clear(ctx context.Context) error {
	// Clear the base ReactMemory
	if err := m.BaseReactMemory.Clear(ctx); err != nil {
		return err
	}

	// Clear working memory items
	m.itemsMu.Lock()
	m.items = make(map[string]*WorkingMemoryItem)
	m.indexByID = make(map[string]*WorkingMemoryItem)
	m.itemsMu.Unlock()

	return nil
}

// ReactWorkingMemoryWrapper wraps a ReactMemory to add working memory capabilities.
type ReactWorkingMemoryWrapper struct {
	ReactMemory
	items     map[string]*WorkingMemoryItem
	itemsMu   sync.RWMutex
	indexByID map[string]*WorkingMemoryItem
}

// NewReactWorkingMemoryWrapper creates a wrapper that adds working memory to any ReactMemory.
func NewReactWorkingMemoryWrapper(mem ReactMemory) *ReactWorkingMemoryWrapper {
	return &ReactWorkingMemoryWrapper{
		ReactMemory: mem,
		items:       make(map[string]*WorkingMemoryItem),
		indexByID:   make(map[string]*WorkingMemoryItem),
	}
}

// StoreItem adds or updates an item in working memory.
func (w *ReactWorkingMemoryWrapper) StoreItem(ctx context.Context, item *WorkingMemoryItem) error {
	if item == nil {
		return nil
	}

	// Fill in missing fields
	if item.ID == "" {
		item.ID = fmt.Sprintf("item-%d", time.Now().UnixNano())
	}

	now := time.Now().Unix()
	if item.CreatedAt == 0 {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	// Store the item
	w.itemsMu.Lock()
	defer w.itemsMu.Unlock()

	w.items[item.ID] = item
	w.indexByID[item.ID] = item

	return nil
}

// RetrieveItem gets an item by ID from working memory.
func (w *ReactWorkingMemoryWrapper) RetrieveItem(ctx context.Context, id string) (*WorkingMemoryItem, error) {
	w.itemsMu.RLock()
	item, ok := w.indexByID[id]
	w.itemsMu.RUnlock()

	if !ok {
		return nil, nil
	}

	// Update access statistics
	w.itemsMu.Lock()
	item.AccessCount++
	item.LastAccessedAt = time.Now().Unix()
	w.itemsMu.Unlock()

	return item, nil
}

// RetrieveItemsByType gets all items of a specific type.
func (w *ReactWorkingMemoryWrapper) RetrieveItemsByType(ctx context.Context, itemType string) ([]*WorkingMemoryItem, error) {
	w.itemsMu.RLock()
	defer w.itemsMu.RUnlock()

	var result []*WorkingMemoryItem
	for _, item := range w.items {
		if item.Type == itemType {
			result = append(result, item)
		}
	}

	return result, nil
}

// RetrieveItemsByName gets all items with the given name.
func (w *ReactWorkingMemoryWrapper) RetrieveItemsByName(ctx context.Context, name string) ([]*WorkingMemoryItem, error) {
	w.itemsMu.RLock()
	defer w.itemsMu.RUnlock()

	var result []*WorkingMemoryItem
	for _, item := range w.items {
		if item.Name == name {
			result = append(result, item)
		}
	}

	return result, nil
}

// SearchItems searches for items matching a query across name and content.
func (w *ReactWorkingMemoryWrapper) SearchItems(ctx context.Context, query string) ([]*WorkingMemoryItem, error) {
	w.itemsMu.RLock()
	defer w.itemsMu.RUnlock()

	var result []*WorkingMemoryItem
	for _, item := range w.items {
		// Simple substring search in name
		if containsSubstring(item.Name, query) {
			result = append(result, item)
			continue
		}

		// Check content if it's a string
		if contentStr, ok := item.Content.(string); ok {
			if containsSubstring(contentStr, query) {
				result = append(result, item)
				continue
			}
		}

		// Try to check content if it can be marshaled to JSON
		if contentBytes, err := json.Marshal(item.Content); err == nil {
			if containsSubstring(string(contentBytes), query) {
				result = append(result, item)
			}
		}
	}

	return result, nil
}

// ListItems returns all items in working memory, optionally filtered and sorted.
func (w *ReactWorkingMemoryWrapper) ListItems(
	ctx context.Context,
	filter map[string]interface{},
	sortBy string,
) ([]*WorkingMemoryItem, error) {
	w.itemsMu.RLock()
	defer w.itemsMu.RUnlock()

	// Collect all items matching the filter
	var result []*WorkingMemoryItem
	for _, item := range w.items {
		if matchesFilter(item, filter) {
			result = append(result, item)
		}
	}

	// Sort results if requested
	if sortBy != "" {
		sortItems(result, sortBy)
	}

	return result, nil
}

// RemoveItem deletes an item from working memory.
func (w *ReactWorkingMemoryWrapper) RemoveItem(ctx context.Context, id string) error {
	w.itemsMu.Lock()
	defer w.itemsMu.Unlock()

	delete(w.items, id)
	delete(w.indexByID, id)

	// Also remove this ID from RelatedItems lists
	for _, item := range w.items {
		for i, relatedID := range item.RelatedItems {
			if relatedID == id {
				// Remove this ID by replacing it with the last element and trimming the slice
				item.RelatedItems[i] = item.RelatedItems[len(item.RelatedItems)-1]
				item.RelatedItems = item.RelatedItems[:len(item.RelatedItems)-1]
				break
			}
		}
	}

	return nil
}

// RelateItems creates a bidirectional relationship between two items.
func (w *ReactWorkingMemoryWrapper) RelateItems(ctx context.Context, id1 string, id2 string) error {
	w.itemsMu.Lock()
	defer w.itemsMu.Unlock()

	// Check that both items exist
	item1, ok1 := w.indexByID[id1]
	item2, ok2 := w.indexByID[id2]
	if !ok1 || !ok2 {
		return fmt.Errorf("one or both items do not exist")
	}

	// Add relation from item1 to item2 if not already present
	hasRelation1to2 := false
	for _, relID := range item1.RelatedItems {
		if relID == id2 {
			hasRelation1to2 = true
			break
		}
	}
	if !hasRelation1to2 {
		item1.RelatedItems = append(item1.RelatedItems, id2)
	}

	// Add relation from item2 to item1 if not already present
	hasRelation2to1 := false
	for _, relID := range item2.RelatedItems {
		if relID == id1 {
			hasRelation2to1 = true
			break
		}
	}
	if !hasRelation2to1 {
		item2.RelatedItems = append(item2.RelatedItems, id1)
	}

	return nil
}

// GetContext generates a context summary suitable for including in prompts.
func (w *ReactWorkingMemoryWrapper) GetContext(ctx context.Context) string {
	w.itemsMu.RLock()
	defer w.itemsMu.RUnlock()

	if len(w.items) == 0 {
		return "No context available."
	}

	var b strings.Builder
	b.WriteString("Working Memory Context:\n\n")

	// Group items by type
	itemsByType := make(map[string][]*WorkingMemoryItem)
	for _, item := range w.items {
		itemsByType[item.Type] = append(itemsByType[item.Type], item)
	}

	// Order types for consistent output
	types := make([]string, 0, len(itemsByType))
	for t := range itemsByType {
		types = append(types, t)
	}
	sort.Strings(types)

	// Output items by type
	for _, t := range types {
		items := itemsByType[t]
		b.WriteString(fmt.Sprintf("## %s\n", strings.Title(t)))

		// Sort items by name for consistent output
		sort.Slice(items, func(i, j int) bool {
			return items[i].Name < items[j].Name
		})

		for _, item := range items {
			b.WriteString(fmt.Sprintf("- %s: ", item.Name))

			// Format the content based on its type
			switch content := item.Content.(type) {
			case string:
				b.WriteString(content)
			case []interface{}:
				b.WriteString("[")
				for i, val := range content {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%v", val))
				}
				b.WriteString("]")
			case map[string]interface{}:
				b.WriteString("{")
				keys := make([]string, 0, len(content))
				for k := range content {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for i, k := range keys {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString(fmt.Sprintf("%s: %v", k, content[k]))
				}
				b.WriteString("}")
			default:
				b.WriteString(fmt.Sprintf("%v", content))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// Clear empties the memory.
func (w *ReactWorkingMemoryWrapper) Clear(ctx context.Context) error {
	// Clear the wrapped ReactMemory
	if err := w.ReactMemory.Clear(ctx); err != nil {
		return err
	}

	// Clear working memory items
	w.itemsMu.Lock()
	w.items = make(map[string]*WorkingMemoryItem)
	w.indexByID = make(map[string]*WorkingMemoryItem)
	w.itemsMu.Unlock()

	return nil
}
