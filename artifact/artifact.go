// Package artifact provides the definition and service for content artifacts.
package artifact

// Artifact represents a content artifact such as an image, video, or document.
type Artifact struct {
	// Data contains the raw bytes (required).
	Data []byte `json:"data,omitempty"`
	// MimeType is the IANA standard MIME type of the source data (required).
	MimeType string `json:"mime_type,omitempty"`
	// Name is an optional display name of the artifact.
	// Used to provide a label or filename to distinguish artifacts.
	// This field is not currently used in the GenerateContent calls.
	Name string `json:"name,omitempty"`
}
