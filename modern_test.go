// Tests for modern.go.

package common

import (
	"encoding/binary"
	"errors"
	"testing"
)

// memBAR implements BARMemoryAccessor against a per-(BAR, offset)
// in-memory store. Useful for COMMON_CFG handshake tests where we want
// to simulate the device side write-through plus a few register
// "device-side" behaviours.
type memBAR struct {
	// store keyed by (bar << 32) | offset
	store map[uint64]uint64
	// failReads / failWrites: if non-empty, any read/write at one of
	// the listed offsets fails with a fake error.
	failReads  map[uint64]bool
	failWrites map[uint64]bool
}

func newMemBAR() *memBAR {
	return &memBAR{
		store:      map[uint64]uint64{},
		failReads:  map[uint64]bool{},
		failWrites: map[uint64]bool{},
	}
}

func key(bar uint8, off uint64) uint64 { return uint64(bar)<<48 | off }

func (m *memBAR) Read8(bar uint8, off uint64) (uint8, error) {
	if m.failReads[off] {
		return 0, errors.New("fake: read fail")
	}
	return uint8(m.store[key(bar, off)] & 0xFF), nil
}
func (m *memBAR) Read16(bar uint8, off uint64) (uint16, error) {
	if m.failReads[off] {
		return 0, errors.New("fake: read fail")
	}
	return uint16(m.store[key(bar, off)] & 0xFFFF), nil
}
func (m *memBAR) Read32(bar uint8, off uint64) (uint32, error) {
	if m.failReads[off] {
		return 0, errors.New("fake: read fail")
	}
	return uint32(m.store[key(bar, off)] & 0xFFFFFFFF), nil
}
func (m *memBAR) Read64(bar uint8, off uint64) (uint64, error) {
	if m.failReads[off] {
		return 0, errors.New("fake: read fail")
	}
	return m.store[key(bar, off)], nil
}
func (m *memBAR) Write8(bar uint8, off uint64, v uint8) error {
	if m.failWrites[off] {
		return errors.New("fake: write fail")
	}
	m.store[key(bar, off)] = uint64(v)
	return nil
}
func (m *memBAR) Write16(bar uint8, off uint64, v uint16) error {
	if m.failWrites[off] {
		return errors.New("fake: write fail")
	}
	m.store[key(bar, off)] = uint64(v)
	return nil
}
func (m *memBAR) Write32(bar uint8, off uint64, v uint32) error {
	if m.failWrites[off] {
		return errors.New("fake: write fail")
	}
	m.store[key(bar, off)] = uint64(v)
	return nil
}
func (m *memBAR) Write64(bar uint8, off uint64, v uint64) error {
	if m.failWrites[off] {
		return errors.New("fake: write fail")
	}
	m.store[key(bar, off)] = v
	return nil
}

// memDevice simulates a virtio modern device for an end-to-end
// InitModernConfig test. It exposes:
//
//   - PCIConfigReader, populated with a 256-byte config-space buffer.
//   - BARMemoryAccessor, populated with the device's BAR contents
//     (Read* will return what we pre-loaded; Write* mutates the store).
//   - PageAllocator, returning Go-allocated pages.
//
// Adequate for InitModernConfig + the register accessor handshake.
type memDevice struct {
	cfg []byte
	*memBAR
}

func (d *memDevice) ReadConfig8(off uint8) (uint8, error) {
	if int(off) >= len(d.cfg) {
		return 0, errors.New("fake: read past config-space")
	}
	return d.cfg[off], nil
}
func (d *memDevice) ReadConfig16(off uint8) (uint16, error) {
	if int(off)+2 > len(d.cfg) {
		return 0, errors.New("fake: read past config-space")
	}
	return binary.LittleEndian.Uint16(d.cfg[off : off+2]), nil
}
func (d *memDevice) ReadConfig32(off uint8) (uint32, error) {
	if int(off)+4 > len(d.cfg) {
		return 0, errors.New("fake: read past config-space")
	}
	return binary.LittleEndian.Uint32(d.cfg[off : off+4]), nil
}
func (d *memDevice) AllocatePages(count int) (uint64, []byte, error) {
	mem := make([]byte, count*int(PageSize))
	// physAddr is just the address of the backing array — fine for tests.
	return uint64(0xC0FFEE000), mem, nil
}

func TestCommonCfgRegisterOffsets(t *testing.T) {
	cases := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"DeviceFeatureSelect", CfgDeviceFeatureSelect, 0x00},
		{"DeviceFeature", CfgDeviceFeature, 0x04},
		{"DriverFeatureSelect", CfgDriverFeatureSelect, 0x08},
		{"DriverFeature", CfgDriverFeature, 0x0c},
		{"MsixConfig", CfgMsixConfig, 0x10},
		{"NumQueues", CfgNumQueues, 0x12},
		{"DeviceStatus", CfgDeviceStatus, 0x14},
		{"ConfigGeneration", CfgConfigGeneration, 0x15},
		{"QueueSelect", CfgQueueSelect, 0x16},
		{"QueueSize", CfgQueueSize, 0x18},
		{"QueueMsixVector", CfgQueueMsixVector, 0x1a},
		{"QueueEnable", CfgQueueEnable, 0x1c},
		{"QueueNotifyOff", CfgQueueNotifyOff, 0x1e},
		{"QueueDesc", CfgQueueDesc, 0x20},
		{"QueueDriver", CfgQueueDriver, 0x28},
		{"QueueDevice", CfgQueueDevice, 0x30},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got 0x%x, want 0x%x", c.name, c.got, c.want)
		}
	}
	if CommonCfgSize != 0x38 {
		t.Errorf("CommonCfgSize: got 0x%x, want 0x38", CommonCfgSize)
	}
}

func TestStatusBits(t *testing.T) {
	if StatusAcknowledge != 0x01 {
		t.Errorf("ACK = 0x%x, want 0x01", StatusAcknowledge)
	}
	if StatusDriver != 0x02 {
		t.Errorf("DRIVER = 0x%x, want 0x02", StatusDriver)
	}
	if StatusDriverOK != 0x04 {
		t.Errorf("DRIVER_OK = 0x%x, want 0x04", StatusDriverOK)
	}
	if StatusFeaturesOK != 0x08 {
		t.Errorf("FEATURES_OK = 0x%x, want 0x08", StatusFeaturesOK)
	}
	if StatusNeedsReset != 0x40 {
		t.Errorf("NEEDS_RESET = 0x%x, want 0x40", StatusNeedsReset)
	}
	if StatusFailed != 0x80 {
		t.Errorf("FAILED = 0x%x, want 0x80", StatusFailed)
	}
}

func TestFeatureVersion1Bit(t *testing.T) {
	if FeatureVersion1 != (1 << 32) {
		t.Errorf("FeatureVersion1 = 0x%x, want 1<<32", FeatureVersion1)
	}
	if FeatureRingPacked != (1 << 34) {
		t.Errorf("FeatureRingPacked = 0x%x, want 1<<34", FeatureRingPacked)
	}
}

func TestParseCaps_Happy(t *testing.T) {
	caps := []PCICap{
		{CapID: PCICapIDVendorSpecific, CfgType: PCICapCommonCfg, BAR: 4, Offset: 0, Length: 56, CfgSpaceOffset: 0x40},
		{CapID: PCICapIDVendorSpecific, CfgType: PCICapNotifyCfg, BAR: 4, Offset: 0x1000, Length: 0x1000, CfgSpaceOffset: 0x50, Len: 20},
		{CapID: PCICapIDVendorSpecific, CfgType: PCICapISRCfg, BAR: 4, Offset: 0x2000, Length: 0x1000, CfgSpaceOffset: 0x60},
		{CapID: PCICapIDVendorSpecific, CfgType: PCICapDeviceCfg, BAR: 4, Offset: 0x3000, Length: 12, CfgSpaceOffset: 0x70},
		{CapID: PCICapIDVendorSpecific, CfgType: PCICapPCICfg, BAR: 0, Offset: 0, Length: 0, CfgSpaceOffset: 0x80},
	}
	bar := newMemBAR()
	cfg, err := ParseCaps(caps, bar)
	if err != nil {
		t.Fatalf("ParseCaps: %v", err)
	}
	if cfg.CommonCfgBAR != 4 || cfg.CommonCfgOffset != 0 {
		t.Errorf("CommonCfg: got (%d, 0x%x), want (4, 0)", cfg.CommonCfgBAR, cfg.CommonCfgOffset)
	}
	if !cfg.HasNotifyCfg() {
		t.Errorf("HasNotifyCfg = false, want true")
	}
	if !cfg.HasDeviceCfg() {
		t.Errorf("HasDeviceCfg = false, want true")
	}
	if cfg.DeviceCfgLength != 12 {
		t.Errorf("DeviceCfgLength: got %d, want 12", cfg.DeviceCfgLength)
	}
}

func TestParseCaps_VZShape_ShortDeviceCfg(t *testing.T) {
	// Apple VZ ships DEVICE_CFG with length=17; ParseCaps stores it
	// faithfully.
	caps := []PCICap{
		{CfgType: PCICapCommonCfg, BAR: 0, Offset: 0x40, Length: 56},
		{CfgType: PCICapNotifyCfg, BAR: 0, Offset: 0x80, Length: 0x100},
		{CfgType: PCICapDeviceCfg, BAR: 0, Offset: 0x8000, Length: 17},
	}
	cfg, err := ParseCaps(caps, newMemBAR())
	if err != nil {
		t.Fatalf("ParseCaps: %v", err)
	}
	if cfg.DeviceCfgLength != 17 {
		t.Errorf("DeviceCfgLength: got %d, want 17", cfg.DeviceCfgLength)
	}
}

func TestParseCaps_NoCommonCfg(t *testing.T) {
	caps := []PCICap{{CfgType: PCICapNotifyCfg, BAR: 0, Offset: 0x80, Length: 0x100}}
	_, err := ParseCaps(caps, newMemBAR())
	if !errors.Is(err, ErrNoCommonCfg) {
		t.Errorf("got %v, want ErrNoCommonCfg", err)
	}
}

func TestParseCaps_CommonCfgAtZero(t *testing.T) {
	// Edge: a CommonCfg legitimately placed at BAR=0, offset=0. The
	// "unset pattern" detection must not confuse this for "no CommonCfg".
	caps := []PCICap{
		{CfgType: PCICapCommonCfg, BAR: 0, Offset: 0, Length: 56},
		{CfgType: PCICapNotifyCfg, BAR: 0, Offset: 0x80, Length: 0x100},
	}
	cfg, err := ParseCaps(caps, newMemBAR())
	if err != nil {
		t.Fatalf("ParseCaps: %v", err)
	}
	if cfg.CommonCfgBAR != 0 || cfg.CommonCfgOffset != 0 {
		t.Errorf("CommonCfg at zero: got (%d, 0x%x)", cfg.CommonCfgBAR, cfg.CommonCfgOffset)
	}
}

func TestParseCaps_CommonCfgTooShort(t *testing.T) {
	caps := []PCICap{
		{CfgType: PCICapCommonCfg, BAR: 0, Offset: 0x40, Length: 32},
		{CfgType: PCICapNotifyCfg, BAR: 0, Offset: 0x80, Length: 0x100},
	}
	_, err := ParseCaps(caps, newMemBAR())
	if !errors.Is(err, ErrCommonCfgTooShort) {
		t.Errorf("got %v, want ErrCommonCfgTooShort", err)
	}
}

func TestParseCaps_NoNotifyCfg(t *testing.T) {
	caps := []PCICap{
		{CfgType: PCICapCommonCfg, BAR: 0, Offset: 0x40, Length: 56},
		{CfgType: PCICapDeviceCfg, BAR: 0, Offset: 0x8000, Length: 12},
	}
	_, err := ParseCaps(caps, newMemBAR())
	if !errors.Is(err, ErrNoNotifyCfg) {
		t.Errorf("got %v, want ErrNoNotifyCfg", err)
	}
}

func TestPerQueueNotifyOffset(t *testing.T) {
	cfg := &ModernConfig{NotifyCfgOffset: 0x1000, NotifyOffMultiplier: 4}
	if got := cfg.PerQueueNotifyOffset(0); got != 0x1000 {
		t.Errorf("queue 0: got 0x%x, want 0x1000", got)
	}
	if got := cfg.PerQueueNotifyOffset(1); got != 0x1004 {
		t.Errorf("queue 1: got 0x%x, want 0x1004", got)
	}
	if got := cfg.PerQueueNotifyOffset(10); got != 0x1028 {
		t.Errorf("queue 10: got 0x%x, want 0x1028", got)
	}
	cfg.NotifyOffMultiplier = 0
	if got := cfg.PerQueueNotifyOffset(5); got != 0x1000 {
		t.Errorf("multiplier=0 queue 5: got 0x%x, want 0x1000", got)
	}
}

func TestModernConfig_HasFlags(t *testing.T) {
	cfg := &ModernConfig{}
	if cfg.HasNotifyCfg() {
		t.Errorf("default cfg: HasNotifyCfg = true, want false")
	}
	if cfg.HasDeviceCfg() {
		t.Errorf("default cfg: HasDeviceCfg = true, want false")
	}
	cfg.NotifyCfgLength = 0x10
	cfg.DeviceCfgLength = 0x10
	if !cfg.HasNotifyCfg() {
		t.Errorf("after Length=0x10: HasNotifyCfg = false, want true")
	}
	if !cfg.HasDeviceCfg() {
		t.Errorf("after Length=0x10: HasDeviceCfg = false, want true")
	}
}

// TestModernConfig_RegisterAccessors round-trips every COMMON_CFG
// register via the typed accessors. The memBAR mock stores the last
// written value; reads come back the same.
func TestModernConfig_RegisterAccessors(t *testing.T) {
	bar := newMemBAR()
	cfg := &ModernConfig{BAR: bar, CommonCfgBAR: 4, CommonCfgOffset: 0x100}

	// 8-bit registers: DeviceStatus + ConfigGeneration.
	if err := cfg.SetDeviceStatus(StatusAcknowledge | StatusDriver); err != nil {
		t.Fatalf("SetDeviceStatus: %v", err)
	}
	if got, _ := cfg.DeviceStatus(); got != (StatusAcknowledge | StatusDriver) {
		t.Errorf("DeviceStatus: got 0x%x, want 0x%x", got, StatusAcknowledge|StatusDriver)
	}
	bar.store[key(4, 0x100+CfgConfigGeneration)] = 0x5
	if got, _ := cfg.ConfigGeneration(); got != 0x5 {
		t.Errorf("ConfigGeneration: got %d, want 5", got)
	}

	// 16-bit registers: NumQueues, QueueSize, QueueNotifyOff, QueueSelect, QueueEnable.
	bar.store[key(4, 0x100+CfgNumQueues)] = 3
	if got, _ := cfg.NumQueues(); got != 3 {
		t.Errorf("NumQueues: got %d, want 3", got)
	}
	if err := cfg.SetQueueSize(64); err != nil {
		t.Fatalf("SetQueueSize: %v", err)
	}
	if got, _ := cfg.QueueSize(); got != 64 {
		t.Errorf("QueueSize: got %d, want 64", got)
	}
	bar.store[key(4, 0x100+CfgQueueNotifyOff)] = 7
	if got, _ := cfg.QueueNotifyOff(); got != 7 {
		t.Errorf("QueueNotifyOff: got %d, want 7", got)
	}
	if err := cfg.SelectQueue(1); err != nil {
		t.Fatalf("SelectQueue: %v", err)
	}
	if err := cfg.SetQueueEnable(1); err != nil {
		t.Fatalf("SetQueueEnable: %v", err)
	}
	if got, _ := cfg.QueueEnable(); got != 1 {
		t.Errorf("QueueEnable: got %d, want 1", got)
	}

	// 64-bit queue address registers.
	if err := cfg.SetQueueDesc(0x1234567800001000); err != nil {
		t.Fatalf("SetQueueDesc: %v", err)
	}
	if got, _ := cfg.QueueDesc(); got != 0x1234567800001000 {
		t.Errorf("QueueDesc: got 0x%x", got)
	}
	if err := cfg.SetQueueDriver(0x1234567800002000); err != nil {
		t.Fatalf("SetQueueDriver: %v", err)
	}
	if got, _ := cfg.QueueDriver(); got != 0x1234567800002000 {
		t.Errorf("QueueDriver: got 0x%x", got)
	}
	if err := cfg.SetQueueDevice(0x1234567800003000); err != nil {
		t.Fatalf("SetQueueDevice: %v", err)
	}
	if got, _ := cfg.QueueDevice(); got != 0x1234567800003000 {
		t.Errorf("QueueDevice: got 0x%x", got)
	}
}

func TestModernConfig_DeviceFeatures64(t *testing.T) {
	bar := newMemBAR()
	cfg := &ModernConfig{BAR: bar, CommonCfgBAR: 0, CommonCfgOffset: 0}
	// Simulate the device: when select=0 → return lo; when select=1 → return hi.
	// Our memBAR is naive (one-store-per-offset) so we instead pre-populate
	// the DeviceFeature offset with both halves by checking selects.
	// Simpler: use a custom BAR.
	df := &featureBAR{store: map[uint64]uint64{}, lo: 0xCAFEBABE, hi: 0x5}
	cfg.BAR = df
	feats, err := cfg.DeviceFeatures64()
	if err != nil {
		t.Fatalf("DeviceFeatures64: %v", err)
	}
	want := uint64(0xCAFEBABE) | uint64(0x5)<<32
	if feats != want {
		t.Errorf("DeviceFeatures64: got 0x%x, want 0x%x", feats, want)
	}
}

func TestModernConfig_SetDriverFeatures64(t *testing.T) {
	bar := newMemBAR()
	cfg := &ModernConfig{BAR: bar, CommonCfgBAR: 0, CommonCfgOffset: 0}
	if err := cfg.SetDriverFeatures64(0xCAFEBABE00000005); err != nil {
		t.Fatalf("SetDriverFeatures64: %v", err)
	}
	// Last DriverFeatureSelect write was for the high half (1); last DriverFeature
	// write was 0xCAFEBABE (high 32 bits).
	if got := bar.store[key(0, CfgDriverFeatureSelect)]; got != 1 {
		t.Errorf("DriverFeatureSelect last write: got %d, want 1", got)
	}
	if got := bar.store[key(0, CfgDriverFeature)]; got != 0xCAFEBABE {
		t.Errorf("DriverFeature last write: got 0x%x, want 0xCAFEBABE", got)
	}
}

// featureBAR is a BARMemoryAccessor whose DeviceFeature read depends on
// the last DeviceFeatureSelect write — mirrors the real device's
// behaviour.
type featureBAR struct {
	store    map[uint64]uint64
	lo, hi   uint32
	lastSel  uint32
}

func (m *featureBAR) Read8(bar uint8, off uint64) (uint8, error) {
	return uint8(m.store[key(bar, off)]), nil
}
func (m *featureBAR) Read16(bar uint8, off uint64) (uint16, error) {
	return uint16(m.store[key(bar, off)]), nil
}
func (m *featureBAR) Read32(bar uint8, off uint64) (uint32, error) {
	if off == CfgDeviceFeature {
		if m.lastSel == 0 {
			return m.lo, nil
		}
		return m.hi, nil
	}
	return uint32(m.store[key(bar, off)]), nil
}
func (m *featureBAR) Read64(bar uint8, off uint64) (uint64, error) {
	return m.store[key(bar, off)], nil
}
func (m *featureBAR) Write8(bar uint8, off uint64, v uint8) error {
	m.store[key(bar, off)] = uint64(v)
	return nil
}
func (m *featureBAR) Write16(bar uint8, off uint64, v uint16) error {
	m.store[key(bar, off)] = uint64(v)
	return nil
}
func (m *featureBAR) Write32(bar uint8, off uint64, v uint32) error {
	if off == CfgDeviceFeatureSelect {
		m.lastSel = v
	}
	m.store[key(bar, off)] = uint64(v)
	return nil
}
func (m *featureBAR) Write64(bar uint8, off uint64, v uint64) error {
	m.store[key(bar, off)] = v
	return nil
}

// TestModernConfig_NotifyQueue exercises the doorbell-width selection
// rule: multiplier >= 4 → uint32 write; multiplier < 4 → uint16 write.
func TestModernConfig_NotifyQueue(t *testing.T) {
	bar := newMemBAR()
	cfg := &ModernConfig{
		BAR:                 bar,
		NotifyCfgBAR:        4,
		NotifyCfgOffset:     0x1000,
		NotifyOffMultiplier: 4,
	}
	if err := cfg.NotifyQueue(1, 1); err != nil {
		t.Fatalf("NotifyQueue: %v", err)
	}
	// PerQueueNotifyOffset(1) = 0x1000 + 1*4 = 0x1004
	if got := bar.store[key(4, 0x1004)]; got != 1 {
		t.Errorf("NotifyQueue multiplier=4: got 0x%x, want 1", got)
	}
	// Smaller multiplier → uint16 width path.
	cfg.NotifyOffMultiplier = 2
	if err := cfg.NotifyQueue(0, 0); err != nil {
		t.Fatalf("NotifyQueue (multiplier=2): %v", err)
	}
}

func TestModernConfig_DeviceCfgRead(t *testing.T) {
	bar := newMemBAR()
	cfg := &ModernConfig{
		BAR:             bar,
		DeviceCfgBAR:    4,
		DeviceCfgOffset: 0x3000,
		DeviceCfgLength: 12,
	}
	// Plant a virtio-net MAC at offset 0.
	bar.store[key(4, 0x3000)] = 0x52
	bar.store[key(4, 0x3001)] = 0x55
	got0, _ := cfg.DeviceCfgRead8(0)
	got1, _ := cfg.DeviceCfgRead8(1)
	if got0 != 0x52 || got1 != 0x55 {
		t.Errorf("DeviceCfgRead8(0,1): got 0x%x 0x%x", got0, got1)
	}
	// Out-of-bounds.
	if _, err := cfg.DeviceCfgRead8(12); !errors.Is(err, ErrDeviceCfgOutOfBounds) {
		t.Errorf("DeviceCfgRead8(12) on length=12: got %v, want ErrDeviceCfgOutOfBounds", err)
	}
	// DeviceCfg not present.
	cfg2 := &ModernConfig{BAR: bar}
	if _, err := cfg2.DeviceCfgRead8(0); !errors.Is(err, ErrNoDeviceCfg) {
		t.Errorf("DeviceCfgRead8 no DeviceCfg: got %v, want ErrNoDeviceCfg", err)
	}
}

func TestModernConfig_DeviceCfgRead16_32_64(t *testing.T) {
	bar := newMemBAR()
	cfg := &ModernConfig{
		BAR:             bar,
		DeviceCfgBAR:    0,
		DeviceCfgOffset: 0,
		DeviceCfgLength: 16,
	}
	bar.store[key(0, 0)] = 0x1234
	if got, _ := cfg.DeviceCfgRead16(0); got != 0x1234 {
		t.Errorf("DeviceCfgRead16: got 0x%x", got)
	}
	if got, _ := cfg.DeviceCfgRead32(0); got != 0x1234 {
		t.Errorf("DeviceCfgRead32: got 0x%x", got)
	}
	if got, _ := cfg.DeviceCfgRead64(0); got != 0x1234 {
		t.Errorf("DeviceCfgRead64: got 0x%x", got)
	}
	// Out-of-bounds.
	if _, err := cfg.DeviceCfgRead16(15); !errors.Is(err, ErrDeviceCfgOutOfBounds) {
		t.Errorf("DeviceCfgRead16(15) on length=16: got %v", err)
	}
	if _, err := cfg.DeviceCfgRead32(13); !errors.Is(err, ErrDeviceCfgOutOfBounds) {
		t.Errorf("DeviceCfgRead32(13) on length=16: got %v", err)
	}
	if _, err := cfg.DeviceCfgRead64(9); !errors.Is(err, ErrDeviceCfgOutOfBounds) {
		t.Errorf("DeviceCfgRead64(9) on length=16: got %v", err)
	}
	// No DeviceCfg.
	empty := &ModernConfig{BAR: bar}
	if _, err := empty.DeviceCfgRead16(0); !errors.Is(err, ErrNoDeviceCfg) {
		t.Errorf("DeviceCfgRead16 no DeviceCfg: got %v", err)
	}
	if _, err := empty.DeviceCfgRead32(0); !errors.Is(err, ErrNoDeviceCfg) {
		t.Errorf("DeviceCfgRead32 no DeviceCfg: got %v", err)
	}
	if _, err := empty.DeviceCfgRead64(0); !errors.Is(err, ErrNoDeviceCfg) {
		t.Errorf("DeviceCfgRead64 no DeviceCfg: got %v", err)
	}
}

func TestInitModernConfig_EndToEnd(t *testing.T) {
	// Build a config-space buffer with three caps placed at known
	// offsets. Layout:
	//   0x40: CommonCfg  (16 bytes)        next=0x50
	//   0x50: NotifyCfg  (20 bytes ext.)   next=0x68
	//                    [+16..+20] = uint32 multiplier
	//   0x68: DeviceCfg  (16 bytes)        next=0x00
	cfg := make([]byte, 256)
	binary.LittleEndian.PutUint16(cfg[0x00:0x02], PCIVendorID)
	binary.LittleEndian.PutUint16(cfg[0x02:0x04], PCIDeviceIDModernNet)
	binary.LittleEndian.PutUint16(cfg[0x06:0x08], PCIStatusCapabilityList)
	cfg[0x34] = 0x40
	// CommonCfg at 0x40.
	common := virtioCapBytes(0x50, 16, PCICapCommonCfg, 0, 0, 0x0, 0x38)
	copy(cfg[0x40:], common[:])
	// NotifyCfg at 0x50, 20 bytes (extended).
	notify := virtioCapBytes(0x68, 20, PCICapNotifyCfg, 0, 0, 0x1000, 0x1000)
	copy(cfg[0x50:], notify[:])
	// Multiplier = 4 at cfg[0x60].
	binary.LittleEndian.PutUint32(cfg[0x60:0x64], 4)
	// DeviceCfg at 0x68.
	devCap := virtioCapBytes(0x00, 16, PCICapDeviceCfg, 0, 0, 0x3000, 17)
	copy(cfg[0x68:], devCap[:])

	bar := newMemBAR()
	dev := &memDevice{cfg: cfg, memBAR: bar}

	mc, err := InitModernConfig(dev)
	if err != nil {
		t.Fatalf("InitModernConfig: %v", err)
	}
	if mc.NotifyOffMultiplier != 4 {
		t.Errorf("NotifyOffMultiplier: got %d, want 4", mc.NotifyOffMultiplier)
	}
	if mc.DeviceCfgLength != 17 {
		t.Errorf("DeviceCfgLength: got %d, want 17", mc.DeviceCfgLength)
	}
	if !mc.HasNotifyCfg() || !mc.HasDeviceCfg() {
		t.Errorf("expected NotifyCfg + DeviceCfg both present")
	}
}

func TestInitModernConfig_NoCapListBit(t *testing.T) {
	cfg := make([]byte, 256)
	// Status bit unset.
	dev := &memDevice{cfg: cfg, memBAR: newMemBAR()}
	_, err := InitModernConfig(dev)
	if !errors.Is(err, ErrCapListBitUnset) {
		t.Errorf("got %v, want ErrCapListBitUnset", err)
	}
}

func TestInitModernConfig_LegacyNotifyCap(t *testing.T) {
	// Notify cap with cap_len = 16 (no extended multiplier) — must
	// fall back to multiplier = 0.
	var caps []byte
	at := func(b [16]byte) { caps = append(caps, b[:]...) }
	at(virtioCapBytes(0x50, 16, PCICapCommonCfg, 0, 0, 0, 56))
	at(virtioCapBytes(0, 16 /*not 20*/, PCICapNotifyCfg, 0, 0, 0x1000, 0x1000))
	cfg := synthConfigSpace(t, PCIVendorID, PCIDeviceIDModernNet, PCIStatusCapabilityList, 0x40, caps)
	dev := &memDevice{cfg: cfg, memBAR: newMemBAR()}
	mc, err := InitModernConfig(dev)
	if err != nil {
		t.Fatalf("InitModernConfig: %v", err)
	}
	if mc.NotifyOffMultiplier != 0 {
		t.Errorf("legacy notify cap (len=16): NotifyOffMultiplier got %d, want 0", mc.NotifyOffMultiplier)
	}
}
