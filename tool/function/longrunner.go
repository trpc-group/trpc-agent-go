package function

// LongRunner defines an interface for determining if an operation or process
// is expected to run for an extended period of time.
type LongRunner interface {
	// LongRunning returns true if the operation is expected to run for a long time.
	LongRunning() bool
}
