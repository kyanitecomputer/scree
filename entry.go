package scree

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

const journalTypeEntry = uint16(1)

const (
	journalTypeEntryRemove        = uint16(6)
	journalTypeNamespacePurge     = uint16(7)
	journalTypeNamespacePurgeUpTo = uint16(8)
	journalTypeSetLastSequence    = uint16(9)
)

// entryHeaderSize is the size of an entry record before its subject: the
// namespace, sequence and timestamp, and the lengths of the subject, header
// and payload.
const entryHeaderSize = 8 + 8 + 8 + 2 + 4 + 4

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
	if entry == nil {
		return 0, ErrInvalidArgument
	}
	if err := s.checkNamespace(ns); err != nil {
		return 0, err
	}
	if s.logs[ns].lastSeq() == math.MaxUint64 {
		return 0, fmt.Errorf("%w: sequences of namespace %d exhausted", ErrNoSpace, ns)
	}
	seq := s.logs[ns].lastSeq() + 1
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
	entry, ok := s.logs[ns].get(seq)
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
	for entry := range s.logs[ns].ascending() {
		if entry.Subject != subject {
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
	if first := s.logs[ns].firstSeq(); first != 0 {
		return first, nil
	}
	return 0, ErrNotFound
}

// LastSequence returns the highest assigned sequence in a namespace.
func (s *Store) LastSequence(ns NamespaceID) (uint64, error) {
	last := s.logs[ns].lastSeq()
	if last == 0 {
		return 0, ErrNotFound
	}
	return last, nil
}

// RemoveEntry removes a single entry from a namespace.
func (s *Store) RemoveEntry(ns NamespaceID, seq uint64) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if _, ok := s.logs[ns].get(seq); !ok {
		return ErrNotFound
	}
	data := make([]byte, 16)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	binary.LittleEndian.PutUint64(data[8:], seq)
	if err := s.journal.append(journalEntry{Type: journalTypeEntryRemove, Data: data}); err != nil {
		return err
	}
	s.logs[ns].remove(seq)
	return nil
}

// Purge removes all entries from a namespace and returns how many it
// removed. The next entry gets the sequence after the last one assigned.
func (s *Store) Purge(ns NamespaceID) (uint64, error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	if err := s.checkNamespace(ns); err != nil {
		return 0, err
	}
	if s.logs[ns].len() == 0 {
		return 0, nil
	}
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	if err := s.journal.append(journalEntry{Type: journalTypeNamespacePurge, Data: data}); err != nil {
		return 0, err
	}
	return s.logs[ns].removeUpTo(math.MaxUint64), nil
}

// PurgeUpTo removes the entries with sequence numbers less than or equal to
// seq, in one journal record, and returns how many it removed.
func (s *Store) PurgeUpTo(ns NamespaceID, seq uint64) (uint64, error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	if err := s.checkNamespace(ns); err != nil {
		return 0, err
	}
	if first := s.logs[ns].firstSeq(); first == 0 || first > seq {
		return 0, nil
	}
	data := make([]byte, 16)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	binary.LittleEndian.PutUint64(data[8:], seq)
	if err := s.journal.append(journalEntry{Type: journalTypeNamespacePurgeUpTo, Data: data}); err != nil {
		return 0, err
	}
	return s.logs[ns].removeUpTo(seq), nil
}

// SetLastSequence makes seq the last sequence of a namespace, so that the
// next entry gets seq+1. Entries after seq are removed, and their sequences
// are assigned again; it returns how many it removed.
func (s *Store) SetLastSequence(ns NamespaceID, seq uint64) (uint64, error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	if err := s.checkNamespace(ns); err != nil {
		return 0, err
	}
	if s.logs[ns].lastSeq() == seq {
		return 0, nil
	}
	data := make([]byte, 16)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	binary.LittleEndian.PutUint64(data[8:], seq)
	if err := s.journal.append(journalEntry{Type: journalTypeSetLastSequence, Data: data}); err != nil {
		return 0, err
	}
	return s.logFor(ns).setLast(seq), nil
}

// Sync commits pending writes to the underlying block device.
func (s *Store) Sync() error {
	return s.dev.Sync()
}

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
		s.logs[ns].remove(seq)
	case journalTypeNamespacePurge:
		if len(entry.Data) < 8 {
			return ErrCorrupt
		}
		s.logs[NamespaceID(binary.LittleEndian.Uint64(entry.Data))].removeUpTo(math.MaxUint64)
	case journalTypeSetLastSequence:
		if len(entry.Data) < 16 {
			return ErrCorrupt
		}
		s.logFor(NamespaceID(binary.LittleEndian.Uint64(entry.Data))).setLast(binary.LittleEndian.Uint64(entry.Data[8:]))
	case journalTypeNamespacePurgeUpTo:
		if len(entry.Data) < 16 {
			return ErrCorrupt
		}
		s.logs[NamespaceID(binary.LittleEndian.Uint64(entry.Data))].removeUpTo(binary.LittleEndian.Uint64(entry.Data[8:]))
	default:
		return fmt.Errorf("%w: journal record type %d", ErrUnsupportedFormat, entry.Type)
	}
	return nil
}

func (s *Store) applyEntry(record entryRecord) {
	s.logFor(record.Namespace).add(record.StoredEntry)
}

// logFor returns the entry log of ns, which it creates if needed.
func (s *Store) logFor(ns NamespaceID) *entryLog {
	log := s.logs[ns]
	if log == nil {
		log = newEntryLog()
		s.logs[ns] = log
	}
	return log
}

func encodeEntryRecord(record entryRecord) ([]byte, error) {
	if len(record.Subject) > math.MaxUint16 || uint64(len(record.Header)) > math.MaxUint32 || uint64(len(record.Payload)) > math.MaxUint32 {
		return nil, ErrInvalidArgument
	}
	buf := bytes.NewBuffer(make([]byte, 0, entryHeaderSize+len(record.Subject)+len(record.Header)+len(record.Payload)))
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
	if len(data) < entryHeaderSize {
		return entryRecord{}, ErrCorrupt
	}
	record := entryRecord{Namespace: NamespaceID(binary.LittleEndian.Uint64(data[0:]))}
	record.Sequence = binary.LittleEndian.Uint64(data[8:])
	record.Timestamp = int64(binary.LittleEndian.Uint64(data[16:]))
	subjectLen := uint64(binary.LittleEndian.Uint16(data[24:]))
	headerLen := uint64(binary.LittleEndian.Uint32(data[26:]))
	payloadLen := uint64(binary.LittleEndian.Uint32(data[30:]))
	if subjectLen+headerLen+payloadLen > uint64(len(data)-entryHeaderSize) {
		return entryRecord{}, ErrCorrupt
	}
	pos := entryHeaderSize
	end := pos + int(subjectLen+headerLen+payloadLen)
	record.Subject = string(data[pos : pos+int(subjectLen)])
	pos += int(subjectLen)
	record.Header = cloneBytes(data[pos : pos+int(headerLen)])
	pos += int(headerLen)
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
