package scree

import "fmt"

// VerifyOptions configures store verification.
type VerifyOptions struct {
	// CheckData decodes every journal record and applies it to a scratch
	// copy of the store.
	CheckData bool
}

// Verify checks persistent store metadata and journal records. It reports
// ErrCorrupt for journal units that hold no valid record, which includes a
// write that power loss cut short, and for programmed units after the end of
// the journal.
func (s *Store) Verify(opts VerifyOptions) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	sb, err := mountSuperblock(s.dev)
	if err != nil {
		return err
	}
	master, err := mountMaster(s.dev)
	if err != nil {
		return err
	}
	if err := validateMaster(sb, master); err != nil {
		return err
	}
	scratch := newStore(s.dev, sb, master)
	entries, bad, err := scratch.journal.replay()
	if err != nil {
		return err
	}
	if bad > 0 {
		return fmt.Errorf("%w: %d journal units hold no valid record", ErrCorrupt, bad)
	}
	if err := scratch.journal.checkErased(); err != nil {
		return err
	}
	if opts.CheckData {
		for _, entry := range entries {
			if err := scratch.applyRecord(entry); err != nil {
				return err
			}
		}
	}
	return nil
}
