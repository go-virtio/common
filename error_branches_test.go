// SPDX-License-Identifier: BSD-3-Clause
//
// Targeted error-branch coverage for go-virtio/common: drives every
// remaining uncovered `if err != nil { return … }` / defensive sentinel
// in modern.go (InitModernConfig) and virtqueue.go
// (Avail/UsedHeaderBytes short-slice fallback, NewVirtqueue tiny-layout
// branch). Mirrors the fake-transport / nth-call / tap pattern landed in
// go-virtio/net.

package common

import (
	"encoding/binary"
	"errors"
	"testing"
)

// --- InitModernConfig error branches ----------------------------------

// walkFailReader makes WalkPCICaps return (nil, err) by failing the very
// first ReadConfig8 on the cap header. This drives InitModernConfig's
// `walkErr != nil && len(caps) == 0` branch.
type walkFailReader struct {
	memBAR
}

func (w *walkFailReader) ReadConfig8(off uint8) (uint8, error) {
	// Any cap-header byte read fails. The cap-list pointer read is
	// ReadConfig8 too — but that's handled separately upstream in
	// InitModernConfig (PCICfgCapabilitiesPtr at 0x34), so we let it
	// succeed by returning the canonical cap-list start (0x40) when
	// the offset is the CapabilitiesPtr.
	if off == PCICfgCapabilitiesPtr {
		return 0x40, nil
	}
	return 0, errors.New("walk: u8 fail")
}

func (w *walkFailReader) ReadConfig16(off uint8) (uint16, error) {
	// Status with CapList bit set so InitModernConfig proceeds past the
	// PCIStatusCapabilityList gate.
	return PCIStatusCapabilityList, nil
}

func (w *walkFailReader) ReadConfig32(off uint8) (uint32, error) {
	return 0, errors.New("walk: u32 fail")
}

func (w *walkFailReader) AllocatePages(int) (uint64, []byte, error) {
	return 0, nil, errors.New("unused")
}

func TestInitModernConfig_WalkFails_NoCaps(t *testing.T) {
	dev := &walkFailReader{}
	if _, err := InitModernConfig(dev); err == nil {
		t.Error("expected walk error with empty caps slice")
	}
}

// TestInitModernConfig_ParseCapsFails: walk succeeds (returns at least
// one cap), but ParseCaps rejects the result. Easiest path: hand it a
// single CommonCfg cap with no NotifyCfg → ErrNoNotifyCfg.
func TestInitModernConfig_ParseCapsFails(t *testing.T) {
	cfg := make([]byte, 256)
	binary.LittleEndian.PutUint16(cfg[0x06:0x08], PCIStatusCapabilityList)
	cfg[0x34] = 0x40
	// Only a CommonCfg cap, with next=0 terminator → walk succeeds and
	// returns one cap; ParseCaps surfaces ErrNoNotifyCfg.
	cc := virtioCapBytes(0x00, 16, PCICapCommonCfg, 0, 0, 0, 56)
	copy(cfg[0x40:], cc[:])
	dev := &memDevice{cfg: cfg, memBAR: newMemBAR()}
	_, err := InitModernConfig(dev)
	if !errors.Is(err, ErrNoNotifyCfg) {
		t.Errorf("got %v, want ErrNoNotifyCfg", err)
	}
}

// wrapSafeMemDev mirrors memDevice but treats config-space reads with a
// uint16-widened offset so a cap placed at the very top of cfg-space
// (where off+12 lands at 0xFC and uint8 off+4 would wrap to 0) still
// reads cleanly. Used by TestInitModernConfig_MultiplierOffsetTooHigh.
type wrapSafeMemDev struct {
	cfg []byte
	*memBAR
}

func (d *wrapSafeMemDev) ReadConfig8(off uint8) (uint8, error) {
	if int(off) >= len(d.cfg) {
		return 0, errors.New("read past cfg")
	}
	return d.cfg[off], nil
}
func (d *wrapSafeMemDev) ReadConfig16(off uint8) (uint16, error) {
	if int(off)+2 > len(d.cfg) {
		return 0, errors.New("read past cfg")
	}
	return binary.LittleEndian.Uint16(d.cfg[off : int(off)+2]), nil
}
func (d *wrapSafeMemDev) ReadConfig32(off uint8) (uint32, error) {
	if int(off)+4 > len(d.cfg) {
		return 0, errors.New("read past cfg")
	}
	return binary.LittleEndian.Uint32(d.cfg[off : int(off)+4]), nil
}
func (d *wrapSafeMemDev) AllocatePages(int) (uint64, []byte, error) {
	return 0, nil, errors.New("unused")
}

// TestInitModernConfig_MultiplierOffsetTooHigh: a NOTIFY_CFG cap placed
// so far into config-space that NotifyOffMultiplierCfgOffset(...) > 0xFF
// — InitModernConfig must fall back to multiplier=0 without erroring.
//
// NotifyOffMultiplierCfgOffset(x) = x + 16. For >0xFF we need x >= 0xF0.
// Place the cap at 0xF0 (cap fits in 0xF0..0xFF, 16 bytes). next must
// terminate.
func TestInitModernConfig_MultiplierOffsetTooHigh(t *testing.T) {
	cfg := make([]byte, 256)
	binary.LittleEndian.PutUint16(cfg[0x06:0x08], PCIStatusCapabilityList)
	cfg[0x34] = 0x40
	// CommonCfg at 0x40 → next 0xF0.
	cc := virtioCapBytes(0xF0, 16, PCICapCommonCfg, 0, 0, 0, 56)
	copy(cfg[0x40:], cc[:])
	// NotifyCfg at 0xF0, cap_len = 20 to satisfy the extended-cap
	// sanity check — so the multiplier-read branch is actually entered
	// — but the multiplier offset 0xF0 + 16 = 0x100 > 0xFF triggers the
	// fall-back to 0.
	nc := virtioCapBytes(0x00, 20, PCICapNotifyCfg, 0, 0, 0x1000, 0x100)
	copy(cfg[0xF0:], nc[:])
	dev := &wrapSafeMemDev{cfg: cfg, memBAR: newMemBAR()}
	mc, err := InitModernConfig(dev)
	if err != nil {
		t.Fatalf("InitModernConfig: %v", err)
	}
	if mc.NotifyOffMultiplier != 0 {
		t.Errorf("multiplier-too-high fallback: got %d, want 0", mc.NotifyOffMultiplier)
	}
}

// --- Virtqueue header-bytes short-slice fallback ----------------------

// TestAvailHeaderBytes_TruncatedSlice constructs a Virtqueue whose
// availSlice() is shorter than 8 bytes. The natural path is unreachable
// (size>=1 yields availSlice >= 8) so we hand-build a Virtqueue with a
// degenerate layout to exercise the defensive fallback.
func TestAvailHeaderBytes_TruncatedSlice(t *testing.T) {
	// Build a Virtqueue with size=0 layout: AvailRingOffset = 0,
	// AvailUsedEventOffset = 4 (header only), TotalSize = 14. The
	// availSlice spans [0, 6) = 6 bytes < 8.
	layout := ComputeVirtqueueLayout(0)
	mem := make([]byte, int(layout.TotalSize))
	// Mark bytes so we can see the copy worked.
	for i := range mem {
		mem[i] = byte(0xA0 + i)
	}
	q := NewVirtqueueFromAlloc(0xDEADBEEF, mem, 0, 0)
	got := q.AvailHeaderBytes()
	if len(got) != 8 {
		t.Errorf("len: got %d, want 8", len(got))
	}
	// First byte is the same as mem[AvailRingOffset]=mem[0]=0xA0.
	if got[0] != 0xA0 {
		t.Errorf("got[0]: 0x%x, want 0xA0", got[0])
	}
	// Trailing bytes past the truncated source remain zero (default
	// allocation by make).
	if got[7] != 0 {
		t.Errorf("got[7]: 0x%x, want 0", got[7])
	}
}

// TestUsedHeaderBytes_TruncatedSlice — same shape as the avail test;
// usedSlice for size=0 is 6 bytes < 16, exercising the defensive
// fallback.
func TestUsedHeaderBytes_TruncatedSlice(t *testing.T) {
	layout := ComputeVirtqueueLayout(0)
	mem := make([]byte, int(layout.TotalSize))
	for i := range mem {
		mem[i] = byte(0xB0 + i)
	}
	q := NewVirtqueueFromAlloc(0xDEADBEEF, mem, 0, 0)
	got := q.UsedHeaderBytes()
	if len(got) != 16 {
		t.Errorf("len: got %d, want 16", len(got))
	}
	// First byte comes from mem[UsedRingOffset]; with size=0,
	// UsedRingOffset = 8. mem[8] = 0xB8.
	if got[0] != 0xB8 {
		t.Errorf("got[0]: 0x%x, want 0xB8", got[0])
	}
	if got[15] != 0 {
		t.Errorf("got[15]: 0x%x, want 0", got[15])
	}
}
