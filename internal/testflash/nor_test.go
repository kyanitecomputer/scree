package testflash

import (
	"errors"
	"math"
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

func TestNORRejectsOverflowingRange(t *testing.T) {
	nor, err := NewNOR(testGeometry())
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}
	if _, err := nor.ReadAt(make([]byte, 256), math.MaxInt64-128); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("ReadAt() error = %v, want %v", err, ErrOutOfRange)
	}
	if _, err := nor.WriteAt(make([]byte, 256), math.MaxInt64-128); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("WriteAt() error = %v, want %v", err, ErrOutOfRange)
	}
}

func TestNORTearAfterWrites(t *testing.T) {
	nor, err := NewNOR(testGeometry())
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}
	nor.TearAfterWrites(1, 16)
	src := make([]byte, 256)
	if _, err := nor.WriteAt(src, 0); err != nil {
		t.Fatalf("WriteAt(first) error = %v", err)
	}
	if _, err := nor.WriteAt(src, 256); !errors.Is(err, ErrInjected) {
		t.Fatalf("WriteAt(torn) error = %v, want %v", err, ErrInjected)
	}
	got := make([]byte, 256)
	if _, err := nor.ReadAt(got, 256); err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	for i, b := range got {
		want := byte(0xff)
		if i < 16 {
			want = 0
		}
		if b != want {
			t.Fatalf("ReadAt()[%d] = %#x, want %#x", i, b, want)
		}
	}
	nor.ClearFailures()
	if _, err := nor.WriteAt(src, 512); err != nil {
		t.Fatalf("WriteAt(after clear) error = %v", err)
	}
}

func TestNORProgramsAwayFromEraseValue(t *testing.T) {
	geometry := testGeometry()
	geometry.EraseValue = 0x00
	nor, err := NewNOR(geometry)
	if err != nil {
		t.Fatalf("NewNOR() error = %v", err)
	}
	first := make([]byte, 256)
	for i := range first {
		first[i] = 0xf0
	}
	if _, err := nor.WriteAt(first, 0); err != nil {
		t.Fatalf("WriteAt(first) error = %v", err)
	}
	if _, err := nor.WriteAt(make([]byte, 256), 0); !errors.Is(err, ErrNotErased) {
		t.Fatalf("WriteAt(erased over programmed) error = %v, want %v", err, ErrNotErased)
	}
	second := make([]byte, 256)
	for i := range second {
		second[i] = 0xff
	}
	if _, err := nor.WriteAt(second, 0); err != nil {
		t.Fatalf("WriteAt(second) error = %v", err)
	}
	got := make([]byte, 256)
	if _, err := nor.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	if got[0] != 0xff {
		t.Fatalf("ReadAt()[0] = %#x, want 0xff", got[0])
	}
}
