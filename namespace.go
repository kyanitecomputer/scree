package scree

import (
	"encoding/binary"
	"sort"
)

const (
	journalTypeNamespaceCreate = uint16(2)
	journalTypeNamespaceDelete = uint16(3)
)

// NamespaceEntry describes a namespace.
type NamespaceEntry struct {
	ID   NamespaceID
	Name string
}

// CreateNamespace creates a named namespace.
func (s *Store) CreateNamespace(name string) (NamespaceID, error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	if name == "" {
		return 0, ErrNotFound
	}
	if id, ok := s.nsByName[name]; ok {
		return id, nil
	}
	id := s.nextNS
	s.nextNS++
	data := encodeNamespaceRecord(id, name)
	if err := s.journal.append(journalEntry{Type: journalTypeNamespaceCreate, Data: data}); err != nil {
		return 0, err
	}
	s.applyNamespaceCreate(id, name)
	return id, nil
}

// LookupNamespace returns the namespace ID for name.
func (s *Store) LookupNamespace(name string) (NamespaceID, bool, error) {
	id, ok := s.nsByName[name]
	return id, ok, nil
}

// ListNamespaces returns all namespaces ordered by ID.
func (s *Store) ListNamespaces() ([]NamespaceEntry, error) {
	entries := make([]NamespaceEntry, 0, len(s.namespaces))
	for id, name := range s.namespaces {
		entries = append(entries, NamespaceEntry{ID: id, Name: name})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// DeleteNamespace deletes a namespace and its in-memory entries.
func (s *Store) DeleteNamespace(id NamespaceID) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if _, ok := s.namespaces[id]; !ok {
		return ErrNotFound
	}
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, uint64(id))
	if err := s.journal.append(journalEntry{Type: journalTypeNamespaceDelete, Data: data}); err != nil {
		return err
	}
	s.applyNamespaceDelete(id)
	return nil
}

func (s *Store) applyNamespaceCreate(id NamespaceID, name string) {
	s.namespaces[id] = name
	s.nsByName[name] = id
	if id >= s.nextNS {
		s.nextNS = id + 1
	}
}

func (s *Store) applyNamespaceDelete(id NamespaceID) {
	name := s.namespaces[id]
	delete(s.namespaces, id)
	delete(s.nsByName, name)
	delete(s.entries, id)
	delete(s.lastSeq, id)
	delete(s.meta, id)
}

func encodeNamespaceRecord(id NamespaceID, name string) []byte {
	data := make([]byte, 10+len(name))
	binary.LittleEndian.PutUint64(data, uint64(id))
	binary.LittleEndian.PutUint16(data[8:], uint16(len(name)))
	copy(data[10:], name)
	return data
}

func decodeNamespaceRecord(data []byte) (NamespaceID, string, error) {
	if len(data) < 10 {
		return 0, "", ErrCorrupt
	}
	nameLen := int(binary.LittleEndian.Uint16(data[8:]))
	if 10+nameLen > len(data) {
		return 0, "", ErrCorrupt
	}
	return NamespaceID(binary.LittleEndian.Uint64(data)), string(data[10 : 10+nameLen]), nil
}
