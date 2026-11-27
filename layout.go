package scree

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"

	"src.kyanite.computer/scree/blkdev"
)

const (
	superMagic        = "SCREE\x00\x01\x00"
	superVersion      = uint32(1)
	minBlockCount     = 8
	defaultJournalLEB = 4

	// journalStart is the first journal block, after the two superblocks
	// and the two master nodes.
	journalStart = 4

	superOffsetMagic         = 0
	superOffsetVersion       = 8
	superOffsetBlockSize     = 12
	superOffsetBlockCount    = 16
	superOffsetWriteSize     = 20
	superOffsetJournalStart  = 24
	superOffsetJournalBlocks = 28
	superOffsetFlags         = 32
	superOffsetCRC           = 36
	superSize                = 40

	superFlagAuth = uint32(1 << 0)
)

type superblock struct {
	BlockSize     uint32
	BlockCount    uint32
	WriteSize     uint32
	JournalStart  uint32
	JournalBlocks uint32
	Flags         uint32
}

// unitSize returns the size superblocks, master nodes and journal records are
// written in: the smallest multiple of the write size that holds a superblock.
func unitSize(writeSize int) int {
	return (superSize + writeSize - 1) / writeSize * writeSize
}

// checkUnitSize rejects devices whose erase blocks cannot hold a unit.
func checkUnitSize(dev blkdev.BlockDevice) error {
	if unitSize(dev.WriteSize()) > dev.BlockSize() {
		return ErrUnsupportedFormat
	}
	return nil
}

func newSuperblock(dev blkdev.BlockDevice, opts FormatOptions) (superblock, error) {
	if err := checkUnitSize(dev); err != nil {
		return superblock{}, err
	}
	if dev.BlockCount() < minBlockCount || uint64(dev.BlockCount()) > math.MaxUint32 || uint64(dev.BlockSize()) > math.MaxUint32 {
		return superblock{}, ErrUnsupportedFormat
	}
	journalBlocks := opts.JournalBlocks
	if journalBlocks == 0 {
		journalBlocks = defaultJournalLEB
	}
	if journalBlocks < 1 || journalBlocks > dev.BlockCount()-journalStart {
		return superblock{}, ErrUnsupportedFormat
	}
	var flags uint32
	if opts.EnableAuth {
		flags |= superFlagAuth
	}
	return superblock{
		BlockSize:     uint32(dev.BlockSize()),
		BlockCount:    uint32(dev.BlockCount()),
		WriteSize:     uint32(dev.WriteSize()),
		JournalStart:  journalStart,
		JournalBlocks: uint32(journalBlocks),
		Flags:         flags,
	}, nil
}

func writeSuperblock(dev blkdev.BlockDevice, block int, sb superblock) error {
	buf := make([]byte, unitSize(dev.WriteSize()))
	copy(buf[superOffsetMagic:], superMagic)
	binary.LittleEndian.PutUint32(buf[superOffsetVersion:], superVersion)
	binary.LittleEndian.PutUint32(buf[superOffsetBlockSize:], sb.BlockSize)
	binary.LittleEndian.PutUint32(buf[superOffsetBlockCount:], sb.BlockCount)
	binary.LittleEndian.PutUint32(buf[superOffsetWriteSize:], sb.WriteSize)
	binary.LittleEndian.PutUint32(buf[superOffsetJournalStart:], sb.JournalStart)
	binary.LittleEndian.PutUint32(buf[superOffsetJournalBlocks:], sb.JournalBlocks)
	binary.LittleEndian.PutUint32(buf[superOffsetFlags:], sb.Flags)
	binary.LittleEndian.PutUint32(buf[superOffsetCRC:], crc32.ChecksumIEEE(buf[:superOffsetCRC]))
	if err := dev.EraseBlock(block); err != nil {
		return err
	}
	return dev.WriteAt(block, 0, buf)
}

func readSuperblock(dev blkdev.BlockDevice, block int) (superblock, error) {
	buf := make([]byte, unitSize(dev.WriteSize()))
	if err := dev.ReadAt(block, 0, buf); err != nil {
		return superblock{}, err
	}
	if string(buf[superOffsetMagic:superOffsetMagic+len(superMagic)]) != superMagic {
		return superblock{}, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[superOffsetVersion:]) != superVersion {
		return superblock{}, ErrUnsupportedFormat
	}
	gotCRC := binary.LittleEndian.Uint32(buf[superOffsetCRC:])
	if gotCRC != crc32.ChecksumIEEE(buf[:superOffsetCRC]) {
		return superblock{}, ErrCorrupt
	}
	sb := superblock{
		BlockSize:     binary.LittleEndian.Uint32(buf[superOffsetBlockSize:]),
		BlockCount:    binary.LittleEndian.Uint32(buf[superOffsetBlockCount:]),
		WriteSize:     binary.LittleEndian.Uint32(buf[superOffsetWriteSize:]),
		JournalStart:  binary.LittleEndian.Uint32(buf[superOffsetJournalStart:]),
		JournalBlocks: binary.LittleEndian.Uint32(buf[superOffsetJournalBlocks:]),
		Flags:         binary.LittleEndian.Uint32(buf[superOffsetFlags:]),
	}
	if uint64(sb.BlockSize) != uint64(dev.BlockSize()) || uint64(sb.BlockCount) != uint64(dev.BlockCount()) || uint64(sb.WriteSize) != uint64(dev.WriteSize()) {
		return superblock{}, ErrUnsupportedFormat
	}
	if sb.JournalStart < journalStart || sb.JournalBlocks < 1 || uint64(sb.JournalStart)+uint64(sb.JournalBlocks) > uint64(sb.BlockCount) {
		return superblock{}, ErrCorrupt
	}
	return sb, nil
}

func mountSuperblock(dev blkdev.BlockDevice) (superblock, error) {
	sb, err := readSuperblock(dev, 0)
	if err == nil {
		return sb, nil
	}
	backup, backupErr := readSuperblock(dev, 1)
	if backupErr == nil {
		return backup, nil
	}
	return superblock{}, fmt.Errorf("primary superblock: %w; backup superblock: %w", err, backupErr)
}
