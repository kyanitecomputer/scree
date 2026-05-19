package blkdev

import "errors"

var (
	// ErrInvalidGeometry reports an unusable logical block layout.
	ErrInvalidGeometry = errors.New("blkdev: invalid geometry")

	// ErrOutOfRange reports a block or offset outside the device.
	ErrOutOfRange = errors.New("blkdev: out of range")

	// ErrUnaligned reports an operation that violates write alignment.
	ErrUnaligned = errors.New("blkdev: unaligned operation")
)

// BlockDevice provides a uniform logical block interface.
type BlockDevice interface {
	BlockSize() int
	BlockCount() int
	WriteSize() int
	ReadAt(block int, off int, dst []byte) error
	WriteAt(block int, off int, src []byte) error
	EraseBlock(block int) error
	Sync() error
	Close() error
}

// Validate checks common logical block geometry constraints.
func Validate(dev BlockDevice) error {
	if dev == nil {
		return ErrInvalidGeometry
	}
	if dev.BlockSize() <= 0 || dev.BlockCount() <= 0 || dev.WriteSize() <= 0 {
		return ErrInvalidGeometry
	}
	if dev.BlockSize()%dev.WriteSize() != 0 {
		return ErrInvalidGeometry
	}
	return nil
}

// CheckRange validates a block operation range.
func CheckRange(dev BlockDevice, block int, off int, n int) error {
	if err := Validate(dev); err != nil {
		return err
	}
	if block < 0 || block >= dev.BlockCount() || off < 0 || n < 0 || off+n > dev.BlockSize() {
		return ErrOutOfRange
	}
	return nil
}

// CheckWrite validates a block write range and alignment.
func CheckWrite(dev BlockDevice, block int, off int, n int) error {
	if err := CheckRange(dev, block, off, n); err != nil {
		return err
	}
	if off%dev.WriteSize() != 0 || n%dev.WriteSize() != 0 {
		return ErrUnaligned
	}
	return nil
}
