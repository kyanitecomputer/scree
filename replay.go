package scree

import (
	"encoding/binary"
	"fmt"
	"math"
)

// replayEntries applies the journal to the in-memory state.
func (s *Store) replayEntries() error {
	entries, _, err := s.journal.replay()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.applyRecord(entry); err != nil {
			return err
		}
	}
	return nil
}

// applyRecord applies a journal record to the in-memory state.
func (s *Store) applyRecord(entry journalEntry) error {
	switch entry.Type {
	case recordEntry:
		record, err := decodeEntryRecord(entry.Data)
		if err != nil {
			return err
		}
		s.applyEntry(record)
	case recordNamespaceCreate:
		id, name, err := decodeNamespaceRecord(entry.Data)
		if err != nil {
			return err
		}
		s.applyNamespaceCreate(id, name)
	case recordNamespaceDelete:
		if len(entry.Data) < 8 {
			return ErrCorrupt
		}
		s.applyNamespaceDelete(NamespaceID(binary.LittleEndian.Uint64(entry.Data)))
	case recordMetaPut:
		ns, key, data, err := decodeMetaRecord(entry.Data)
		if err != nil {
			return err
		}
		s.applyMetaPut(ns, key, data)
	case recordMetaDelete:
		ns, key, _, err := decodeMetaRecord(entry.Data)
		if err != nil {
			return err
		}
		if s.meta[ns] != nil {
			delete(s.meta[ns], key)
		}
	case recordEntryRemove:
		if len(entry.Data) < 16 {
			return ErrCorrupt
		}
		ns := NamespaceID(binary.LittleEndian.Uint64(entry.Data))
		seq := binary.LittleEndian.Uint64(entry.Data[8:])
		s.logs[ns].remove(seq)
	case recordNamespacePurge:
		if len(entry.Data) < 8 {
			return ErrCorrupt
		}
		s.logs[NamespaceID(binary.LittleEndian.Uint64(entry.Data))].removeUpTo(math.MaxUint64)
	case recordNamespacePurgeUpTo:
		if len(entry.Data) < 16 {
			return ErrCorrupt
		}
		s.logs[NamespaceID(binary.LittleEndian.Uint64(entry.Data))].removeUpTo(binary.LittleEndian.Uint64(entry.Data[8:]))
	case recordSetLastSequence:
		if len(entry.Data) < 16 {
			return ErrCorrupt
		}
		s.logFor(NamespaceID(binary.LittleEndian.Uint64(entry.Data))).setLast(binary.LittleEndian.Uint64(entry.Data[8:]))
	default:
		return fmt.Errorf("%w: journal record type %d", ErrUnsupportedFormat, entry.Type)
	}
	return nil
}
