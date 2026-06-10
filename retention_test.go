package scree

import "testing"

func TestSubjectLookupAndRetention(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if _, err := store.Append(1, &Entry{Subject: "a", Payload: []byte("one")}); err != nil {
		t.Fatalf("Append(one) error = %v", err)
	}
	if _, err := store.Append(1, &Entry{Subject: "b", Payload: []byte("two")}); err != nil {
		t.Fatalf("Append(two) error = %v", err)
	}
	if _, err := store.Append(1, &Entry{Subject: "a", Payload: []byte("three")}); err != nil {
		t.Fatalf("Append(three) error = %v", err)
	}
	entry, err := store.LoadEntryBySubject(1, "a", 2)
	if err != nil || string(entry.Payload) != "three" {
		t.Fatalf("LoadEntryBySubject() = %+v, %v, want payload three", entry, err)
	}
	first, err := store.FirstSequence(1)
	if err != nil || first != 1 {
		t.Fatalf("FirstSequence() = %d, %v, want 1, nil", first, err)
	}
	last, err := store.LastSequence(1)
	if err != nil || last != 3 {
		t.Fatalf("LastSequence() = %d, %v, want 3, nil", last, err)
	}
	if err := store.RemoveEntry(1, 1); err != nil {
		t.Fatalf("RemoveEntry() error = %v", err)
	}
	first, err = store.FirstSequence(1)
	if err != nil || first != 2 {
		t.Fatalf("FirstSequence() after remove = %d, %v, want 2, nil", first, err)
	}
	if err := store.PurgeUpTo(1, 2); err != nil {
		t.Fatalf("PurgeUpTo() error = %v", err)
	}
	first, err = store.FirstSequence(1)
	if err != nil || first != 3 {
		t.Fatalf("FirstSequence() after purge = %d, %v, want 3, nil", first, err)
	}
}

func TestRetentionSurvivesRemount(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if _, err := store.Append(1, &Entry{Subject: "a", Payload: []byte("one")}); err != nil {
		t.Fatalf("Append(one) error = %v", err)
	}
	if _, err := store.Append(1, &Entry{Subject: "a", Payload: []byte("two")}); err != nil {
		t.Fatalf("Append(two) error = %v", err)
	}
	if err := store.RemoveEntry(1, 1); err != nil {
		t.Fatalf("RemoveEntry() error = %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if _, err := reopened.LoadEntry(1, 1); err != ErrNotFound {
		t.Fatalf("LoadEntry(1) error = %v, want %v", err, ErrNotFound)
	}
	entry, err := reopened.LoadEntry(1, 2)
	if err != nil || string(entry.Payload) != "two" {
		t.Fatalf("LoadEntry(2) = %+v, %v, want two", entry, err)
	}
}
