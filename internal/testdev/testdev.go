// Package testdev provides block devices on simulated flash for tests.
package testdev

import (
	"testing"

	"src.kyanite.computer/scree/blkdev"
	"src.kyanite.computer/scree/driver"
	"src.kyanite.computer/scree/internal/testflash"
)

// Geometry is the flash geometry of New: 16 erase blocks of 4 KiB,
// programmed in pages of 256 bytes, erased to 0xff.
var Geometry = driver.Geometry{TotalSize: 16 * 4096, EraseBlockSize: 4096, PageSize: 256, EraseValue: 0xff}

// New returns a block device on new simulated flash of Geometry, and the flash.
func New(t testing.TB) (*testflash.NOR, blkdev.BlockDevice) {
	t.Helper()
	return NewGeometry(t, Geometry)
}

// NewGeometry returns a block device on new simulated flash of geometry, and
// the flash.
func NewGeometry(t testing.TB, geometry driver.Geometry) (*testflash.NOR, blkdev.BlockDevice) {
	t.Helper()
	flash, err := testflash.NewNOR(geometry)
	if err != nil {
		t.Fatalf("testflash.NewNOR() error = %v", err)
	}
	dev, err := blkdev.NewNOR(flash)
	if err != nil {
		t.Fatalf("blkdev.NewNOR() error = %v", err)
	}
	return flash, dev
}
