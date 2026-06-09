// Tests for pci.go. The hot path is WalkPCICaps; live hosts drive it
// via PCIConfigReader callbacks bridged to host transport primitives,
// here we feed it a hand-rolled 256-byte config-space buffer.

package common

import (
	"encoding/binary"
	"errors"
	"testing"
)

// fakeReader implements PCIConfigReader against a fixed-size byte
// buffer. Errors when reading past the buffer's length so tests can
// exercise the error-propagation paths.
type fakeReader struct {
	cfg []byte
	// failU8At / failU32At: when > 0, fail the Nth call to that width.
	failU8At  int
	u8Calls   int
	failU32At int
	u32Calls  int
}

func (r *fakeReader) ReadConfig8(off uint8) (uint8, error) {
	r.u8Calls++
	if r.failU8At > 0 && r.u8Calls == r.failU8At {
		return 0, errors.New("fake: injected u8 failure")
	}
	if int(off) >= len(r.cfg) {
		return 0, errors.New("fake: read past config-space")
	}
	return r.cfg[off], nil
}

func (r *fakeReader) ReadConfig16(off uint8) (uint16, error) {
	if int(off)+2 > len(r.cfg) {
		return 0, errors.New("fake: read past config-space (u16)")
	}
	return binary.LittleEndian.Uint16(r.cfg[off : off+2]), nil
}

func (r *fakeReader) ReadConfig32(off uint8) (uint32, error) {
	r.u32Calls++
	if r.failU32At > 0 && r.u32Calls == r.failU32At {
		return 0, errors.New("fake: injected u32 failure")
	}
	if int(off)+4 > len(r.cfg) {
		return 0, errors.New("fake: read past config-space (u32)")
	}
	return binary.LittleEndian.Uint32(r.cfg[off : off+4]), nil
}

// synthConfigSpace returns a 256-byte config-space buffer with vendor
// IDs, Status[CapList] set, the CapabilitiesPtr at 0x34 set, and the
// given raw cap-list bytes pasted in starting at offset `firstCap`.
func synthConfigSpace(t *testing.T, vid, did uint16, status uint16, firstCap uint8, capBytes []byte) []byte {
	t.Helper()
	cfg := make([]byte, 256)
	binary.LittleEndian.PutUint16(cfg[0x00:0x02], vid)
	binary.LittleEndian.PutUint16(cfg[0x02:0x04], did)
	binary.LittleEndian.PutUint16(cfg[0x06:0x08], status)
	cfg[0x34] = firstCap
	if int(firstCap)+len(capBytes) > len(cfg) {
		t.Fatalf("synthConfigSpace: cap bytes overflow (cap=%d len=%d)", firstCap, len(capBytes))
	}
	copy(cfg[firstCap:int(firstCap)+len(capBytes)], capBytes)
	return cfg
}

// virtioCapBytes lays out one struct virtio_pci_cap (16 bytes) at the
// canonical offsets per Virtio 1.1 §4.1.4.
func virtioCapBytes(next, clen, cfgType, bar, id uint8, offset, length uint32) [16]byte {
	var b [16]byte
	b[0] = PCICapIDVendorSpecific
	b[1] = next
	b[2] = clen
	b[3] = cfgType
	b[4] = bar
	b[5] = id
	binary.LittleEndian.PutUint32(b[8:12], offset)
	binary.LittleEndian.PutUint32(b[12:16], length)
	return b
}

func TestWalkPCICaps_ModernNet(t *testing.T) {
	var caps []byte
	at := func(b [16]byte) { caps = append(caps, b[:]...) }
	at(virtioCapBytes(0x50, 16, PCICapCommonCfg, 4, 0, 0x0000, 0x38))
	at(virtioCapBytes(0x60, 16, PCICapNotifyCfg, 4, 0, 0x1000, 0x1000))
	at(virtioCapBytes(0x70, 16, PCICapISRCfg, 4, 0, 0x2000, 0x1000))
	at(virtioCapBytes(0x80, 16, PCICapDeviceCfg, 4, 0, 0x3000, 12))
	at(virtioCapBytes(0x00, 16, PCICapPCICfg, 0, 0, 0x0000, 0x0000))

	cfg := synthConfigSpace(t, PCIVendorID, PCIDeviceIDModernNet, PCIStatusCapabilityList, 0x40, caps)
	r := &fakeReader{cfg: cfg}
	got, err := WalkPCICaps(r, 0x40)
	if err != nil {
		t.Fatalf("WalkPCICaps: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d caps, want 5", len(got))
	}
	wantTypes := []uint8{PCICapCommonCfg, PCICapNotifyCfg, PCICapISRCfg, PCICapDeviceCfg, PCICapPCICfg}
	for i, w := range wantTypes {
		if got[i].CfgType != w {
			t.Errorf("cap[%d].CfgType = %d, want %d", i, got[i].CfgType, w)
		}
	}
	if got[0].Offset != 0 || got[0].Length != 0x38 || got[0].BAR != 4 {
		t.Errorf("CommonCfg locator wrong: %+v", got[0])
	}
	if got[3].Offset != 0x3000 || got[3].Length != 12 || got[3].BAR != 4 {
		t.Errorf("DeviceCfg locator wrong: %+v", got[3])
	}
	if got[0].CfgSpaceOffset != 0x40 || got[1].CfgSpaceOffset != 0x50 {
		t.Errorf("CfgSpaceOffset attribution wrong: %+v", got)
	}
}

func TestWalkPCICaps_SkipsNonVendor(t *testing.T) {
	// 0x40 = MSI-X (cap_id 0x11), next 0x50. 0x50 = virtio CommonCfg.
	msix := [16]byte{0x11, 0x50}
	common := virtioCapBytes(0x00, 16, PCICapCommonCfg, 0, 0, 0, 0)
	caps := append(msix[:], common[:]...)
	cfg := synthConfigSpace(t, PCIVendorID, PCIDeviceIDModernNet, PCIStatusCapabilityList, 0x40, caps)
	r := &fakeReader{cfg: cfg}
	got, err := WalkPCICaps(r, 0x40)
	if err != nil {
		t.Fatalf("WalkPCICaps: %v", err)
	}
	if len(got) != 1 || got[0].CfgType != PCICapCommonCfg {
		t.Fatalf("expected one CommonCfg, got %+v", got)
	}
}

func TestWalkPCICaps_EmptyChain(t *testing.T) {
	r := &fakeReader{cfg: make([]byte, 256)}
	got, err := WalkPCICaps(r, 0)
	if err != nil {
		t.Fatalf("WalkPCICaps(0): %v", err)
	}
	if got != nil {
		t.Fatalf("WalkPCICaps(0): expected nil, got %+v", got)
	}
}

func TestWalkPCICaps_BadFirstPtr(t *testing.T) {
	r := &fakeReader{cfg: make([]byte, 256)}
	_, err := WalkPCICaps(r, 0x10)
	if !errors.Is(err, ErrCapChainBadPtr) {
		t.Fatalf("expected ErrCapChainBadPtr, got %v", err)
	}
}

func TestWalkPCICaps_Cycle(t *testing.T) {
	c := virtioCapBytes(0x40 /* next = self */, 16, PCICapCommonCfg, 0, 0, 0, 0)
	cfg := synthConfigSpace(t, PCIVendorID, PCIDeviceIDModernNet, PCIStatusCapabilityList, 0x40, c[:])
	r := &fakeReader{cfg: cfg}
	got, err := WalkPCICaps(r, 0x40)
	if !errors.Is(err, ErrCapChainTooLong) {
		t.Fatalf("expected ErrCapChainTooLong, got %v (len=%d)", err, len(got))
	}
	if len(got) != MaxCapsToWalk {
		t.Errorf("expected %d caps before bailing, got %d", MaxCapsToWalk, len(got))
	}
}

func TestWalkPCICaps_ReadU8Errors(t *testing.T) {
	var caps []byte
	at := func(b [16]byte) { caps = append(caps, b[:]...) }
	at(virtioCapBytes(0x50, 16, PCICapCommonCfg, 0, 0, 0, 0))
	at(virtioCapBytes(0x00, 16, PCICapNotifyCfg, 0, 0, 0, 0))
	cfg := synthConfigSpace(t, PCIVendorID, PCIDeviceIDModernNet, PCIStatusCapabilityList, 0x40, caps)
	for _, failAt := range []int{1, 2, 3, 4, 5, 6, 7} {
		r := &fakeReader{cfg: cfg, failU8At: failAt}
		_, err := WalkPCICaps(r, 0x40)
		if err == nil {
			t.Errorf("failU8At=%d: expected error, got nil", failAt)
		}
	}
}

func TestWalkPCICaps_ReadU32Errors(t *testing.T) {
	c := virtioCapBytes(0x00, 16, PCICapCommonCfg, 0, 0, 0, 0)
	cfg := synthConfigSpace(t, PCIVendorID, PCIDeviceIDModernNet, PCIStatusCapabilityList, 0x40, c[:])
	// First u32 read (offset at +8) — fails before we collect the cap.
	r1 := &fakeReader{cfg: cfg, failU32At: 1}
	if _, err := WalkPCICaps(r1, 0x40); err == nil {
		t.Errorf("failU32At=1: expected error")
	}
	// Second u32 read (length at +12) — fails after offset read.
	r2 := &fakeReader{cfg: cfg, failU32At: 2}
	if _, err := WalkPCICaps(r2, 0x40); err == nil {
		t.Errorf("failU32At=2: expected error")
	}
}

func TestPCICapsByType(t *testing.T) {
	caps := []PCICap{
		{CfgType: PCICapCommonCfg, BAR: 1},
		{CfgType: PCICapDeviceCfg, BAR: 4, Offset: 0x3000},
	}
	got := PCICapsByType(caps, PCICapDeviceCfg)
	if got == nil || got.BAR != 4 || got.Offset != 0x3000 {
		t.Fatalf("got %+v, want DeviceCfg with BAR=4 offset=0x3000", got)
	}
	if PCICapsByType(caps, PCICapISRCfg) != nil {
		t.Fatalf("expected nil for missing cap type")
	}
}

func TestPCIDeviceIDHelpers(t *testing.T) {
	cases := []struct {
		did                uint16
		isNet              bool
		isBlock            bool
		isEntropy          bool
		isVsock            bool
		isModern, isLegacy bool
	}{
		{0x1000, true, false, false, false, false, true},   // legacy net
		{0x1001, false, true, false, false, false, true},   // legacy block
		{0x1005, false, false, true, false, false, true},   // legacy entropy
		{0x1040, false, false, false, false, true, false},  // modern type=0
		{0x1041, true, false, false, false, true, false},   // modern net
		{0x1042, false, true, false, false, true, false},   // modern block
		{0x1044, false, false, true, false, true, false},   // modern entropy
		{0x1053, false, false, false, true, true, false},   // modern vsock
		{0x107F, false, false, false, false, true, false},  // top of modern
		{0x1080, false, false, false, false, false, false}, // outside both
	}
	for _, c := range cases {
		if got := PCIDeviceIDIsNet(c.did); got != c.isNet {
			t.Errorf("PCIDeviceIDIsNet(0x%04x) = %v, want %v", c.did, got, c.isNet)
		}
		if got := PCIDeviceIDIsBlock(c.did); got != c.isBlock {
			t.Errorf("PCIDeviceIDIsBlock(0x%04x) = %v, want %v", c.did, got, c.isBlock)
		}
		if got := PCIDeviceIDIsEntropy(c.did); got != c.isEntropy {
			t.Errorf("PCIDeviceIDIsEntropy(0x%04x) = %v, want %v", c.did, got, c.isEntropy)
		}
		if got := PCIDeviceIDIsVsock(c.did); got != c.isVsock {
			t.Errorf("PCIDeviceIDIsVsock(0x%04x) = %v, want %v", c.did, got, c.isVsock)
		}
		if got := PCIDeviceIDIsModern(c.did); got != c.isModern {
			t.Errorf("PCIDeviceIDIsModern(0x%04x) = %v, want %v", c.did, got, c.isModern)
		}
		if got := PCIDeviceIDIsLegacy(c.did); got != c.isLegacy {
			t.Errorf("PCIDeviceIDIsLegacy(0x%04x) = %v, want %v", c.did, got, c.isLegacy)
		}
	}
}

func TestVirtioConstants(t *testing.T) {
	if PCIVendorID != 0x1AF4 {
		t.Errorf("PCIVendorID = 0x%04x, want 0x1AF4", PCIVendorID)
	}
	cases := []struct {
		name string
		v    uint8
		want uint8
	}{
		{"CommonCfg", PCICapCommonCfg, 1},
		{"NotifyCfg", PCICapNotifyCfg, 2},
		{"ISRCfg", PCICapISRCfg, 3},
		{"DeviceCfg", PCICapDeviceCfg, 4},
		{"PCICfg", PCICapPCICfg, 5},
		{"SharedMemCfg", PCICapSharedMemCfg, 8},
		{"VendorCfg", PCICapVendorCfg, 9},
	}
	for _, c := range cases {
		if c.v != c.want {
			t.Errorf("PCICap%s = %d, want %d", c.name, c.v, c.want)
		}
	}
	if PCICapIDVendorSpecific != 0x09 {
		t.Errorf("PCICapIDVendorSpecific = 0x%x, want 0x09", PCICapIDVendorSpecific)
	}
	if PCIStatusCapabilityList != 0x10 {
		t.Errorf("PCIStatusCapabilityList = 0x%x, want 0x10", PCIStatusCapabilityList)
	}
}

func TestNotifyOffMultiplierCfgOffset(t *testing.T) {
	if got := NotifyOffMultiplierCfgOffset(0x50); got != 0x60 {
		t.Errorf("got 0x%x, want 0x60", got)
	}
	if got := NotifyOffMultiplierCfgOffset(0xA0); got != 0xB0 {
		t.Errorf("got 0x%x, want 0xB0", got)
	}
}

func TestSentinelErrors(t *testing.T) {
	if ErrCapChainTooLong.Error() == "" {
		t.Error("ErrCapChainTooLong message empty")
	}
	if ErrCapChainBadPtr.Error() == "" {
		t.Error("ErrCapChainBadPtr message empty")
	}
}

func TestDeviceTypes(t *testing.T) {
	if DeviceTypeNet != 1 {
		t.Errorf("DeviceTypeNet = %d, want 1", DeviceTypeNet)
	}
	if DeviceTypeBlock != 2 {
		t.Errorf("DeviceTypeBlock = %d, want 2", DeviceTypeBlock)
	}
	if DeviceTypeConsole != 3 {
		t.Errorf("DeviceTypeConsole = %d, want 3", DeviceTypeConsole)
	}
	if DeviceTypeEntropy != 4 {
		t.Errorf("DeviceTypeEntropy = %d, want 4", DeviceTypeEntropy)
	}
	if DeviceTypeVsock != 19 {
		t.Errorf("DeviceTypeVsock = %d, want 19", DeviceTypeVsock)
	}
	// Modern DID = 0x1040 + device_type (Virtio 1.1 §4.1.2.1).
	if PCIDeviceIDModernEntropy != PCIDeviceIDModernMin+DeviceTypeEntropy {
		t.Errorf("PCIDeviceIDModernEntropy = 0x%04x, want 0x%04x",
			PCIDeviceIDModernEntropy, PCIDeviceIDModernMin+DeviceTypeEntropy)
	}
	if PCIDeviceIDModernVsock != PCIDeviceIDModernMin+DeviceTypeVsock {
		t.Errorf("PCIDeviceIDModernVsock = 0x%04x, want 0x%04x",
			PCIDeviceIDModernVsock, PCIDeviceIDModernMin+DeviceTypeVsock)
	}
}
