package scree

// VerifyOptions configures store verification.
type VerifyOptions struct {
	// CheckData verifies data records reachable from the journal.
	CheckData bool
}

// Verify checks persistent store metadata and journal records.
func (s *Store) Verify(opts VerifyOptions) error {
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
	journal, err := openJournal(s.dev, sb)
	if err != nil {
		return err
	}
	if opts.CheckData {
		entries, err := journal.replay()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Type == journalTypeEntry {
				if _, err := decodeEntryRecord(entry.Data); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
