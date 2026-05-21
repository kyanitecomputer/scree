package driver

import "errors"

var (
	// ErrInvalidGeometry reports unusable flash geometry.
	ErrInvalidGeometry = errors.New("driver: invalid geometry")

	// ErrECCFailed reports an uncorrectable NAND ECC failure.
	ErrECCFailed = errors.New("driver: uncorrectable ECC error")

	// ErrBadBlock reports access to a known bad NAND block.
	ErrBadBlock = errors.New("driver: bad block")

	// ErrWriteFail reports a failed flash program operation.
	ErrWriteFail = errors.New("driver: write failure")

	// ErrEraseFail reports a failed flash erase operation.
	ErrEraseFail = errors.New("driver: erase failure")
)

// Geometry describes common flash device geometry.
type Geometry struct {
	TotalSize      int64
	EraseBlockSize int
	PageSize       int
	EraseValue     byte
}

// Validate rejects unusable geometry values.
func (g Geometry) Validate() error {
	if g.TotalSize <= 0 || g.EraseBlockSize <= 0 || g.PageSize <= 0 {
		return ErrInvalidGeometry
	}
	if int64(g.EraseBlockSize) > g.TotalSize {
		return ErrInvalidGeometry
	}
	if g.EraseBlockSize%g.PageSize != 0 {
		return ErrInvalidGeometry
	}
	if g.TotalSize%int64(g.EraseBlockSize) != 0 {
		return ErrInvalidGeometry
	}
	return nil
}

// Flash is the base interface for a flash device.
type Flash interface {
	Geometry() Geometry
}

// NORFlash provides byte-addressed NOR flash operations.
type NORFlash interface {
	Flash
	ReadAt(dst []byte, addr int64) (int, error)
	WriteAt(src []byte, addr int64) (int, error)
	EraseBlock(addr int64) error
}

// NANDGeometry describes NAND-specific geometry.
type NANDGeometry struct {
	PagesPerBlock int
	OOBSize       int
	MaxBitflips   int
	MaxBadBlocks  int
}

// NANDFlash provides page-addressed NAND flash operations.
type NANDFlash interface {
	Flash
	NANDGeometry() NANDGeometry
	ReadPage(page int64, data []byte, oob []byte) (bitflips int, err error)
	WritePage(page int64, data []byte, oob []byte) error
	EraseBlock(block int64) error
	IsBadBlock(block int64) (bool, error)
	MarkBadBlock(block int64) error
}
