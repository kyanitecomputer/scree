package scree

import (
	"hash/crc32"
	"testing"

	"src.kyanite.computer/scree/blkdev"
	"src.kyanite.computer/scree/driver"
	"src.kyanite.computer/scree/internal/testflash"
)

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

	entries, bad, err := openJournal(dev, store.super).replay()
	if err != nil || bad != 0 {
		t.Fatalf("replay() bad = %d, error = %v, want 0, nil", bad, err)
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

func TestJournalSkipsCorruptEntry(t *testing.T) {
	dev := newFormattedDevice(t, FormatOptions{JournalBlocks: 1})
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if err := store.journal.append(journalEntry{Type: 1, Data: []byte("one")}); err != nil {
		t.Fatalf("append(one) error = %v", err)
	}
	block, off := store.journal.location(1)
	torn := make([]byte, dev.WriteSize())
	fillErased(torn, dev.EraseValue())
	torn[0] = 0
	if err := dev.WriteAt(block, off, torn); err != nil {
		t.Fatalf("WriteAt(torn) error = %v", err)
	}
	store.journal.nextUnit = 2
	if err := store.journal.append(journalEntry{Type: 1, Data: []byte("three")}); err != nil {
		t.Fatalf("append(three) error = %v", err)
	}
	reopened := openJournal(dev, store.super)
	entries, bad, err := reopened.replay()
	if err != nil {
		t.Fatalf("replay() error = %v", err)
	}
	if len(entries) != 2 || bad != 1 || string(entries[1].Data) != "three" {
		t.Fatalf("replay() = %+v, bad %d, want one, three and 1 bad unit", entries, bad)
	}
	if reopened.nextUnit != 3 {
		t.Fatalf("replay() nextUnit = %d, want 3", reopened.nextUnit)
	}
}

func TestJournalUsesEraseValue(t *testing.T) {
	flash, err := testflash.NewNOR(driver.Geometry{TotalSize: 16 * 4096, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0x00})
	if err != nil {
		t.Fatalf("testflash.NewNOR() error = %v", err)
	}
	dev, err := blkdev.NewNOR(flash)
	if err != nil {
		t.Fatalf("blkdev.NewNOR() error = %v", err)
	}
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if _, err := store.CreateNamespace("a"); err != nil {
		t.Fatalf("CreateNamespace() error = %v", err)
	}
	reopened, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() reopened error = %v", err)
	}
	if _, ok, _ := reopened.LookupNamespace("a"); !ok {
		t.Fatal("LookupNamespace() found nothing after remount")
	}
	if err := reopened.Verify(VerifyOptions{CheckData: true}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestJournalCRC(t *testing.T) {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = byte(i * 7)
	}
	const length = 20
	// The CRC covers the record from its type on, with a zero CRC field.
	covered := append([]byte(nil), buf[journalOffsetType:journalOffsetData+length]...)
	clear(covered[journalOffsetCRC-journalOffsetType : journalOffsetData-journalOffsetType])
	if got, want := journalCRC(buf, length), crc32.ChecksumIEEE(covered); got != want {
		t.Fatalf("journalCRC() = %#x, want %#x", got, want)
	}
	if n := testing.AllocsPerRun(10, func() { journalCRC(buf, length) }); n != 0 {
		t.Fatalf("journalCRC() allocates %v times", n)
	}
}
