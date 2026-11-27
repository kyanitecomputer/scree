package scree

import (
	"errors"
	"math"
	"testing"
)

func TestSubjectLookupAndRetention(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("one")}); err != nil {
		t.Fatalf("Append(one) error = %v", err)
	}
	if _, err := store.Append(ns, &Entry{Subject: "b", Payload: []byte("two")}); err != nil {
		t.Fatalf("Append(two) error = %v", err)
	}
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("three")}); err != nil {
		t.Fatalf("Append(three) error = %v", err)
	}
	entry, err := store.LoadEntryBySubject(ns, "a", 2)
	if err != nil || string(entry.Payload) != "three" {
		t.Fatalf("LoadEntryBySubject() = %+v, %v, want payload three", entry, err)
	}
	first, err := store.FirstSequence(ns)
	if err != nil || first != 1 {
		t.Fatalf("FirstSequence() = %d, %v, want 1, nil", first, err)
	}
	last, err := store.LastSequence(ns)
	if err != nil || last != 3 {
		t.Fatalf("LastSequence() = %d, %v, want 3, nil", last, err)
	}
	if err := store.RemoveEntry(ns, 1); err != nil {
		t.Fatalf("RemoveEntry() error = %v", err)
	}
	first, err = store.FirstSequence(ns)
	if err != nil || first != 2 {
		t.Fatalf("FirstSequence() after remove = %d, %v, want 2, nil", first, err)
	}
	if n, err := store.PurgeUpTo(ns, 2); err != nil || n != 1 {
		t.Fatalf("PurgeUpTo() = %d, %v, want 1, nil", n, err)
	}
	first, err = store.FirstSequence(ns)
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
	ns := createNamespace(t, store, "test")
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("one")}); err != nil {
		t.Fatalf("Append(one) error = %v", err)
	}
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("two")}); err != nil {
		t.Fatalf("Append(two) error = %v", err)
	}
	if err := store.RemoveEntry(ns, 1); err != nil {
		t.Fatalf("RemoveEntry() error = %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if _, err := reopened.LoadEntry(ns, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadEntry(ns) error = %v, want %v", err, ErrNotFound)
	}
	entry, err := reopened.LoadEntry(ns, 2)
	if err != nil || string(entry.Payload) != "two" {
		t.Fatalf("LoadEntry(2) = %+v, %v, want two", entry, err)
	}
}

func TestPurgeKeepsSequence(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	for range 2 {
		if _, err := store.Append(ns, &Entry{Subject: "a"}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if n, err := store.Purge(ns); err != nil || n != 2 {
		t.Fatalf("Purge() = %d, %v, want 2, nil", n, err)
	}
	if last, err := store.LastSequence(ns); err != nil || last != 2 {
		t.Fatalf("LastSequence() = %d, %v, want 2, nil", last, err)
	}
	if seq, err := store.Append(ns, &Entry{Subject: "a"}); err != nil || seq != 3 {
		t.Fatalf("Append() after purge = %d, %v, want 3, nil", seq, err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if seq, err := reopened.Append(ns, &Entry{Subject: "a"}); err != nil || seq != 4 {
		t.Fatalf("Append() after remount = %d, %v, want 4, nil", seq, err)
	}
}

func TestPurgeUpToWritesOneRecord(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{JournalBlocks: 1})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	for range 5 {
		if _, err := store.Append(ns, &Entry{Subject: "a"}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	before := store.journal.nextUnit
	if n, err := store.PurgeUpTo(ns, math.MaxUint64); err != nil || n != 5 {
		t.Fatalf("PurgeUpTo(MaxUint64) = %d, %v, want 5, nil", n, err)
	}
	if units := store.journal.nextUnit - before; units != 1 {
		t.Fatalf("PurgeUpTo() wrote %d journal units, want 1", units)
	}
	if n, err := store.PurgeUpTo(ns, math.MaxUint64); err != nil || n != 0 {
		t.Fatalf("PurgeUpTo() again = %d, %v, want 0, nil", n, err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if _, err := reopened.FirstSequence(ns); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FirstSequence() after remount error = %v, want %v", err, ErrNotFound)
	}
}

func TestSetLastSequence(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	for range 3 {
		if _, err := store.Append(ns, &Entry{Subject: "a"}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if n, err := store.SetLastSequence(ns, 1); err != nil || n != 2 {
		t.Fatalf("SetLastSequence(1) = %d, %v, want 2, nil", n, err)
	}
	if seq, err := store.Append(ns, &Entry{Subject: "b"}); err != nil || seq != 2 {
		t.Fatalf("Append() after truncate = %d, %v, want 2, nil", seq, err)
	}
	if n, err := store.SetLastSequence(ns, 10); err != nil || n != 0 {
		t.Fatalf("SetLastSequence(10) = %d, %v, want 0, nil", n, err)
	}
	if seq, err := store.Append(ns, &Entry{Subject: "c"}); err != nil || seq != 11 {
		t.Fatalf("Append() after skip = %d, %v, want 11, nil", seq, err)
	}
	if _, err := store.SetLastSequence(ns, math.MaxUint64); err != nil {
		t.Fatalf("SetLastSequence(max) error = %v", err)
	}
	if _, err := store.Append(ns, &Entry{Subject: "d"}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Append() after max error = %v, want %v", err, ErrNoSpace)
	}
	if _, err := store.SetLastSequence(ns, 11); err != nil {
		t.Fatalf("SetLastSequence(11) error = %v", err)
	}

	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	for seq, want := range map[uint64]string{1: "a", 2: "b", 11: "c"} {
		entry, err := reopened.LoadEntry(ns, seq)
		if err != nil || entry.Subject != want {
			t.Fatalf("LoadEntry(%d) = %+v, %v, want %s", seq, entry, err, want)
		}
	}
	if _, err := reopened.LoadEntry(ns, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadEntry(3) error = %v, want %v", err, ErrNotFound)
	}
	if seq, err := reopened.Append(ns, &Entry{Subject: "e"}); err != nil || seq != 12 {
		t.Fatalf("Append() after remount = %d, %v, want 12, nil", seq, err)
	}
}
