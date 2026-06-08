// go-virtio/common — Virtio modern (1.0+) PCI transport: config layout
// + typed register accessors.
//
// Transport-agnostic: every register access routes through a
// BARMemoryAccessor (see transport.go), so the same `ModernConfig` type
// drives a UEFI-backed device (EFI_PCI_IO_PROTOCOL.Mem.Read/Write) and
// a bare-metal device (direct MMIO at the BAR's physical base).
//
// References (cited at every layout/register decision below):
//
//   - Virtio 1.1 §4.1.4   "Virtio Structure PCI Capabilities" — the
//     `struct virtio_pci_cap` body that `WalkPCICaps` (pci.go) parses.
//   - Virtio 1.1 §4.1.5   "PCI-specific Initialization And Device
//     Operation" — the COMMON_CFG / NOTIFY_CFG / ISR_CFG / DEVICE_CFG
//     register layouts.
//   - Virtio 1.1 §4.1.5.1 "Common configuration structure layout":
//     the table of offsets encoded below as `Cfg*` constants.
//   - Virtio 1.1 §4.1.5.2 "Notification capability": the per-queue
//     notification address is
//         cap.offset + queue_notify_off * notify_off_multiplier.
//   - Linux drivers/virtio/virtio_pci_modern.c — canonical
//     Go-translatable reference; we follow its struct shape.

package common

// COMMON_CFG register offsets (Virtio 1.1 §4.1.5.1, table 4.1.5.1.1):
//
//	0x00  le32  device_feature_select
//	0x04  le32  device_feature        (read)
//	0x08  le32  driver_feature_select
//	0x0c  le32  driver_feature        (write)
//	0x10  le16  msix_config
//	0x12  le16  num_queues            (read)
//	0x14  u8    device_status
//	0x15  u8    config_generation     (read)
//	0x16  le16  queue_select
//	0x18  le16  queue_size
//	0x1a  le16  queue_msix_vector
//	0x1c  le16  queue_enable
//	0x1e  le16  queue_notify_off      (read)
//	0x20  le64  queue_desc
//	0x28  le64  queue_driver
//	0x30  le64  queue_device
//
// Total 56 bytes.
const (
	CfgDeviceFeatureSelect uint64 = 0x00
	CfgDeviceFeature       uint64 = 0x04
	CfgDriverFeatureSelect uint64 = 0x08
	CfgDriverFeature       uint64 = 0x0c
	CfgMsixConfig          uint64 = 0x10
	CfgNumQueues           uint64 = 0x12
	CfgDeviceStatus        uint64 = 0x14
	CfgConfigGeneration    uint64 = 0x15
	CfgQueueSelect         uint64 = 0x16
	CfgQueueSize           uint64 = 0x18
	CfgQueueMsixVector     uint64 = 0x1a
	CfgQueueEnable         uint64 = 0x1c
	CfgQueueNotifyOff      uint64 = 0x1e
	CfgQueueDesc           uint64 = 0x20
	CfgQueueDriver         uint64 = 0x28
	CfgQueueDevice         uint64 = 0x30
)

// CommonCfgSize is the minimum byte-length of a valid
// VIRTIO_PCI_CAP_COMMON_CFG region (56 bytes = 0x38). QEMU+EDK2 and
// Apple VZ both publish this value or larger; anything smaller is a
// firmware bug.
const CommonCfgSize uint32 = 0x38

// DeviceStatus bits (Virtio 1.1 §2.1). The init sequence drives
// these through SetDeviceStatus:
//
//	1. Write 0 to DeviceStatus (full reset).
//	2. Set ACKNOWLEDGE.
//	3. Set DRIVER.
//	4. Read DeviceFeature, mask down, write DriverFeature.
//	5. Set FEATURES_OK, re-read DeviceStatus, confirm FEATURES_OK.
//	6. Set up per-class queues.
//	7. Set DRIVER_OK.
//
// Failure mode: FAILED bit set (firmware/device rejected our config).
const (
	StatusAcknowledge uint8 = 0x01
	StatusDriver      uint8 = 0x02
	StatusDriverOK    uint8 = 0x04
	StatusFeaturesOK  uint8 = 0x08
	StatusNeedsReset  uint8 = 0x40
	StatusFailed      uint8 = 0x80
)

// Transport-level Virtio reserved feature bits (Virtio 1.1 §6). The
// device-class drivers (go-virtio/net, …) own the per-class bits; the
// transport-level bits live here.
const (
	// FeatureVersion1 = bit 32. Non-negotiable for this package — the
	// entire modern transport layout depends on it.
	FeatureVersion1 uint64 = 1 << 32

	// FeatureRingPacked = bit 34. The driver-side state machine in
	// virtqueue.go implements split-ring only, so this package's
	// drivers MUST NOT acknowledge this bit. Constant exposed for
	// diagnostic narrow probes that want to verify a device offers it.
	FeatureRingPacked uint64 = 1 << 34
)

// ModernConfig is the parsed, pre-located handle for one virtio modern
// device. It pins the (BAR, offset, length) tuples for each capability
// so the per-register accessors don't have to re-walk the cap list,
// and it carries a `BARMemoryAccessor` for the actual MMIO routing.
//
// `NotifyOffMultiplier` is read from the NOTIFY_CFG capability's
// trailing 4-byte field (Virtio 1.1 §4.1.4.4). The per-queue
// notification address is
//
//	notify_cap.offset + queue_notify_off * notify_off_multiplier
//
// where `queue_notify_off` comes from COMMON_CFG.QueueNotifyOff after
// QueueSelect.
type ModernConfig struct {
	// BAR is the BARMemoryAccessor that backs every COMMON_CFG /
	// NOTIFY_CFG / ISR_CFG / DEVICE_CFG read/write. Held by value as
	// an interface; the concrete implementation lives in the host
	// transport adapter.
	BAR BARMemoryAccessor

	// CommonCfg is the BAR + offset of the VIRTIO_PCI_CAP_COMMON_CFG
	// region. Length is guaranteed >= 56 by the spec; we don't store
	// it since every COMMON_CFG access is at a fixed offset below 56.
	CommonCfgBAR    uint8
	CommonCfgOffset uint64

	// NotifyCfg is the BAR + offset of the VIRTIO_PCI_CAP_NOTIFY_CFG
	// region. NotifyOffMultiplier is the per-device multiplier read
	// from the extended cap (Virtio 1.1 §4.1.4.4).
	NotifyCfgBAR        uint8
	NotifyCfgOffset     uint64
	NotifyCfgLength     uint32
	NotifyOffMultiplier uint32

	// ISRCfg is the BAR + offset of the VIRTIO_PCI_CAP_ISR_CFG region.
	// 1 byte, holding the interrupt-status bits the device publishes
	// for polling (Virtio 1.1 §4.1.5.3).
	ISRCfgBAR    uint8
	ISRCfgOffset uint64

	// DeviceCfg is the BAR + offset of the VIRTIO_PCI_CAP_DEVICE_CFG
	// region. Length is stored because some firmware (notably Apple VZ
	// on virtio-net) publishes shorter device-cfg regions than QEMU+EDK2;
	// the read accessors enforce a bounds-check against this value.
	DeviceCfgBAR    uint8
	DeviceCfgOffset uint64
	DeviceCfgLength uint32

	// PCICfg is the BAR + offset of the VIRTIO_PCI_CAP_PCI_CFG region
	// (alternate cfg-access window). Optional; on devices that don't
	// publish it, this field is zero.
	PCICfgBAR    uint8
	PCICfgOffset uint64
}

// HasNotifyCfg reports whether NOTIFY_CFG was located (length > 0).
// An init sequence MUST have it; ParseCaps surfaces the absence early.
func (c *ModernConfig) HasNotifyCfg() bool { return c.NotifyCfgLength > 0 }

// HasDeviceCfg reports whether DEVICE_CFG was located.
func (c *ModernConfig) HasDeviceCfg() bool { return c.DeviceCfgLength > 0 }

// PerQueueNotifyOffset returns the BAR-relative offset the virtqueue's
// `notify` write must hit, given the device's `queue_notify_off` value
// (read from COMMON_CFG after QueueSelect). Per Virtio 1.1 §4.1.4.4:
//
//	addr = notify_cap.offset + queue_notify_off * notify_off_multiplier
//
// Returned offset is BAR-relative; the caller turns it into an MMIO
// write via BAR.Write16/Write32 at (NotifyCfgBAR, returned offset).
func (c *ModernConfig) PerQueueNotifyOffset(queueNotifyOff uint16) uint64 {
	return c.NotifyCfgOffset + uint64(queueNotifyOff)*uint64(c.NotifyOffMultiplier)
}

// ParseCaps converts the raw capability list (output of WalkPCICaps)
// into a populated `*ModernConfig` by locating each required
// capability and pinning its BAR + offset. The `BAR` accessor is
// caller-supplied (typically the host transport adapter).
//
// `NotifyOffMultiplier` is NOT filled here — that requires reading 4
// bytes from PCI config space at notify_cap.CfgSpaceOffset+16, which
// is a `PCIConfigReader.ReadConfig32` call; use
// `InitModernConfig(transport)` for the full bring-up.
//
// Errors:
//
//   - ErrNoCommonCfg: required COMMON_CFG capability missing.
//   - ErrCommonCfgTooShort: COMMON_CFG length < 56.
//   - ErrNoNotifyCfg: required NOTIFY_CFG capability missing.
//
// Optional caps (ISRCfg, DeviceCfg, PCICfg) leave the corresponding
// fields zero if absent; callers check HasDeviceCfg() before reading
// device-specific config.
func ParseCaps(caps []PCICap, bar BARMemoryAccessor) (*ModernConfig, error) {
	cfg := &ModernConfig{BAR: bar}
	for i := range caps {
		c := &caps[i]
		switch c.CfgType {
		case PCICapCommonCfg:
			if c.Length < CommonCfgSize {
				return nil, ErrCommonCfgTooShort
			}
			cfg.CommonCfgBAR = c.BAR
			cfg.CommonCfgOffset = uint64(c.Offset)
		case PCICapNotifyCfg:
			cfg.NotifyCfgBAR = c.BAR
			cfg.NotifyCfgOffset = uint64(c.Offset)
			cfg.NotifyCfgLength = c.Length
		case PCICapISRCfg:
			cfg.ISRCfgBAR = c.BAR
			cfg.ISRCfgOffset = uint64(c.Offset)
		case PCICapDeviceCfg:
			cfg.DeviceCfgBAR = c.BAR
			cfg.DeviceCfgOffset = uint64(c.Offset)
			cfg.DeviceCfgLength = c.Length
		case PCICapPCICfg:
			cfg.PCICfgBAR = c.BAR
			cfg.PCICfgOffset = uint64(c.Offset)
		}
	}
	if cfg.CommonCfgOffset == 0 && cfg.CommonCfgBAR == 0 {
		// Defensive: BAR=0 + offset=0 could legitimately be a CommonCfg
		// cap placed at the start of BAR 0 (the modern transport
		// doesn't forbid it). Walk the cap list a second time to
		// resolve the ambiguity.
		hasCommon := false
		for _, c := range caps {
			if c.CfgType == PCICapCommonCfg {
				hasCommon = true
				break
			}
		}
		if !hasCommon {
			return nil, ErrNoCommonCfg
		}
	}
	if cfg.NotifyCfgLength == 0 {
		return nil, ErrNoNotifyCfg
	}
	return cfg, nil
}

// InitModernConfig drives the full modern-transport setup for one
// virtio device using a `Transport`:
//
//  1. Read PCI Status and confirm bit 4 (CapabilityList) is set.
//  2. Read the CapabilitiesPtr at config offset 0x34.
//  3. Walk the cap list via `WalkPCICaps` against the transport.
//  4. Pass the resulting `[]PCICap` to `ParseCaps` along with the
//     transport's BAR accessor.
//  5. If NOTIFY_CFG was found, read the 4-byte
//     `notify_off_multiplier` from cfg-space at NotifyCfg's
//     CfgSpaceOffset + 16.
//
// Returns the populated `*ModernConfig` or a wrapped error.
//
// Errors propagated:
//
//   - any error from the transport's ReadConfig* / Read* calls.
//   - the cap-walker's `ErrCapChainTooLong` / `ErrCapChainBadPtr`.
//   - `ErrNoCommonCfg`, `ErrNoNotifyCfg`, `ErrCommonCfgTooShort`.
//   - `ErrCapListBitUnset` — the device's PCI Status[CapList] bit is 0,
//     indicating a pre-1.0 / I/O-port-only legacy device that the
//     modern transport cannot drive.
func InitModernConfig(t Transport) (*ModernConfig, error) {
	status, err := t.ReadConfig16(PCICfgStatus)
	if err != nil {
		return nil, err
	}
	if status&PCIStatusCapabilityList == 0 {
		return nil, ErrCapListBitUnset
	}
	capPtr, err := t.ReadConfig8(PCICfgCapabilitiesPtr)
	if err != nil {
		return nil, err
	}
	caps, walkErr := WalkPCICaps(t, capPtr)
	if walkErr != nil && len(caps) == 0 {
		// Hard fail only if we got nothing — partial chains still
		// usable (the standard COMMON_CFG / NOTIFY_CFG / DEVICE_CFG
		// live early in the list on every host we've measured).
		return nil, walkErr
	}
	cfg, err := ParseCaps(caps, t)
	if err != nil {
		return nil, err
	}
	// Read the extended notify_off_multiplier from PCI config space at
	// NotifyCfg.CfgSpaceOffset + 16.
	for i := range caps {
		c := &caps[i]
		if c.CfgType != PCICapNotifyCfg {
			continue
		}
		// Sanity check the cap_len: extended notify cap is 20 bytes per
		// Virtio 1.1 §4.1.4.4 (16-byte header + 4-byte multiplier). A
		// 16-byte NOTIFY_CFG with no multiplier is legacy-style; treat
		// it as multiplier=0 (every queue notifies at the same offset).
		if c.Len < PCICapHeaderSize+PCICapNotifyExtraSize {
			cfg.NotifyOffMultiplier = 0
			break
		}
		// ReadConfig32 takes a uint8 offset; the multiplier offset
		// in cfg-space is at most 0xFF + 16 = 0x10F bytes from the
		// start, but PCI config-space addressing is byte-granular
		// within the 256-byte window. The cap header sits in the
		// 0x40..0xFF area, so multiplier offset is in 0x50..0x10F
		// — we narrow to an 8-bit value by relying on
		// CfgSpaceOffset+16 <= 255 (cap-list pointers can't extend
		// past 0xFF in standard config-space).
		multOffset := NotifyOffMultiplierCfgOffset(c.CfgSpaceOffset)
		if multOffset > 0xFF {
			// Spec-violating cap placement; skip the multiplier
			// read and fall back to 0.
			cfg.NotifyOffMultiplier = 0
			break
		}
		mult, err := t.ReadConfig32(uint8(multOffset))
		if err != nil {
			return nil, err
		}
		cfg.NotifyOffMultiplier = mult
		break
	}
	return cfg, nil
}

// --- COMMON_CFG register accessors -------------------------------------
//
// All COMMON_CFG accesses route through the embedded `BARMemoryAccessor`
// against (CommonCfgBAR, CommonCfgOffset + reg_offset). The per-register
// offsets come from the Cfg* constants above.

// ReadDeviceFeatureSelect / WriteDeviceFeatureSelect drive the feature
// negotiation iterator. The driver writes a select value (0 or 1) and
// then reads DeviceFeature for the corresponding 32-bit half (low or
// high) of the 64-bit feature bitmap.
func (c *ModernConfig) ReadDeviceFeatureSelect() (uint32, error) {
	return c.BAR.Read32(c.CommonCfgBAR, c.CommonCfgOffset+CfgDeviceFeatureSelect)
}

// WriteDeviceFeatureSelect writes the feature-select index.
func (c *ModernConfig) WriteDeviceFeatureSelect(v uint32) error {
	return c.BAR.Write32(c.CommonCfgBAR, c.CommonCfgOffset+CfgDeviceFeatureSelect, v)
}

// ReadDeviceFeature reads the 32-bit feature half corresponding to the
// last DeviceFeatureSelect write.
func (c *ModernConfig) ReadDeviceFeature() (uint32, error) {
	return c.BAR.Read32(c.CommonCfgBAR, c.CommonCfgOffset+CfgDeviceFeature)
}

// WriteDriverFeatureSelect writes the driver-feature-select index.
func (c *ModernConfig) WriteDriverFeatureSelect(v uint32) error {
	return c.BAR.Write32(c.CommonCfgBAR, c.CommonCfgOffset+CfgDriverFeatureSelect, v)
}

// WriteDriverFeature writes the 32-bit driver-feature half corresponding
// to the last DriverFeatureSelect write.
func (c *ModernConfig) WriteDriverFeature(v uint32) error {
	return c.BAR.Write32(c.CommonCfgBAR, c.CommonCfgOffset+CfgDriverFeature, v)
}

// DeviceFeatures64 reads the full 64-bit feature bitmap in two 32-bit
// halves, hiding the select+read dance.
func (c *ModernConfig) DeviceFeatures64() (uint64, error) {
	if err := c.WriteDeviceFeatureSelect(0); err != nil {
		return 0, err
	}
	lo, err := c.ReadDeviceFeature()
	if err != nil {
		return 0, err
	}
	if err := c.WriteDeviceFeatureSelect(1); err != nil {
		return 0, err
	}
	hi, err := c.ReadDeviceFeature()
	if err != nil {
		return 0, err
	}
	return uint64(lo) | uint64(hi)<<32, nil
}

// SetDriverFeatures64 writes the full 64-bit driver-feature bitmap in
// two 32-bit halves.
func (c *ModernConfig) SetDriverFeatures64(v uint64) error {
	if err := c.WriteDriverFeatureSelect(0); err != nil {
		return err
	}
	if err := c.WriteDriverFeature(uint32(v & 0xFFFFFFFF)); err != nil {
		return err
	}
	if err := c.WriteDriverFeatureSelect(1); err != nil {
		return err
	}
	return c.WriteDriverFeature(uint32(v >> 32))
}

// DeviceStatus reads the DeviceStatus byte. The init sequence reads it
// after writing FEATURES_OK to confirm the device accepted the subset.
func (c *ModernConfig) DeviceStatus() (uint8, error) {
	return c.BAR.Read8(c.CommonCfgBAR, c.CommonCfgOffset+CfgDeviceStatus)
}

// SetDeviceStatus writes a new DeviceStatus byte. The init sequence
// drives this through the {0, ACK, ACK|DRIVER, ACK|DRIVER|FEATURES_OK,
// ACK|DRIVER|FEATURES_OK|DRIVER_OK} progression.
func (c *ModernConfig) SetDeviceStatus(v uint8) error {
	return c.BAR.Write8(c.CommonCfgBAR, c.CommonCfgOffset+CfgDeviceStatus, v)
}

// NumQueues returns the device's maximum supported queue count
// (Virtio 1.1 §4.1.5.1). For virtio-net this is at least 2 (rxq+txq);
// for virtio-blk usually 1.
func (c *ModernConfig) NumQueues() (uint16, error) {
	return c.BAR.Read16(c.CommonCfgBAR, c.CommonCfgOffset+CfgNumQueues)
}

// ConfigGeneration returns the device's configuration-generation
// counter (Virtio 1.1 §2.4.1). Used to detect device-cfg races.
func (c *ModernConfig) ConfigGeneration() (uint8, error) {
	return c.BAR.Read8(c.CommonCfgBAR, c.CommonCfgOffset+CfgConfigGeneration)
}

// SelectQueue selects which virtqueue subsequent register accesses
// target (Virtio 1.1 §4.1.5.1.3). MUST be called before any
// QueueSize / QueueDesc / QueueDriver / QueueDevice / QueueEnable
// read or write.
func (c *ModernConfig) SelectQueue(idx uint16) error {
	return c.BAR.Write16(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueSelect, idx)
}

// QueueDesc / QueueDriver / QueueDevice / QueueEnable read back the
// per-queue address registers. Exposed for diagnostic readback after
// SetQueue* — useful to confirm the host actually stored the writes
// (some firmware silently drops 64-bit MMIO writes).
func (c *ModernConfig) QueueDesc() (uint64, error) {
	return c.BAR.Read64(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueDesc)
}

// QueueDriver reads the per-queue avail-ring address.
func (c *ModernConfig) QueueDriver() (uint64, error) {
	return c.BAR.Read64(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueDriver)
}

// QueueDevice reads the per-queue used-ring address.
func (c *ModernConfig) QueueDevice() (uint64, error) {
	return c.BAR.Read64(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueDevice)
}

// QueueEnable reads the per-queue enable bit.
func (c *ModernConfig) QueueEnable() (uint16, error) {
	return c.BAR.Read16(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueEnable)
}

// QueueSize returns the device's current size for the selected queue
// (the device's maximum capability; the driver MAY write a smaller
// power-of-two value).
func (c *ModernConfig) QueueSize() (uint16, error) {
	return c.BAR.Read16(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueSize)
}

// SetQueueSize writes the driver's chosen queue size. MUST be a power
// of two and <= the device's reported max.
func (c *ModernConfig) SetQueueSize(v uint16) error {
	return c.BAR.Write16(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueSize, v)
}

// QueueNotifyOff returns the per-queue notification offset
// (Virtio 1.1 §4.1.4.4).
func (c *ModernConfig) QueueNotifyOff() (uint16, error) {
	return c.BAR.Read16(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueNotifyOff)
}

// SetQueueDesc / SetQueueDriver / SetQueueDevice publish the per-queue
// physical addresses to the device (Virtio 1.1 §4.1.5.1). All three are
// 64-bit; we write them with one BAR.Write64 per spec.
func (c *ModernConfig) SetQueueDesc(addr uint64) error {
	return c.BAR.Write64(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueDesc, addr)
}

// SetQueueDriver publishes the avail-ring's physical address.
func (c *ModernConfig) SetQueueDriver(addr uint64) error {
	return c.BAR.Write64(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueDriver, addr)
}

// SetQueueDevice publishes the used-ring's physical address.
func (c *ModernConfig) SetQueueDevice(addr uint64) error {
	return c.BAR.Write64(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueDevice, addr)
}

// SetQueueEnable writes 1 to QueueEnable (Virtio 1.1 §4.1.5.1.3) —
// the device starts servicing the queue.
func (c *ModernConfig) SetQueueEnable(v uint16) error {
	return c.BAR.Write16(c.CommonCfgBAR, c.CommonCfgOffset+CfgQueueEnable, v)
}

// NotifyQueue writes the queue index to the per-queue notification
// address (Virtio 1.1 §4.1.4.4).
//
// **Write-width selection.** The spec ("The driver writes the 16-bit
// virtqueue index ...") prescribes the VALUE width, not the MMIO WIDTH.
// Empirically, virtio backends differ on the MMIO width they accept:
//
//   - QEMU + EDK2 accepts any width as long as it lands on the
//     per-queue slot — the canonical reference driver
//     (Linux drivers/virtio/virtio_pci_modern.c::vp_notify) issues
//     `iowrite16(vq->index, addr)` everywhere.
//   - Apple VZ's virtio-net backend (vfkit 0.6.3 / arm64) appears to
//     dispatch notifications based on the per-queue stride implied by
//     `notify_off_multiplier`: with `multiplier=4` and `length=8` (two
//     queues, stride 4 each), VZ honors a 32-bit MMIO write at the
//     slot's base offset but silently drops a 16-bit write at the same
//     offset.
//
// So we widen the doorbell write to match the per-queue stride: when
// the device publishes `notify_off_multiplier >= 4` we issue a uint32
// MMIO write (queue index zero-extended); when the multiplier is 0, 1,
// or 2 we keep the spec-default uint16. The value written is the queue
// index in either case, exactly as the spec mandates.
//
// On QEMU+EDK2 with `notify_off_multiplier=4` (the standard modern
// transport), this is a no-op for correctness — a uint32 write with the
// queue index in the low 16 bits and zero in the high 16 bits hits the
// same per-queue dispatch path; the upper 16 bits are "reserved, write
// zero" per spec.
func (c *ModernConfig) NotifyQueue(queueIdx uint16, queueNotifyOff uint16) error {
	addr := c.PerQueueNotifyOffset(queueNotifyOff)
	if c.NotifyOffMultiplier >= 4 {
		return c.BAR.Write32(c.NotifyCfgBAR, addr, uint32(queueIdx))
	}
	return c.BAR.Write16(c.NotifyCfgBAR, addr, queueIdx)
}

// DeviceCfgRead8 reads one byte from the device-specific config region
// with a bounds-check against `DeviceCfgLength`. Returns 0 + a sentinel
// error if the offset is outside the region the device published.
//
// Device-class drivers (virtio-net's MAC reader, virtio-blk's capacity
// reader, …) route their DeviceCfg accesses through this entry so the
// bounds-check is applied consistently.
func (c *ModernConfig) DeviceCfgRead8(offset uint32) (uint8, error) {
	if !c.HasDeviceCfg() {
		return 0, ErrNoDeviceCfg
	}
	if offset >= c.DeviceCfgLength {
		return 0, ErrDeviceCfgOutOfBounds
	}
	return c.BAR.Read8(c.DeviceCfgBAR, c.DeviceCfgOffset+uint64(offset))
}

// DeviceCfgRead16 / DeviceCfgRead32 / DeviceCfgRead64 are typed
// device-cfg readers with the same bounds-check. The width is the
// access width; the bounds-check guards offset + sizeof(width).
func (c *ModernConfig) DeviceCfgRead16(offset uint32) (uint16, error) {
	if !c.HasDeviceCfg() {
		return 0, ErrNoDeviceCfg
	}
	if uint64(offset)+2 > uint64(c.DeviceCfgLength) {
		return 0, ErrDeviceCfgOutOfBounds
	}
	return c.BAR.Read16(c.DeviceCfgBAR, c.DeviceCfgOffset+uint64(offset))
}

// DeviceCfgRead32 reads a 32-bit field from device-cfg with bounds-check.
func (c *ModernConfig) DeviceCfgRead32(offset uint32) (uint32, error) {
	if !c.HasDeviceCfg() {
		return 0, ErrNoDeviceCfg
	}
	if uint64(offset)+4 > uint64(c.DeviceCfgLength) {
		return 0, ErrDeviceCfgOutOfBounds
	}
	return c.BAR.Read32(c.DeviceCfgBAR, c.DeviceCfgOffset+uint64(offset))
}

// DeviceCfgRead64 reads a 64-bit field from device-cfg with bounds-check.
func (c *ModernConfig) DeviceCfgRead64(offset uint32) (uint64, error) {
	if !c.HasDeviceCfg() {
		return 0, ErrNoDeviceCfg
	}
	if uint64(offset)+8 > uint64(c.DeviceCfgLength) {
		return 0, ErrDeviceCfgOutOfBounds
	}
	return c.BAR.Read64(c.DeviceCfgBAR, c.DeviceCfgOffset+uint64(offset))
}

// Sentinel errors for the modern transport.
var (
	ErrNoCommonCfg          = commonError("go-virtio/common: no VIRTIO_PCI_CAP_COMMON_CFG capability found")
	ErrCommonCfgTooShort    = commonError("go-virtio/common: COMMON_CFG length < 56 (firmware malformed)")
	ErrNoNotifyCfg          = commonError("go-virtio/common: no VIRTIO_PCI_CAP_NOTIFY_CFG capability found")
	ErrCapListBitUnset      = commonError("go-virtio/common: PCI Status[CapList] bit unset (legacy-only device)")
	ErrNoDeviceCfg          = commonError("go-virtio/common: device has no VIRTIO_PCI_CAP_DEVICE_CFG")
	ErrDeviceCfgOutOfBounds = commonError("go-virtio/common: device-cfg read past published length")
)
