// Host-endianness helpers for the lock-free virtqueue index words.
//
// Virtio is a LITTLE-ENDIAN transport (Virtio 1.1 §1.4 "Structure
// Specifications": all multi-byte fields are little-endian regardless
// of guest CPU). The available- and used-ring headers pack two uint16
// fields — `flags` at byte offset +0 and `idx` at +2 — into one
// naturally-aligned 32-bit word, which we publish/observe with a single
// atomic.StoreUint32/LoadUint32 to get release/acquire ordering for
// free.
//
// That atomic operates in the HOST byte order. On a little-endian host
// the in-memory bytes already match the virtio layout, so the word is
// `flags | idx<<16`. On a big-endian host (s390x) the native atomic
// would byte-swap the word relative to memory, putting `idx` in the low
// 16 bits and `flags` in the high 16 bits — corrupting both the value
// the device reads (avail idx) and the value we read back (used idx).
//
// hostWordToLE / leWordToHost bridge the two: they byte-swap on
// big-endian hosts so the *bytes in memory* are always little-endian,
// exactly as the virtio spec and the rest of this file (which uses
// binary.LittleEndian everywhere) require.
//
// SPDX-License-Identifier: BSD-3-Clause

package common

import (
	"encoding/binary"
	"math/bits"
)

// hostIsBigEndian is true on big-endian hosts (e.g. s390x). Determined
// once at init by writing 1 as a native uint16 and inspecting byte 0.
var hostIsBigEndian = func() bool {
	var b [2]byte
	binary.NativeEndian.PutUint16(b[:], 1)
	return b[0] == 0
}()

// hostWordToLE converts a logical (flags|idx<<16) word — assembled in
// the conceptual little-endian layout — into the native-uint32 value
// that, when stored with atomic.StoreUint32, lands little-endian bytes
// in memory.
func hostWordToLE(w uint32) uint32 {
	if hostIsBigEndian {
		return bits.ReverseBytes32(w)
	}
	return w
}

// leWordToHost is the inverse of hostWordToLE: it takes the native
// uint32 read by atomic.LoadUint32 (whose bytes are little-endian in
// memory) and returns the logical (flags|idx<<16) word.
func leWordToHost(w uint32) uint32 {
	if hostIsBigEndian {
		return bits.ReverseBytes32(w)
	}
	return w
}
