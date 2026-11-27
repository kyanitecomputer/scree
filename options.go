package scree

// AuthFailureMode controls how authenticated volumes react to verification
// failures. Authenticated volumes are not implemented yet.
type AuthFailureMode uint8

const (
	// AuthStrict rejects data that fails authentication.
	AuthStrict AuthFailureMode = iota

	// AuthReportOnly records authentication failures without making policy decisions for callers.
	AuthReportOnly
)

// FormatOptions configures a newly formatted Scree volume.
type FormatOptions struct {
	// JournalBlocks reserves logical erase blocks for the write-ahead journal.
	JournalBlocks int

	// EnableAuth asks for authenticated metadata. It is not implemented
	// yet, and Format refuses it with ErrUnsupportedFormat.
	EnableAuth bool
}

// MountOptions configures runtime behavior for a mounted Scree volume.
type MountOptions struct {
	// AuthKey verifies authenticated volumes. Mount refuses those with
	// ErrUnsupportedFormat until they are implemented, and ignores the key.
	AuthKey []byte

	// AuthFailureMode controls whether authentication failures block access.
	AuthFailureMode AuthFailureMode

	// ReadOnly disables all writes when true.
	ReadOnly bool
}
