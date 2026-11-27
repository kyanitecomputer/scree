package scree

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// entryHeaderSize is the size of an entry record before its subject: the
// namespace, sequence and timestamp, and the lengths of the subject, header
// and payload.
const entryHeaderSize = 8 + 8 + 8 + 2 + 4 + 4

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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
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
			Header:    bytes.Clone(entry.Header),
			Payload:   bytes.Clone(entry.Payload),
			Timestamp: ts,
		},
	}
	data, err := encodeEntryRecord(record)
	if err != nil {
		return 0, err
	}
	if err := s.journal.append(journalEntry{Type: recordEntry, Data: data}); err != nil {
		return 0, err
	}
	s.applyEntry(record)
	return seq, nil
}

// LoadEntry reads an entry by namespace and sequence number.
func (s *Store) LoadEntry(ns NamespaceID, seq uint64) (*StoredEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	entry, ok := s.logs[ns].get(seq)
	if !ok {
		return nil, ErrNotFound
	}
	return entry.clone(), nil
}

// LoadEntryBySubject reads the subjectSeq-th entry for a subject.
func (s *Store) LoadEntryBySubject(ns NamespaceID, subject string, subjectSeq uint64) (*StoredEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
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
			return entry.clone(), nil
		}
	}
	return nil, ErrNotFound
}

// FirstSequence returns the lowest live sequence in a namespace.
func (s *Store) FirstSequence(ns NamespaceID) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, ErrClosed
	}
	if first := s.logs[ns].firstSeq(); first != 0 {
		return first, nil
	}
	return 0, ErrNotFound
}

// LastSequence returns the highest assigned sequence in a namespace.
func (s *Store) LastSequence(ns NamespaceID) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return 0, ErrClosed
	}
	last := s.logs[ns].lastSeq()
	if last == 0 {
		return 0, ErrNotFound
	}
	return last, nil
}

// RemoveEntry removes a single entry from a namespace.
func (s *Store) RemoveEntry(ns NamespaceID, seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.readOnly {
		return ErrReadOnly
	}
	if _, ok := s.logs[ns].get(seq); !ok {
		return ErrNotFound
	}
	data := make([]byte, 16)
	binary.LittleEndian.PutUint64(data, uint64(ns))
	binary.LittleEndian.PutUint64(data[8:], seq)
	if err := s.journal.append(journalEntry{Type: recordEntryRemove, Data: data}); err != nil {
		return err
	}
	s.logs[ns].remove(seq)
	return nil
}

// Purge removes all entries from a namespace and returns how many it
// removed. The next entry gets the sequence after the last one assigned.
func (s *Store) Purge(ns NamespaceID) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
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
	if err := s.journal.append(journalEntry{Type: recordNamespacePurge, Data: data}); err != nil {
		return 0, err
	}
	return s.logs[ns].removeUpTo(math.MaxUint64), nil
}

// PurgeUpTo removes the entries with sequence numbers less than or equal to
// seq, in one journal record, and returns how many it removed.
func (s *Store) PurgeUpTo(ns NamespaceID, seq uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
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
	if err := s.journal.append(journalEntry{Type: recordNamespacePurgeUpTo, Data: data}); err != nil {
		return 0, err
	}
	return s.logs[ns].removeUpTo(seq), nil
}

// SetLastSequence makes seq the last sequence of a namespace, so that the
// next entry gets seq+1. Entries after seq are removed, and their sequences
// are assigned again; it returns how many it removed.
func (s *Store) SetLastSequence(ns NamespaceID, seq uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
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
	if err := s.journal.append(journalEntry{Type: recordSetLastSequence, Data: data}); err != nil {
		return 0, err
	}
	return s.logFor(ns).setLast(seq), nil
}

// Sync commits pending writes to the underlying block device.
func (s *Store) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.dev.Sync()
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
	data := make([]byte, entryHeaderSize+len(record.Subject)+len(record.Header)+len(record.Payload))
	binary.LittleEndian.PutUint64(data[0:], uint64(record.Namespace))
	binary.LittleEndian.PutUint64(data[8:], record.Sequence)
	binary.LittleEndian.PutUint64(data[16:], uint64(record.Timestamp))
	binary.LittleEndian.PutUint16(data[24:], uint16(len(record.Subject)))
	binary.LittleEndian.PutUint32(data[26:], uint32(len(record.Header)))
	binary.LittleEndian.PutUint32(data[30:], uint32(len(record.Payload)))
	pos := entryHeaderSize
	pos += copy(data[pos:], record.Subject)
	pos += copy(data[pos:], record.Header)
	copy(data[pos:], record.Payload)
	return data, nil
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
	record.Header = bytes.Clone(data[pos : pos+int(headerLen)])
	pos += int(headerLen)
	record.Payload = bytes.Clone(data[pos:end])
	return record, nil
}

// clone returns a copy of e that shares no memory with it.
func (e StoredEntry) clone() *StoredEntry {
	e.Header = bytes.Clone(e.Header)
	e.Payload = bytes.Clone(e.Payload)
	return &e
}
