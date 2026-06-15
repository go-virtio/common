// Tests for endian.go and the little-endian on-wire invariant of the
// virtqueue index words. These run on every CI arch; on s390x
// (big-endian) they are what proves the atomic index words still land
// little-endian in memory, as virtio requires.

package common

import (
	"encoding/binary"
	"math/bits"
	"testing"
)

// TestEndianHelpersRoundTrip checks hostWordToLE / leWordToHost are
// inverses and behave per the detected host endianness.
func TestEndianHelpersRoundTrip(t *testing.T) {
	for _, w := range []uint32{0, 1, 0x0001_0000, 0xDEAD_BEEF, 0xFFFF_FFFF} {
		if got := leWordToHost(hostWordToLE(w)); got != w {
			t.Errorf("round-trip 0x%08x -> 0x%08x", w, got)
		}
		want := w
		if hostIsBigEndian {
			want = bits.ReverseBytes32(w)
		}
		if got := hostWordToLE(w); got != want {
			t.Errorf("hostWordToLE(0x%08x): got 0x%08x want 0x%08x", w, got, want)
		}
	}
}

// TestPostAvailWritesLittleEndianIdx asserts that PostAvail lays the
// avail-ring `idx` field down as little-endian bytes at offset +2,
// independent of host byte order. Before the endianness fix this stored
// the native-order atomic word, swapping flags and idx on big-endian
// hosts (s390x) and breaking the device's view of the available ring.
func TestPostAvailWritesLittleEndianIdx(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	a := q.availSlice()
	// Pre-seed a recognisable flags value at offset +0.
	binary.LittleEndian.PutUint16(a[0:2], 0x1234)

	if _, err := q.AddBuffer(0, 0xCAFE, 16, false); err != nil {
		t.Fatalf("AddBuffer: %v", err)
	}
	// AddBuffer -> PostAvail must have published idx == 1 at bytes [2:4]
	// in LITTLE-ENDIAN order, and left flags untouched at [0:2].
	if flags := binary.LittleEndian.Uint16(a[0:2]); flags != 0x1234 {
		t.Errorf("flags clobbered: got 0x%04x want 0x1234", flags)
	}
	if idx := binary.LittleEndian.Uint16(a[2:4]); idx != 1 {
		t.Errorf("avail idx bytes not little-endian: [2:4]=% x -> %d, want 1",
			a[2:4], idx)
	}
	if idx := q.AvailIdx(); idx != 1 {
		t.Errorf("AvailIdx: got %d want 1", idx)
	}
}

// TestUsedIdxReadsLittleEndianIdx asserts UsedIdx reads the used-ring
// `idx` field from little-endian bytes at offset +2 regardless of host
// byte order. A device (always little-endian per spec) writes those
// bytes; a big-endian guest that read the native atomic word would see
// garbage and never advance — the exact failure that surfaced on s390x.
func TestUsedIdxReadsLittleEndianIdx(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	u := q.usedSlice()
	// Emulate the device: little-endian flags at +0, idx at +2.
	binary.LittleEndian.PutUint16(u[0:2], 0xBEEF)
	binary.LittleEndian.PutUint16(u[2:4], 0x0102)

	if got := q.UsedIdx(); got != 0x0102 {
		t.Errorf("UsedIdx: got 0x%04x want 0x0102", got)
	}
	// Cross-check against the raw little-endian view.
	if got, raw := q.UsedIdx(), q.UsedIdxRaw(); got != raw {
		t.Errorf("UsedIdx (0x%04x) != UsedIdxRaw (0x%04x)", got, raw)
	}
}
