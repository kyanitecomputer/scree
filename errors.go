package scree

import "errors"

var (
	// ErrCorrupt reports malformed or inconsistent storage contents.
	ErrCorrupt = errors.New("scree: corrupt store")

	// ErrAuthenticationFailed reports authenticated storage verification failure.
	ErrAuthenticationFailed = errors.New("scree: authentication failed")

	// ErrNoSpace reports that no writable storage space is available.
	ErrNoSpace = errors.New("scree: no space left")

	// ErrReadOnly reports that a write operation was attempted on a read-only store.
	ErrReadOnly = errors.New("scree: read-only store")

	// ErrUnsupportedFormat reports an unsupported on-flash format or feature flag.
	ErrUnsupportedFormat = errors.New("scree: unsupported format")

	// ErrNotFound reports that a requested namespace or entry does not exist.
	ErrNotFound = errors.New("scree: not found")
)
