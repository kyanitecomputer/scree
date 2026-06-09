package scree

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

const journalTypeEntry = uint16(1)

const (
	journalTypeEntryRemove    = uint16(6)
	journalTypeNamespacePurge = uint16(7)
)

// NamespaceID identifies a Scree namespace.
type NamespaceID uint64

// Entry represents data to append to a namespace.
type Entry struct {
	Subject   string
	Header    []byte
	Payload   []byte
	Timestamp int64
}

// StoredEntry is an entry loaded from storage.
type StoredEntry struct {
	Sequence  uint64
	Subject   string
	Header    []byte
	Payload   []byte
	Timestamp int64
}

type entryRecord struct {
	Namespace NamespaceID
	StoredEntry
}

// Append writes an entry to a namespace and returns its sequence number.
func (s *Store) Append(ns NamespaceID, entry *Entry) (uint64, error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	if ns == 0 || entry == nil {
		return 0, ErrNotFound
	}
	seq := s.lastSeq[ns] + 1
	ts := entry.Timestamp
	if ts == 0 {
		ts = time.Now().UnixNano()
	}
	record := entryRecord{
		Namespace: ns,
		StoredEntry: StoredEntry{
			Sequence:  seq,
			Subject:   entry.Subject,
			Header:    cloneBytes(entry.Header),
			Payload:   cloneBytes(entry.Payload),
			Timestamp: ts,
		},
	}
	data, err := encodeEntryRecord(record)
	if err != nil {
		return 0, err
	}
	if err := s.journal.append(journalEntry{Type: journalTypeEntry, Data: data}); err != nil {
		return 0, err
	}
	s.applyEntry(record)
	return seq, nil
}

// LoadEntry reads an entry by namespace and sequence number.
func (s *Store) LoadEntry(ns NamespaceID, seq uint64) (*StoredEntry, error) {
	entries := s.entries[ns]
	if entries == nil {
		return nil, ErrNotFound
	}
	entry, ok := entries[seq]
	if !ok {
		return nil, ErrNotFound
	}
	copy := entry
	copy.Header = cloneBytes(entry.Header)
	copy.Payload = cloneBytes(entry.Payload)
	return &copy, nil
}

// LoadEntryBySubject reads the subjectSeq-th entry for a subject.
func (s *Store) LoadEntryBySubject(ns NamespaceID, subject string, subjectSeq uint64) (*StoredEntry, error) {
	if subjectSeq == 0 {
		return nil, ErrNotFound
	}
	var seen uint64
	for seq := uint64(1); seq <= s.lastSeq[ns]; seq++ {
		entry, ok := s.entries[ns][seq]
		if !ok || entry.Subject != subject {
			continue
		}
		seen++
		if seen == subjectSeq {
			copy := entry
			copy.Header = cloneBytes(entry.Header)
			copy.Payload = cloneBytes(entry.Payload)
			return &copy, nil
		}
	}
	return nil, ErrNotFound
}

// FirstSequence returns the lowest live sequence in a namespace.
func (s *Store) FirstSequence(ns NamespaceID) (uint64, error) {
	for seq := uint64(1); seq <= s.lastSeq[ns]; seq++ {
		if _, ok := s.entries[ns][seq]; ok {
			return seq, nil
		}
	}
	return 0, ErrNotFound
}

// LastSequence returns the highest assigned sequence in a namespace.
func (s *Store) LastSequence(ns NamespaceID) (uint64, error) {
	if s.lastSeq[ns] == 0 {
		return 0, ErrNotFound
	}
	return s.lastSeq[ns], nil
}

// RemoveEntry removes a single entry from a namespace.
func (s *Store) RemoveEntry(ns NamespaceID, seq uint64) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if _, ok := s.entries[ns][seq]; !ok {
		return ErrNotFound
	}
	data := make([]byte, 16)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	binary.LittleEndian.PutUint64(data[8:], seq)
	if err := s.journal.append(journalEntry{Type: journalTypeEntryRemove, Data: data}); err != nil {
		return err
	}
	delete(s.entries[ns], seq)
	return nil
}

// Purge removes all entries from a namespace.
func (s *Store) Purge(ns NamespaceID) error {
	if s.readOnly {
		return ErrReadOnly
	}
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	if err := s.journal.append(journalEntry{Type: journalTypeNamespacePurge, Data: data}); err != nil {
		return err
	}
	s.entries[ns] = make(map[uint64]StoredEntry)
	s.lastSeq[ns] = 0
	return nil
}

// PurgeUpTo removes entries with sequence numbers less than or equal to seq.
func (s *Store) PurgeUpTo(ns NamespaceID, seq uint64) error {
	if s.readOnly {
		return ErrReadOnly
	}
	for cur := uint64(1); cur <= seq; cur++ {
		if _, ok := s.entries[ns][cur]; ok {
			if err := s.RemoveEntry(ns, cur); err != nil {
				return err
			}
		}
	}
	return nil
}

// Sync commits pending writes to the underlying block device.
func (s *Store) Sync() error {
	return s.dev.Sync()
}

func (s *Store) replayEntries() error {
	entries, err := s.journal.replay()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Type {
		case journalTypeEntry:
			record, err := decodeEntryRecord(entry.Data)
			if err != nil {
				return err
			}
			s.applyEntry(record)
		case journalTypeNamespaceCreate:
			id, name, err := decodeNamespaceRecord(entry.Data)
			if err != nil {
				return err
			}
			s.applyNamespaceCreate(id, name)
		case journalTypeNamespaceDelete:
			if len(entry.Data) < 8 {
				return ErrCorrupt
			}
			s.applyNamespaceDelete(NamespaceID(binary.LittleEndian.Uint64(entry.Data)))
		case journalTypeMetaPut:
			ns, key, data, err := decodeMetaRecord(entry.Data)
			if err != nil {
				return err
			}
			s.applyMetaPut(ns, key, data)
		case journalTypeMetaDelete:
			ns, key, _, err := decodeMetaRecord(entry.Data)
			if err != nil {
				return err
			}
			if s.meta[ns] != nil {
				delete(s.meta[ns], key)
			}
		case journalTypeEntryRemove:
			if len(entry.Data) < 16 {
				return ErrCorrupt
			}
			ns := NamespaceID(binary.LittleEndian.Uint64(entry.Data))
			seq := binary.LittleEndian.Uint64(entry.Data[8:])
			if s.entries[ns] != nil {
				delete(s.entries[ns], seq)
			}
		case journalTypeNamespacePurge:
			if len(entry.Data) < 8 {
				return ErrCorrupt
			}
			ns := NamespaceID(binary.LittleEndian.Uint64(entry.Data))
			s.entries[ns] = make(map[uint64]StoredEntry)
			s.lastSeq[ns] = 0
		default:
			continue
		}
	}
	return nil
}

func (s *Store) applyEntry(record entryRecord) {
	if s.entries[record.Namespace] == nil {
		s.entries[record.Namespace] = make(map[uint64]StoredEntry)
	}
	s.entries[record.Namespace][record.Sequence] = record.StoredEntry
	if record.Sequence > s.lastSeq[record.Namespace] {
		s.lastSeq[record.Namespace] = record.Sequence
	}
}

func encodeEntryRecord(record entryRecord) ([]byte, error) {
	if len(record.Subject) > 1<<16-1 {
		return nil, ErrUnsupportedFormat
	}
	buf := bytes.NewBuffer(make([]byte, 0, 32+len(record.Subject)+len(record.Header)+len(record.Payload)))
	write := func(v any) error { return binary.Write(buf, binary.LittleEndian, v) }
	if err := write(uint64(record.Namespace)); err != nil {
		return nil, fmt.Errorf("encode namespace: %w", err)
	}
	if err := write(record.Sequence); err != nil {
		return nil, fmt.Errorf("encode sequence: %w", err)
	}
	if err := write(record.Timestamp); err != nil {
		return nil, fmt.Errorf("encode timestamp: %w", err)
	}
	if err := write(uint16(len(record.Subject))); err != nil {
		return nil, fmt.Errorf("encode subject length: %w", err)
	}
	if err := write(uint32(len(record.Header))); err != nil {
		return nil, fmt.Errorf("encode header length: %w", err)
	}
	if err := write(uint32(len(record.Payload))); err != nil {
		return nil, fmt.Errorf("encode payload length: %w", err)
	}
	buf.WriteString(record.Subject)
	buf.Write(record.Header)
	buf.Write(record.Payload)
	return buf.Bytes(), nil
}

func decodeEntryRecord(data []byte) (entryRecord, error) {
	if len(data) < 30 {
		return entryRecord{}, ErrCorrupt
	}
	record := entryRecord{Namespace: NamespaceID(binary.LittleEndian.Uint64(data[0:]))}
	record.Sequence = binary.LittleEndian.Uint64(data[8:])
	record.Timestamp = int64(binary.LittleEndian.Uint64(data[16:]))
	subjectLen := int(binary.LittleEndian.Uint16(data[24:]))
	headerLen := int(binary.LittleEndian.Uint32(data[26:]))
	payloadLen := int(binary.LittleEndian.Uint32(data[30:]))
	pos := 34
	end := pos + subjectLen + headerLen + payloadLen
	if end > len(data) {
		return entryRecord{}, ErrCorrupt
	}
	record.Subject = string(data[pos : pos+subjectLen])
	pos += subjectLen
	record.Header = cloneBytes(data[pos : pos+headerLen])
	pos += headerLen
	record.Payload = cloneBytes(data[pos:end])
	return record, nil
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
