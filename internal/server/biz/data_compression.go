package biz

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

const (
	// DefaultMinCompressSize is the smallest payload worth compressing. Below this
	// the zstd frame overhead (roughly 9 bytes plus a content checksum) can make
	// the stored bytes larger than the input, and small bodies are also the ones
	// an operator is most likely to inspect by hand.
	DefaultMinCompressSize = 4 * 1024

	// zstdFrameMagicLen is the length of the zstd frame magic number.
	zstdFrameMagicLen = 4
)

// zstdFrameMagic is the 4-byte magic that starts every zstd frame. It cannot be
// confused with a JSON document, which always starts with '{' (0x7b), so the
// reader can transparently detect compressed payloads without any metadata column.
var zstdFrameMagic = [zstdFrameMagicLen]byte{0x28, 0xB5, 0x2F, 0xFD}

// The encoder/decoder are process-wide because zstd's EncodeAll/DecodeAll are
// safe for concurrent use and allocating them per call dominates the cost for
// payloads of a few hundred kilobytes.
var (
	zstdEncoder *zstd.Encoder
	zstdDecoder *zstd.Decoder
)

func init() {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(fmt.Sprintf("failed to create zstd encoder: %v", err))
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("failed to create zstd decoder: %v", err))
	}

	zstdEncoder = enc
	zstdDecoder = dec
}

// isZstdFrame reports whether data starts with the zstd frame magic.
func isZstdFrame(data []byte) bool {
	if len(data) < zstdFrameMagicLen {
		return false
	}

	return data[0] == zstdFrameMagic[0] &&
		data[1] == zstdFrameMagic[1] &&
		data[2] == zstdFrameMagic[2] &&
		data[3] == zstdFrameMagic[3]
}

// compressZstd returns a zstd-compressed copy of data.
func compressZstd(data []byte) []byte {
	return zstdEncoder.EncodeAll(data, nil)
}

// decompressZstd expands a zstd frame. Data that is not a zstd frame is returned
// unchanged, which keeps every file written before compression existed readable
// without a migration step.
func decompressZstd(data []byte) ([]byte, error) {
	if !isZstdFrame(data) {
		return data, nil
	}

	out, err := zstdDecoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress zstd payload: %w", err)
	}

	return out, nil
}
