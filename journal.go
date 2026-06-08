package scree

import (
	"encoding/binary"
	"hash/crc32"

	"src.kyanite.computer/scree/blkdev"
)

const (
	journalMagic = uint16(0x5c5e)

	journalOffsetMagic  = 0
	journalOffsetType   = 2
	journalOffsetLength = 4
	journalOffsetCRC    = 8
	journalOffsetData   = 12
)

type journal struct {
	dev       blkdev.BlockDevice
	start     int
	blocks    int
	writeUnit int
	nextUnit  int
}

type journalEntry struct {
	Type uint16
	Data []byte
}

func openJournal(dev blkdev.BlockDevice, sb superblock) (*journal, error) {
	j := &journal{
		dev:       dev,
		start:     int(sb.JournalStart),
		blocks:    int(sb.JournalBlocks),
		writeUnit: dev.WriteSize(),
	}
	entries, err := j.replay()
	if err != nil {
		return nil, err
	}
	j.nextUnit = len(entries)
	return j, nil
}

func (j *journal) append(entry journalEntry) error {
	if len(entry.Data) > j.writeUnit-journalOffsetData {
		return ErrUnsupportedFormat
	}
	if j.nextUnit >= j.capacityUnits() {
		return ErrNoSpace
	}
	buf := make([]byte, j.writeUnit)
	copyErased(buf)
	binary.LittleEndian.PutUint16(buf[journalOffsetMagic:], journalMagic)
	binary.LittleEndian.PutUint16(buf[journalOffsetType:], entry.Type)
	binary.LittleEndian.PutUint32(buf[journalOffsetLength:], uint32(len(entry.Data)))
	binary.LittleEndian.PutUint32(buf[journalOffsetCRC:], 0)
	copy(buf[journalOffsetData:], entry.Data)
	binary.LittleEndian.PutUint32(buf[journalOffsetCRC:], journalCRC(buf, len(entry.Data)))
	block, off := j.location(j.nextUnit)
	if err := j.dev.WriteAt(block, off, buf); err != nil {
		return err
	}
	j.nextUnit++
	return nil
}

func (j *journal) replay() ([]journalEntry, error) {
	var entries []journalEntry
	buf := make([]byte, j.writeUnit)
	for unit := 0; unit < j.capacityUnits(); unit++ {
		block, off := j.location(unit)
		if err := j.dev.ReadAt(block, off, buf); err != nil {
			return nil, err
		}
		if isErased(buf) {
			return entries, nil
		}
		if binary.LittleEndian.Uint16(buf[journalOffsetMagic:]) != journalMagic {
			return entries, nil
		}
		length := int(binary.LittleEndian.Uint32(buf[journalOffsetLength:]))
		if length < 0 || length > j.writeUnit-journalOffsetData {
			return entries, nil
		}
		want := binary.LittleEndian.Uint32(buf[journalOffsetCRC:])
		got := journalCRC(buf, length)
		if got != want {
			return entries, nil
		}
		data := make([]byte, length)
		copy(data, buf[journalOffsetData:journalOffsetData+length])
		entries = append(entries, journalEntry{Type: binary.LittleEndian.Uint16(buf[journalOffsetType:]), Data: data})
	}
	return entries, nil
}

func journalCRC(buf []byte, length int) uint32 {
	crcBuf := make([]byte, journalOffsetData+length-journalOffsetType)
	copy(crcBuf, buf[journalOffsetType:journalOffsetData+length])
	binary.LittleEndian.PutUint32(crcBuf[journalOffsetCRC-journalOffsetType:], 0)
	return crc32.ChecksumIEEE(crcBuf)
}

func (j *journal) capacityUnits() int {
	return j.blocks * j.dev.BlockSize() / j.writeUnit
}

func (j *journal) location(unit int) (block int, off int) {
	unitsPerBlock := j.dev.BlockSize() / j.writeUnit
	return j.start + unit/unitsPerBlock, (unit % unitsPerBlock) * j.writeUnit
}

func copyErased(buf []byte) {
	for i := range buf {
		buf[i] = 0xff
	}
}

func isErased(buf []byte) bool {
	for _, b := range buf {
		if b != 0xff {
			return false
		}
	}
	return true
}
