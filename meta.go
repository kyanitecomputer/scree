package scree

import (
	"encoding/binary"
	"math"
	"sort"
)

const (
	journalTypeMetaPut    = uint16(4)
	journalTypeMetaDelete = uint16(5)
)

// PutMeta stores metadata for a namespace.
func (s *Store) PutMeta(ns NamespaceID, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return ErrReadOnly
	}
	if key == "" {
		return ErrInvalidArgument
	}
	if err := s.checkNamespace(ns); err != nil {
		return err
	}
	record, err := encodeMetaRecord(ns, key, data)
	if err != nil {
		return err
	}
	if err := s.journal.append(journalEntry{Type: journalTypeMetaPut, Data: record}); err != nil {
		return err
	}
	s.applyMetaPut(ns, key, data)
	return nil
}

// GetMeta returns metadata for a namespace.
func (s *Store) GetMeta(ns NamespaceID, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return ErrReadOnly
	}
	if key == "" {
		return ErrInvalidArgument
	}
	if err := s.checkNamespace(ns); err != nil {
		return err
	}
	record, err := encodeMetaRecord(ns, key, nil)
	if err != nil {
		return err
	}
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

func encodeMetaRecord(ns NamespaceID, key string, data []byte) ([]byte, error) {
	if len(key) > math.MaxUint16 || uint64(len(data)) > math.MaxUint32 {
		return nil, ErrInvalidArgument
	}
	record := make([]byte, 14+len(key)+len(data))
	binary.LittleEndian.PutUint64(record, uint64(ns))
	binary.LittleEndian.PutUint16(record[8:], uint16(len(key)))
	binary.LittleEndian.PutUint32(record[10:], uint32(len(data)))
	copy(record[14:], key)
	copy(record[14+len(key):], data)
	return record, nil
}

func decodeMetaRecord(record []byte) (NamespaceID, string, []byte, error) {
	if len(record) < 14 {
		return 0, "", nil, ErrCorrupt
	}
	keyLen := int(binary.LittleEndian.Uint16(record[8:]))
	dataLen := uint64(binary.LittleEndian.Uint32(record[10:]))
	if uint64(keyLen)+dataLen > uint64(len(record)-14) {
		return 0, "", nil, ErrCorrupt
	}
	end := 14 + keyLen + int(dataLen)
	key := string(record[14 : 14+keyLen])
	data := cloneBytes(record[14+keyLen : end])
	return NamespaceID(binary.LittleEndian.Uint64(record)), key, data, nil
}
