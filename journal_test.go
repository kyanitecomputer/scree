package scree

import "testing"

func TestJournalAppendReplay(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{JournalBlocks: 1})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if err := store.journal.append(journalEntry{Type: 1, Data: []byte("one")}); err != nil {
		t.Fatalf("append(one) error = %v", err)
	}
	if err := store.journal.append(journalEntry{Type: 2, Data: []byte("two")}); err != nil {
		t.Fatalf("append(two) error = %v", err)
	}

	reopened, err := openJournal(dev, store.super)
	if err != nil {
		t.Fatalf("openJournal() error = %v", err)
	}
	entries, err := reopened.replay()
	if err != nil {
		t.Fatalf("replay() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("replay() len = %d, want 2", len(entries))
	}
	if entries[0].Type != 1 || string(entries[0].Data) != "one" {
		t.Fatalf("replay()[0] = %+v, want type 1 data one", entries[0])
	}
	if entries[1].Type != 2 || string(entries[1].Data) != "two" {
		t.Fatalf("replay()[1] = %+v, want type 2 data two", entries[1])
	}
}

func TestJournalStopsAtCorruptEntry(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{JournalBlocks: 1})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if err := store.journal.append(journalEntry{Type: 1, Data: []byte("one")}); err != nil {
		t.Fatalf("append(one) error = %v", err)
	}
	block, off := store.journal.location(1)
	bad := make([]byte, dev.WriteSize())
	copyErased(bad)
	bad[0] = 0
	if err := dev.WriteAt(block, off, bad); err != nil {
		t.Fatalf("WriteAt(bad) error = %v", err)
	}
	entries, err := store.journal.replay()
	if err != nil {
		t.Fatalf("replay() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("replay() len = %d, want 1", len(entries))
	}
}
