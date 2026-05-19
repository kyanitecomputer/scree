package blkdev

import "testing"

type testDevice struct {
	blockSize  int
	blockCount int
	writeSize  int
}

func (d testDevice) BlockSize() int                 { return d.blockSize }
func (d testDevice) BlockCount() int                { return d.blockCount }
func (d testDevice) WriteSize() int                 { return d.writeSize }
func (d testDevice) ReadAt(int, int, []byte) error  { return nil }
func (d testDevice) WriteAt(int, int, []byte) error { return nil }
func (d testDevice) EraseBlock(int) error           { return nil }
func (d testDevice) Sync() error                    { return nil }
func (d testDevice) Close() error                   { return nil }

func TestValidate(t *testing.T) {
	if err := Validate(testDevice{blockSize: 4096, blockCount: 2, writeSize: 256}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := Validate(testDevice{blockSize: 4096, blockCount: 2, writeSize: 300}); err != ErrInvalidGeometry {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidGeometry)
	}
}

func TestCheckWrite(t *testing.T) {
	dev := testDevice{blockSize: 4096, blockCount: 2, writeSize: 256}
	if err := CheckWrite(dev, 1, 256, 512); err != nil {
		t.Fatalf("CheckWrite() error = %v", err)
	}
	if err := CheckWrite(dev, 2, 0, 256); err != ErrOutOfRange {
		t.Fatalf("CheckWrite() out of range error = %v, want %v", err, ErrOutOfRange)
	}
	if err := CheckWrite(dev, 0, 1, 256); err != ErrUnaligned {
		t.Fatalf("CheckWrite() unaligned error = %v, want %v", err, ErrUnaligned)
	}
}
