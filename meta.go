package scree

import (
	"encoding/binary"
	"sort"
)

const (
	journalTypeMetaPut    = uint16(4)
	journalTypeMetaDelete = uint16(5)
)

// PutMeta stores metadata for a namespace.
func (s *Store) PutMeta(ns NamespaceID, key string, data []byte) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if key == "" {
		return ErrNotFound
	}
	record := encodeMetaRecord(ns, key, data)
	if err := s.journal.append(journalEntry{Type: journalTypeMetaPut, Data: record}); err != nil {
		return err
	}
	s.applyMetaPut(ns, key, data)
	return nil
}

// GetMeta returns metadata for a namespace.
func (s *Store) GetMeta(ns NamespaceID, key string) ([]byte, error) {
	if s.meta[ns] == nil {
		return nil, ErrNotFound
	}
	data, ok := s.meta[ns][key]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneBytes(data), nil
}

// MetaKeys returns the metadata keys stored in a namespace, sorted. It returns
// an empty slice (not an error) for an unknown or empty namespace.
func (s *Store) MetaKeys(ns NamespaceID) ([]string, error) {
	m := s.meta[ns]
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// DeleteMeta removes metadata from a namespace.
func (s *Store) DeleteMeta(ns NamespaceID, key string) error {
	if s.readOnly {
		return ErrReadOnly
	}
	record := encodeMetaRecord(ns, key, nil)
	if err := s.journal.append(journalEntry{Type: journalTypeMetaDelete, Data: record}); err != nil {
		return err
	}
	if s.meta[ns] != nil {
		delete(s.meta[ns], key)
	}
	return nil
}

func (s *Store) applyMetaPut(ns NamespaceID, key string, data []byte) {
	if s.meta[ns] == nil {
		s.meta[ns] = make(map[string][]byte)
	}
	s.meta[ns][key] = cloneBytes(data)
}

func encodeMetaRecord(ns NamespaceID, key string, data []byte) []byte {
	record := make([]byte, 14+len(key)+len(data))
	binary.LittleEndian.PutUint64(record, uint64(ns))
	binary.LittleEndian.PutUint16(record[8:], uint16(len(key)))
	binary.LittleEndian.PutUint32(record[10:], uint32(len(data)))
	copy(record[14:], key)
	copy(record[14+len(key):], data)
	return record
}

func decodeMetaRecord(record []byte) (NamespaceID, string, []byte, error) {
	if len(record) < 14 {
		return 0, "", nil, ErrCorrupt
	}
	keyLen := int(binary.LittleEndian.Uint16(record[8:]))
	dataLen := int(binary.LittleEndian.Uint32(record[10:]))
	end := 14 + keyLen + dataLen
	if end > len(record) {
		return 0, "", nil, ErrCorrupt
	}
	key := string(record[14 : 14+keyLen])
	data := cloneBytes(record[14+keyLen : end])
	return NamespaceID(binary.LittleEndian.Uint64(record)), key, data, nil
}
