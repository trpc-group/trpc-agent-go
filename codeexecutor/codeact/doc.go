// Package codeact runs generated Python through a capability-limited tool gateway.
//
// CodeAct does not make Python safe by itself. The guest process must run in an
// isolated container, microVM, or an equivalent sandbox. This package owns the
// host-side security boundary: only explicitly registered tools can be called,
// and their input and output schemas are validated on the Go side.
package codeact
