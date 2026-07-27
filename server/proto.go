package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type wireField struct {
	number int
	kind   int
	value  uint64
	bytes  []byte
}

func putVarint(dst []byte, n uint64) []byte {
	for n >= 0x80 {
		dst = append(dst, byte(n)|0x80)
		n >>= 7
	}
	return append(dst, byte(n))
}

func pbVarint(dst []byte, field int, n uint64) []byte {
	dst = putVarint(dst, uint64(field<<3))
	return putVarint(dst, n)
}

func pbBytes(dst []byte, field int, b []byte) []byte {
	dst = putVarint(dst, uint64(field<<3|2))
	dst = putVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

func pbString(dst []byte, field int, s string) []byte { return pbBytes(dst, field, []byte(s)) }

func parseWire(data []byte) ([]wireField, error) {
	fields := make([]wireField, 0, 8)
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, errors.New("invalid protobuf tag")
		}
		data = data[n:]
		f := wireField{number: int(tag >> 3), kind: int(tag & 7)}
		switch f.kind {
		case 0:
			v, m := binary.Uvarint(data)
			if m <= 0 {
				return nil, errors.New("invalid protobuf varint")
			}
			f.value = v
			data = data[m:]
		case 1:
			if len(data) < 8 {
				return nil, io.ErrUnexpectedEOF
			}
			f.bytes = append([]byte(nil), data[:8]...)
			data = data[8:]
		case 2:
			l, m := binary.Uvarint(data)
			if m <= 0 || l > uint64(len(data)-m) {
				return nil, io.ErrUnexpectedEOF
			}
			data = data[m:]
			f.bytes = append([]byte(nil), data[:int(l)]...)
			data = data[int(l):]
		case 5:
			if len(data) < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			f.bytes = append([]byte(nil), data[:4]...)
			data = data[4:]
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", f.kind)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func firstField(fields []wireField, number int) (wireField, bool) {
	for _, f := range fields {
		if f.number == number {
			return f, true
		}
	}
	return wireField{}, false
}

func writeFrame(w io.Writer, payload []byte) error {
	var prefix [10]byte
	n := binary.PutUvarint(prefix[:], uint64(len(payload)))
	if _, err := w.Write(prefix[:n]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r *bufio.Reader, max int) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if n == 0 || n > uint64(max) {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	b := make([]byte, int(n))
	_, err = io.ReadFull(r, b)
	return b, err
}

func nestedVarint(data []byte, field int) (uint64, bool) {
	fs, err := parseWire(data)
	if err != nil {
		return 0, false
	}
	f, ok := firstField(fs, field)
	return f.value, ok && f.kind == 0
}

func textFieldStatus(data []byte) (map[string]any, bool) {
	fs, err := parseWire(data)
	if err != nil {
		return nil, false
	}
	out := map[string]any{"value": "", "label": "", "start": 0, "end": 0}
	found := false
	for _, f := range fs {
		switch f.number {
		case 1:
			out["counter"] = int(f.value)
			found = true
		case 2:
			out["value"] = string(f.bytes)
			found = true
		case 3:
			out["start"] = int(f.value)
			found = true
		case 4:
			out["end"] = int(f.value)
			found = true
		case 6:
			out["label"] = string(f.bytes)
			found = true
		}
	}
	return out, found
}
