package scree

import (
	"errors"
	"testing"

	"src.kyanite.computer/scree/internal/testdev"
)

func TestVerifyDetectsSuperblockCorruption(t *testing.T) {
	flash, dev := testdev.New(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if err := flash.CorruptByte(superOffsetVersion, 0x00); err != nil {
		t.Fatalf("CorruptByte() error = %v", err)
	}
	if err := flash.CorruptByte(int64(dev.BlockSize()+superOffsetVersion), 0x00); err != nil {
		t.Fatalf("CorruptByte() backup error = %v", err)
	}
	if err := store.Verify(VerifyOptions{}); !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Verify() error = %v, want corruption", err)
	}
}

func TestVerifyData(t *testing.T) {
	_, dev := testdev.New(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("b")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Verify(VerifyOptions{CheckData: true}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifyDetectsJournalCorruption(t *testing.T) {
	flash, dev := testdev.New(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	ns := createNamespace(t, store, "test")
	if _, err := store.Append(ns, &Entry{Subject: "a", Payload: []byte("b")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Verify(VerifyOptions{}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	block, off := store.journal.location(0)
	if err := flash.CorruptByte(int64(block*dev.BlockSize()+off+journalOffsetData), 0x00); err != nil {
		t.Fatalf("CorruptByte() error = %v", err)
	}
	if err := store.Verify(VerifyOptions{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrCorrupt)
	}
}

func TestVerifyDetectsProgrammedUnitAfterEnd(t *testing.T) {
	flash, dev := testdev.New(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	block, off := store.journal.location(5)
	if err := flash.CorruptByte(int64(block*dev.BlockSize()+off), 0x00); err != nil {
		t.Fatalf("CorruptByte() error = %v", err)
	}
	if err := store.Verify(VerifyOptions{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify() error = %v, want %v", err, ErrCorrupt)
	}
}

func TestVerifyDataDecodesAllRecords(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry journalEntry
		want  error
	}{
		{"short namespace", journalEntry{Type: recordNamespaceCreate, Data: []byte{1, 2, 3}}, ErrCorrupt},
		{"short meta", journalEntry{Type: recordMetaPut, Data: []byte{1, 2, 3}}, ErrCorrupt},
		{"short remove", journalEntry{Type: recordEntryRemove, Data: []byte{1, 2, 3}}, ErrCorrupt},
		{"unknown type", journalEntry{Type: 0x7fff}, ErrUnsupportedFormat},
	} {
		t.Run(test.name, func(t *testing.T) {
			dev := newFormattedDevice(t, FormatOptions{})
			store, err := Mount(dev, MountOptions{})
			if err != nil {
				t.Fatalf("Mount() error = %v", err)
			}
			if err := store.journal.append(test.entry); err != nil {
				t.Fatalf("append() error = %v", err)
			}
			if err := store.Verify(VerifyOptions{}); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if err := store.Verify(VerifyOptions{CheckData: true}); !errors.Is(err, test.want) {
				t.Fatalf("Verify(CheckData) error = %v, want %v", err, test.want)
			}
			if _, err := Mount(dev, MountOptions{}); !errors.Is(err, test.want) {
				t.Fatalf("Mount() error = %v, want %v", err, test.want)
			}
		})
	}
}
