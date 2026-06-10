package scree

import (
	"errors"
	"testing"

	"src.kyanite.computer/scree/blkdev"
	"src.kyanite.computer/scree/driver"
	"src.kyanite.computer/scree/internal/testflash"
)

func TestVerifyDetectsSuperblockCorruption(t *testing.T) {
	flash, dev := newCorruptibleDevice(t)
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
	_, dev := newCorruptibleDevice(t)
	if err := Format(dev, FormatOptions{}); err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	store, err := Mount(dev, MountOptions{})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if _, err := store.Append(1, &Entry{Subject: "a", Payload: []byte("b")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Verify(VerifyOptions{CheckData: true}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func newCorruptibleDevice(t *testing.T) (*testflash.NOR, blkdev.BlockDevice) {
	t.Helper()
	flash, err := testflash.NewNOR(driver.Geometry{TotalSize: 16 * 4096, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff})
	if err != nil {
		t.Fatalf("testflash.NewNOR() error = %v", err)
	}
	dev, err := blkdev.NewNOR(flash)
	if err != nil {
		t.Fatalf("blkdev.NewNOR() error = %v", err)
	}
	return flash, dev
}
