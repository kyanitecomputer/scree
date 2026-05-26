package testflash

import (
	"errors"
	"testing"

	"src.kyanite.computer/scree/driver"
)

func testGeometry() driver.Geometry {
	return driver.Geometry{TotalSize: 8192, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff}
}

func TestNORReadWriteErase(t *testing.T) {
	nor, err := NewNOR(testGeometry())
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}

	src := make([]byte, 256)
	for i := range src {
		src[i] = 0xa5
	}
	if _, err := nor.WriteAt(src, 0); err != nil {
		t.Fatalf("WriteAt() error = %v", err)
	}

	dst := make([]byte, 256)
	if _, err := nor.ReadAt(dst, 0); err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	for i, b := range dst {
		if b != src[i] {
			t.Fatalf("ReadAt()[%d] = %#x, want %#x", i, b, src[i])
		}
	}

	if err := nor.EraseBlock(0); err != nil {
		t.Fatalf("EraseBlock() error = %v", err)
	}
	if count, err := nor.EraseCount(0); err != nil || count != 1 {
		t.Fatalf("EraseCount(0) = %d, %v, want 1, nil", count, err)
	}
}

func TestNORRejectsUnalignedWrite(t *testing.T) {
	nor, err := NewNOR(testGeometry())
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}
	if _, err := nor.WriteAt(make([]byte, 1), 0); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("WriteAt() error = %v, want %v", err, ErrUnaligned)
	}
}

func TestNORRejectsWriteToProgrammedBits(t *testing.T) {
	nor, err := NewNOR(testGeometry())
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}
	first := make([]byte, 256)
	second := make([]byte, 256)
	for i := range first {
		first[i] = 0x00
		second[i] = 0xff
	}
	if _, err := nor.WriteAt(first, 0); err != nil {
		t.Fatalf("WriteAt(first) error = %v", err)
	}
	if _, err := nor.WriteAt(second, 0); !errors.Is(err, ErrNotErased) {
		t.Fatalf("WriteAt(second) error = %v, want %v", err, ErrNotErased)
	}
}
