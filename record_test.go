package scree

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestDecodeEntryRecordBounds(t *testing.T) {
	data, err := encodeEntryRecord(entryRecord{Namespace: 1, StoredEntry: StoredEntry{Sequence: 2, Subject: "s", Header: []byte("h"), Payload: []byte("p")}})
	if err != nil {
		t.Fatalf("encodeEntryRecord() error = %v", err)
	}
	for n := range entryHeaderSize {
		if _, err := decodeEntryRecord(data[:n]); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("decodeEntryRecord(%d bytes) error = %v, want %v", n, err, ErrCorrupt)
		}
	}
	if _, err := decodeEntryRecord(data[:len(data)-1]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decodeEntryRecord(truncated) error = %v, want %v", err, ErrCorrupt)
	}
	huge := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(huge[26:], math.MaxUint32)
	binary.LittleEndian.PutUint32(huge[30:], math.MaxUint32)
	if _, err := decodeEntryRecord(huge); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decodeEntryRecord(huge lengths) error = %v, want %v", err, ErrCorrupt)
	}
	record, err := decodeEntryRecord(data)
	if err != nil || record.Subject != "s" || string(record.Header) != "h" || string(record.Payload) != "p" {
		t.Fatalf("decodeEntryRecord() = %+v, %v, want s/h/p", record, err)
	}
}

func TestDecodeMetaRecordBounds(t *testing.T) {
	data, err := encodeMetaRecord(1, "key", []byte("value"))
	if err != nil {
		t.Fatalf("encodeMetaRecord() error = %v", err)
	}
	binary.LittleEndian.PutUint32(data[10:], math.MaxUint32)
	if _, _, _, err := decodeMetaRecord(data); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decodeMetaRecord(huge length) error = %v, want %v", err, ErrCorrupt)
	}
}

func TestEncodeRecordsRejectLongFields(t *testing.T) {
	long := strings.Repeat("x", math.MaxUint16+1)
	if _, err := encodeNamespaceRecord(1, long); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("encodeNamespaceRecord() error = %v, want %v", err, ErrInvalidArgument)
	}
	if _, err := encodeMetaRecord(1, long, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("encodeMetaRecord() error = %v, want %v", err, ErrInvalidArgument)
	}
	if _, err := encodeEntryRecord(entryRecord{StoredEntry: StoredEntry{Subject: long}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("encodeEntryRecord() error = %v, want %v", err, ErrInvalidArgument)
	}
}
