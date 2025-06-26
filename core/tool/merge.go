package tool

// Mergeable interface for custom types that want to define their own merging logic
type Mergeable interface {
	Merge(other any) any
}
