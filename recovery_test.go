package scree

import (
	"errors"
	"testing"

	"src.kyanite.computer/scree/internal/testdev"
	"src.kyanite.computer/scree/internal/testflash"
)

func TestPowerLossDuringAppendKeepsPriorEntries(t *testing.T) {
	flash, dev := testdev.New(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	if _, err := store.Append(ns, &Entry{Subject: "ok", Payload: []byte("one"), Timestamp: 1}); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	flash.FailAfterWrites(0)
	_, err = store.Append(ns, &Entry{Subject: "lost", Payload: []byte("two"), Timestamp: 2})
	if !errors.Is(err, testflash.ErrInjected) {
		t.Fatalf("Append(second) error = %v, want %v", err, testflash.ErrInjected)
	}
	flash.ClearFailures()

	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	entry, err := reopened.LoadEntry(ns, 1)
	if err != nil {
		t.Fatalf("LoadEntry(ns) error = %v", err)
	}
	if entry.Subject != "ok" || string(entry.Payload) != "one" {
		t.Fatalf("LoadEntry(ns) = %+v, want ok/one", entry)
	}
	if _, err := reopened.LoadEntry(ns, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadEntry(2) error = %v, want %v", err, ErrNotFound)
	}
}

func TestTornAppendIsSkipped(t *testing.T) {
	flash, dev := testdev.New(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("one"), Timestamp: 1}); err != nil {
		t.Fatalf("Append(one) error = %v", err)
	}
	flash.TearAfterWrites(0, 16)
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("torn"), Timestamp: 2}); !errors.Is(err, testflash.ErrInjected) {
		t.Fatalf("Append(torn) error = %v, want %v", err, testflash.ErrInjected)
	}
	flash.ClearFailures()

	// The store goes on past the torn unit, and so does a remount.
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("two"), Timestamp: 3}); err != nil {
		t.Fatalf("Append(two) error = %v", err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if _, err := reopened.Append(ns, &Entry{Subject: "a", Payload: []byte("three"), Timestamp: 4}); err != nil {
		t.Fatalf("Append(three) after remount error = %v", err)
	}
	reopened, err = Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened again error = %v", err)
	}
	for seq, want := range map[uint64]string{1: "one", 2: "two", 3: "three"} {
		entry, err := reopened.LoadEntry(ns, seq)
		if err != nil || string(entry.Payload) != want {
			t.Fatalf("LoadEntry(%d) = %+v, %v, want %s", seq, entry, err, want)
		}
	}
}
