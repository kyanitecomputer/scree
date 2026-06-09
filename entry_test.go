package scree

import "testing"

func TestAppendSyncRemountLoad(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	seq, err := store.Append(1, &Entry{Subject: "foo", Header: []byte("h"), Payload: []byte("hello"), Timestamp: 42})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if seq != 1 {
		t.Fatalf("Append() seq = %d, want 1", seq)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	entry, err := reopened.LoadEntry(1, seq)
	if err != nil {
		t.Fatalf("LoadEntry() error = %v", err)
	}
	if entry.Subject != "foo" || string(entry.Header) != "h" || string(entry.Payload) != "hello" || entry.Timestamp != 42 {
		t.Fatalf("LoadEntry() = %+v, want foo/h/hello/42", entry)
	}
}

func TestAppendReadOnly(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if _, err := store.Append(1, &Entry{}); err != ErrReadOnly {
		t.Fatalf("Append() error = %v, want %v", err, ErrReadOnly)
	}
}
