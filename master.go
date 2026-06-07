package scree

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"src.kyanite.computer/scree/blkdev"
)

const (
	masterMagic = "SCREEM\x01\x00"

	masterOffsetMagic      = 0
	masterOffsetSequence   = 8
	masterOffsetFreeStart  = 16
	masterOffsetFreeBlocks = 20
	masterOffsetCRC        = 24
)

type masterNode struct {
	Sequence   uint64
	FreeStart  uint32
	FreeBlocks uint32
}

func newMasterNode(sb superblock) masterNode {
	start := sb.JournalStart + sb.JournalBlocks
	return masterNode{FreeStart: start, FreeBlocks: sb.BlockCount - start}
}

func writeMaster(dev blkdev.BlockDevice, block int, m masterNode) error {
	buf := make([]byte, dev.WriteSize())
	copy(buf[masterOffsetMagic:], masterMagic)
	binary.LittleEndian.PutUint64(buf[masterOffsetSequence:], m.Sequence)
	binary.LittleEndian.PutUint32(buf[masterOffsetFreeStart:], m.FreeStart)
	binary.LittleEndian.PutUint32(buf[masterOffsetFreeBlocks:], m.FreeBlocks)
	binary.LittleEndian.PutUint32(buf[masterOffsetCRC:], crc32.ChecksumIEEE(buf[:masterOffsetCRC]))
	if err := dev.EraseBlock(block); err != nil {
		return err
	}
	return dev.WriteAt(block, 0, buf)
}

func readMaster(dev blkdev.BlockDevice, block int) (masterNode, error) {
	buf := make([]byte, dev.WriteSize())
	if err := dev.ReadAt(block, 0, buf); err != nil {
		return masterNode{}, err
	}
	if string(buf[masterOffsetMagic:masterOffsetMagic+len(masterMagic)]) != masterMagic {
		return masterNode{}, ErrCorrupt
	}
	gotCRC := binary.LittleEndian.Uint32(buf[masterOffsetCRC:])
	if gotCRC != crc32.ChecksumIEEE(buf[:masterOffsetCRC]) {
		return masterNode{}, ErrCorrupt
	}
	return masterNode{
		Sequence:   binary.LittleEndian.Uint64(buf[masterOffsetSequence:]),
		FreeStart:  binary.LittleEndian.Uint32(buf[masterOffsetFreeStart:]),
		FreeBlocks: binary.LittleEndian.Uint32(buf[masterOffsetFreeBlocks:]),
	}, nil
}

func mountMaster(dev blkdev.BlockDevice) (masterNode, error) {
	a, errA := readMaster(dev, 2)
	b, errB := readMaster(dev, 3)
	if errA == nil && errB == nil {
		if b.Sequence > a.Sequence {
			return b, nil
		}
		return a, nil
	}
	if errA == nil {
		return a, nil
	}
	if errB == nil {
		return b, nil
	}
	if errors.Is(errA, ErrUnsupportedFormat) || errors.Is(errB, ErrUnsupportedFormat) {
		return masterNode{}, ErrUnsupportedFormat
	}
	return masterNode{}, ErrCorrupt
}

func validateMaster(sb superblock, m masterNode) error {
	if m.FreeStart != sb.JournalStart+sb.JournalBlocks {
		return ErrCorrupt
	}
	if m.FreeStart+m.FreeBlocks != sb.BlockCount {
		return ErrCorrupt
	}
	return nil
}
