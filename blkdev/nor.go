package blkdev

import (
	"fmt"

	"src.kyanite.computer/scree/driver"
)

// NOR wraps NOR flash as a logical block device.
type NOR struct {
	flash driver.NORFlash
	geom  driver.Geometry
}

// NewNOR returns a logical block device backed by NOR flash.
func NewNOR(flash driver.NORFlash) (*NOR, error) {
	if flash == nil {
		return nil, ErrInvalidGeometry
	}
	geom := flash.Geometry()
	if err := geom.Validate(); err != nil {
		return nil, fmt.Errorf("validate NOR geometry: %w", err)
	}
	return &NOR{flash: flash, geom: geom}, nil
}

// BlockSize returns the logical erase block size.
func (n *NOR) BlockSize() int {
	return n.geom.EraseBlockSize
}

// BlockCount returns the number of logical erase blocks.
func (n *NOR) BlockCount() int {
	return int(n.geom.TotalSize) / n.geom.EraseBlockSize
}

// WriteSize returns the minimum write granularity.
func (n *NOR) WriteSize() int {
	return n.geom.PageSize
}

// ReadAt reads from a logical block offset.
func (n *NOR) ReadAt(block int, off int, dst []byte) error {
	if err := CheckRange(n, block, off, len(dst)); err != nil {
		return err
	}
	addr := n.addr(block, off)
	read, err := n.flash.ReadAt(dst, addr)
	if err != nil {
		return fmt.Errorf("read NOR block %d offset %d: %w", block, off, err)
	}
	if read != len(dst) {
		return fmt.Errorf("read NOR block %d offset %d: short read", block, off)
	}
	return nil
}

// WriteAt writes to a logical block offset.
func (n *NOR) WriteAt(block int, off int, src []byte) error {
	if err := CheckWrite(n, block, off, len(src)); err != nil {
		return err
	}
	addr := n.addr(block, off)
	written, err := n.flash.WriteAt(src, addr)
	if err != nil {
		return fmt.Errorf("write NOR block %d offset %d: %w", block, off, err)
	}
	if written != len(src) {
		return fmt.Errorf("write NOR block %d offset %d: short write", block, off)
	}
	return nil
}

// EraseBlock erases a logical block.
func (n *NOR) EraseBlock(block int) error {
	if block < 0 || block >= n.BlockCount() {
		return ErrOutOfRange
	}
	if err := n.flash.EraseBlock(n.addr(block, 0)); err != nil {
		return fmt.Errorf("erase NOR block %d: %w", block, err)
	}
	return nil
}

// Sync ensures pending writes are committed.
func (n *NOR) Sync() error {
	return nil
}

// Close releases resources associated with the block device.
func (n *NOR) Close() error {
	return nil
}

func (n *NOR) addr(block int, off int) int64 {
	return int64(block*n.geom.EraseBlockSize + off)
}
