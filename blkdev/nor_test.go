package blkdev

import (
	"errors"
	"testing"

	"src.kyanite.computer/scree/driver"
	"src.kyanite.computer/scree/internal/testflash"
)

func newTestNOR(t *testing.T) *NOR {
	t.Helper()
	flash, err := testflash.NewNOR(driver.Geometry{TotalSize: 8192, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff})
	if err != nil {
		t.Fatalf("testflash.NewNOR() error = %v", err)
	}
	dev, err := NewNOR(flash)
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}
	return dev
}

func TestNORBlockGeometry(t *testing.T) {
	dev := newTestNOR(t)
	if dev.BlockSize() != 4096 {
		t.Fatalf("BlockSize() = %d, want 4096", dev.BlockSize())
	}
	if dev.BlockCount() != 2 {
		t.Fatalf("BlockCount() = %d, want 2", dev.BlockCount())
	}
	if dev.WriteSize() != 256 {
		t.Fatalf("WriteSize() = %d, want 256", dev.WriteSize())
	}
}

func TestNORReadWriteErase(t *testing.T) {
	dev := newTestNOR(t)
	want := make([]byte, dev.WriteSize())
	for i := range want {
		want[i] = 0x5a
	}
	if err := dev.WriteAt(1, 0, want); err != nil {
		t.Fatalf("WriteAt() error = %v", err)
	}
	got := make([]byte, dev.WriteSize())
	if err := dev.ReadAt(1, 0, got); err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ReadAt()[%d] = %#x, want %#x", i, got[i], want[i])
		}
	}
	if err := dev.EraseBlock(1); err != nil {
		t.Fatalf("EraseBlock() error = %v", err)
	}
	if err := dev.ReadAt(1, 0, got); err != nil {
		t.Fatalf("ReadAt() after erase error = %v", err)
	}
	for i, b := range got {
		if b != 0xff {
			t.Fatalf("ReadAt() after erase[%d] = %#x, want 0xff", i, b)
		}
	}
}

func TestNORRejectsInvalidOperations(t *testing.T) {
	dev := newTestNOR(t)
	if err := dev.WriteAt(0, 1, make([]byte, dev.WriteSize())); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("WriteAt() error = %v, want %v", err, ErrUnaligned)
	}
	if err := dev.WriteAt(9, 0, make([]byte, dev.WriteSize())); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("WriteAt() error = %v, want %v", err, ErrOutOfRange)
	}
	if err := dev.EraseBlock(9); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("EraseBlock() error = %v, want %v", err, ErrOutOfRange)
	}
}
