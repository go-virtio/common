// go-virtio/common — Virtio PCI constants + capability walker.
//
// Transport-agnostic: takes a PCIConfigReader (see transport.go) and
// walks the standard PCI capability linked list, returning every entry
// whose CapID is 0x09 (vendor-specific) — these are the virtio
// capabilities. The walker is the same shape every modern virtio
// device publishes (Virtio 1.1 §4.1.4); per-host wiring lives entirely
// behind the PCIConfigReader.
//
// References:
//
//   - Virtio 1.1 §4.1.2.1 "PCI Device Discovery" — vendor / device IDs.
//   - Virtio 1.1 §4.1.4   "Virtio Structure PCI Capabilities" — the
//                          struct virtio_pci_cap body the walker parses.
//   - Virtio 1.1 §5.1     "Network Device" — virtio-net device-type
//                          encoding.
//   - PCI Local Bus Specification 3.0 §6.7 — generic PCI capability
//                          header (CapID + Next byte pointer).
//   - Linux drivers/virtio/virtio_pci_modern.c — canonical
//                          Go-translatable reference.

package common

// PCIVendorID is the Red Hat / Qumranet vendor ID assigned to every
// transitional and modern virtio device (Virtio 1.1 §4.1.2.1).
const PCIVendorID uint16 = 0x1AF4

// Virtio PCI device-ID ranges (Virtio 1.1 §4.1.2.1):
//
//	0x1000..0x103F  legacy / transitional devices (one DID per type).
//	0x1040..0x107F  modern devices; DID = 0x1040 + device_type.
const (
	PCIDeviceIDLegacyMin uint16 = 0x1000
	PCIDeviceIDLegacyMax uint16 = 0x103F
	PCIDeviceIDModernMin uint16 = 0x1040
	PCIDeviceIDModernMax uint16 = 0x107F
)

// Per-device-type encodings (Virtio 1.1 §5 device chapters). The
// integer is the `T` in `0x1040 + T` for the modern range.
const (
	DeviceTypeNet     uint16 = 1
	DeviceTypeBlock   uint16 = 2
	DeviceTypeConsole uint16 = 3
	DeviceTypeEntropy uint16 = 4
	DeviceTypeBalloon uint16 = 5
	DeviceTypeGPU     uint16 = 16
	DeviceTypeVsock   uint16 = 19
)

// Convenience constants for the common device-class IDs.
const (
	PCIDeviceIDLegacyNet     uint16 = 0x1000
	PCIDeviceIDModernNet     uint16 = 0x1041
	PCIDeviceIDLegacyBlock   uint16 = 0x1001
	PCIDeviceIDModernBlock   uint16 = 0x1042
	PCIDeviceIDLegacyEntropy uint16 = 0x1005
	PCIDeviceIDModernEntropy uint16 = 0x1044
	// virtio-console: legacy/transitional 0x1003, modern 0x1040+3.
	PCIDeviceIDLegacyConsole uint16 = 0x1003
	PCIDeviceIDModernConsole uint16 = 0x1043
	// virtio-balloon: legacy/transitional 0x1002, modern 0x1040+5.
	PCIDeviceIDLegacyBalloon uint16 = 0x1002
	PCIDeviceIDModernBalloon uint16 = 0x1045
	// virtio-vsock and virtio-gpu postdate the legacy transport, so each
	// has only a modern device ID (0x1040 + device type).
	PCIDeviceIDModernVsock uint16 = 0x1053
	PCIDeviceIDModernGPU   uint16 = 0x1050
)

// PCIDeviceIDIsModern reports whether a DeviceID is in the modern
// (1.0+) range (0x1040..0x107F). Legacy devices use 0x1000..0x103F and
// have a different PCI capability shape; this package's modern
// transport rejects them.
func PCIDeviceIDIsModern(deviceID uint16) bool {
	return deviceID >= PCIDeviceIDModernMin && deviceID <= PCIDeviceIDModernMax
}

// PCIDeviceIDIsLegacy reports whether a DeviceID is in the legacy
// (0.9 / transitional) range. Exposed so callers can branch early.
func PCIDeviceIDIsLegacy(deviceID uint16) bool {
	return deviceID >= PCIDeviceIDLegacyMin && deviceID <= PCIDeviceIDLegacyMax
}

// PCIDeviceIDIsNet reports whether a DeviceID identifies a virtio-net
// device (legacy 0x1000 OR modern 0x1041). Other legacy DIDs
// (0x1001 block, 0x1002 balloon, 0x1003 console, …) are not net devices
// even though they share vendor 0x1AF4.
func PCIDeviceIDIsNet(deviceID uint16) bool {
	return deviceID == PCIDeviceIDLegacyNet || deviceID == PCIDeviceIDModernNet
}

// PCIDeviceIDIsBlock reports whether a DeviceID identifies a virtio-blk
// device.
func PCIDeviceIDIsBlock(deviceID uint16) bool {
	return deviceID == PCIDeviceIDLegacyBlock || deviceID == PCIDeviceIDModernBlock
}

// PCIDeviceIDIsEntropy reports whether a DeviceID identifies a
// virtio-rng (virtio-entropy) device (legacy 0x1005 OR modern 0x1044).
func PCIDeviceIDIsEntropy(deviceID uint16) bool {
	return deviceID == PCIDeviceIDLegacyEntropy || deviceID == PCIDeviceIDModernEntropy
}

// PCIDeviceIDIsVsock reports whether a DeviceID identifies a
// virtio-vsock device (modern 0x1053; there is no legacy variant).
func PCIDeviceIDIsVsock(deviceID uint16) bool {
	return deviceID == PCIDeviceIDModernVsock
}

// PCIDeviceIDIsConsole reports whether a DeviceID identifies a
// virtio-console device (legacy 0x1003 OR modern 0x1043).
func PCIDeviceIDIsConsole(deviceID uint16) bool {
	return deviceID == PCIDeviceIDLegacyConsole || deviceID == PCIDeviceIDModernConsole
}

// PCIDeviceIDIsBalloon reports whether a DeviceID identifies a
// virtio-balloon device (legacy 0x1002 OR modern 0x1045).
func PCIDeviceIDIsBalloon(deviceID uint16) bool {
	return deviceID == PCIDeviceIDLegacyBalloon || deviceID == PCIDeviceIDModernBalloon
}

// PCIDeviceIDIsGPU reports whether a DeviceID identifies a virtio-gpu
// device (modern 0x1050; there is no legacy variant).
func PCIDeviceIDIsGPU(deviceID uint16) bool {
	return deviceID == PCIDeviceIDModernGPU
}

// PCICapIDVendorSpecific = 0x09 (PCI Local Bus Specification 3.0
// Appendix H). All Virtio PCI capabilities use this ID.
const PCICapIDVendorSpecific uint8 = 0x09

// VIRTIO_PCI_CAP_* values — the `cfg_type` byte at offset +3 of a
// virtio PCI capability (Virtio 1.1 §4.1.4):
//
//	struct virtio_pci_cap {
//	    u8 cap_vndr;     // PCI cap ID, always 0x09 (vendor-specific)
//	    u8 cap_next;     // next-pointer (PCI cap-list link)
//	    u8 cap_len;      // sizeof(struct virtio_pci_cap), >= 16
//	    u8 cfg_type;     // VIRTIO_PCI_CAP_* (one of the constants below)
//	    u8 bar;          // BAR index containing this structure
//	    u8 id;           // multiple-capability disambiguator
//	    u8 padding[2];
//	    le32 offset;     // offset within `bar`
//	    le32 length;     // length of the structure
//	};
const (
	PCICapCommonCfg    uint8 = 1 // common configuration
	PCICapNotifyCfg    uint8 = 2 // notifications
	PCICapISRCfg       uint8 = 3 // ISR access
	PCICapDeviceCfg    uint8 = 4 // device-specific config
	PCICapPCICfg       uint8 = 5 // PCI configuration access (alternate window)
	PCICapSharedMemCfg uint8 = 8 // 1.1 addition: shared memory
	PCICapVendorCfg    uint8 = 9 // 1.1 addition: vendor-specific
)

// PCICapHeaderSize is the minimum cap_len. Capabilities of every
// cfg_type fit in 16 bytes for the common/notify/ISR/device/PCI-cfg
// kinds; SharedMem and VendorCfg can be larger but this package's
// walker doesn't dive into their bodies.
const PCICapHeaderSize = 16

// PCICapNotifyExtraSize is the byte length of the extended
// `virtio_pci_notify_cap` body beyond the 16-byte standard header
// (Virtio 1.1 §4.1.4.4). The 4 bytes at +16 hold the
// `notify_off_multiplier`.
const PCICapNotifyExtraSize = 4

// PCI configuration-space header offsets used by the modern transport's
// initialization path. The standard PCI Type 0 header is documented in
// PCI Local Bus Specification 3.0 §6.1.
const (
	PCICfgVendorID        uint8 = 0x00
	PCICfgDeviceID        uint8 = 0x02
	PCICfgCommand         uint8 = 0x04
	PCICfgStatus          uint8 = 0x06
	PCICfgCapabilitiesPtr uint8 = 0x34
)

// PCIStatusCapabilityList is bit 4 of the PCI Status register; when
// set, the CapabilitiesPtr at 0x34 is valid (PCI Local Bus
// Specification 3.0 §6.2.3).
const PCIStatusCapabilityList uint16 = 0x0010

// PCICap is the parsed Go view of `struct virtio_pci_cap` from the
// device's PCI cap-list. Stored as direct fields (not a pointer into
// the host's config-space view) so callers — including host tests —
// can hand-build instances.
type PCICap struct {
	// CapID is always 0x09 (vendor-specific). Stored only so a caller
	// can sanity-check.
	CapID uint8

	// Next is the PCI cap-list link byte (config-space offset of the
	// next capability, or 0 to terminate). Stored for debugging.
	Next uint8

	// Len is the cap_len byte; values < 16 are spec-violating.
	Len uint8

	// CfgType is one of the PCICap* constants above. The walker
	// returns all of them; callers filter by type.
	CfgType uint8

	// BAR is the PCI BAR index containing this structure (0..5).
	BAR uint8

	// ID disambiguates multiple capabilities of the same CfgType
	// (e.g. two device-cfg structures on one transitional device).
	ID uint8

	// Offset is the byte offset within BAR of this structure.
	Offset uint32

	// Length is the byte length of the structure inside BAR.
	Length uint32

	// CfgSpaceOffset is the config-space offset where this capability
	// header sits (not part of the spec'd virtio_pci_cap struct — the
	// walker fills it in for diagnostic prints and for reading the
	// extended NOTIFY_CFG body).
	CfgSpaceOffset uint8
}

// MaxCapsToWalk caps the cap-list walk so a malformed (cyclic or
// self-referential) cap chain doesn't hang the probe. The PCI spec
// allows at most 48 capabilities in the 192-byte device-specific config
// area; 64 is generous.
const MaxCapsToWalk = 64

// WalkPCICaps walks the PCI capability linked list starting at
// `firstCapOffset`, returning every entry whose CapID is 0x09
// (vendor-specific — i.e. a virtio capability). Non-vendor capabilities
// (e.g. MSI-X) are skipped — the walker follows their next pointer
// without emitting them.
//
// Per Virtio 1.1 §4.1.4, each vendor cap reads:
//
//	+0  CapID    (u8) = 0x09
//	+1  Next     (u8)
//	+2  cap_len  (u8) >= 16
//	+3  cfg_type (u8) = one of PCICap*
//	+4  bar      (u8)
//	+5  id       (u8)
//	+6  padding  (u8 x 2)
//	+8  offset   (LE u32)
//	+12 length   (LE u32)
//
// Errors:
//
//   - ErrCapChainBadPtr — a Next pointer landed below 0x40 (outside
//     the standard cap area). Returned with the partial result.
//   - ErrCapChainTooLong — the walker hit MaxCapsToWalk iterations
//     without seeing a 0-terminator. Returned with whatever caps were
//     collected.
//   - any error returned by the PCIConfigReader — propagated as-is.
//
// On a Read* error mid-walk, the partial result is returned alongside
// the error so the caller can still inspect whatever caps were
// successfully enumerated before the failure.
func WalkPCICaps(r PCIConfigReader, firstCapOffset uint8) ([]PCICap, error) {
	if firstCapOffset == 0 {
		// Status[CapList] was set but the pointer is 0 — empty list.
		// Spec-violating but harmless; treat as "no virtio caps".
		return nil, nil
	}
	var out []PCICap
	off := firstCapOffset
	for i := 0; i < MaxCapsToWalk; i++ {
		if off == 0 {
			return out, nil
		}
		// PCI cap-list pointers MUST land in the standard config-space
		// area (0x40..0xFF). A value < 0x40 is a malformed firmware.
		if off < 0x40 {
			return out, ErrCapChainBadPtr
		}
		capID, err := r.ReadConfig8(off + 0)
		if err != nil {
			return out, err
		}
		next, err := r.ReadConfig8(off + 1)
		if err != nil {
			return out, err
		}
		if capID != PCICapIDVendorSpecific {
			// Not a virtio capability; follow the link without
			// emitting anything.
			off = next
			continue
		}
		clen, err := r.ReadConfig8(off + 2)
		if err != nil {
			return out, err
		}
		cfgType, err := r.ReadConfig8(off + 3)
		if err != nil {
			return out, err
		}
		bar, err := r.ReadConfig8(off + 4)
		if err != nil {
			return out, err
		}
		id, err := r.ReadConfig8(off + 5)
		if err != nil {
			return out, err
		}
		offset32, err := r.ReadConfig32(off + 8)
		if err != nil {
			return out, err
		}
		length32, err := r.ReadConfig32(off + 12)
		if err != nil {
			return out, err
		}
		out = append(out, PCICap{
			CapID:          capID,
			Next:           next,
			Len:            clen,
			CfgType:        cfgType,
			BAR:            bar,
			ID:             id,
			Offset:         offset32,
			Length:         length32,
			CfgSpaceOffset: off,
		})
		off = next
	}
	return out, ErrCapChainTooLong
}

// PCICapsByType returns the first capability in `caps` whose CfgType
// matches, or nil. Callers use this to locate (for example) the
// VIRTIO_PCI_CAP_DEVICE_CFG capability before reading device-specific
// config like a virtio-net MAC.
func PCICapsByType(caps []PCICap, cfgType uint8) *PCICap {
	for i := range caps {
		if caps[i].CfgType == cfgType {
			return &caps[i]
		}
	}
	return nil
}

// NotifyOffMultiplierCfgOffset returns the PCI config-space byte offset
// of the `notify_off_multiplier` field for a NOTIFY_CFG cap that sits
// at `notifyCapCfgSpaceOffset` in cfg-space. Per Virtio 1.1 §4.1.4.4,
// the extended notify variant adds 4 bytes past the 16-byte header.
func NotifyOffMultiplierCfgOffset(notifyCapCfgSpaceOffset uint8) uint32 {
	return uint32(notifyCapCfgSpaceOffset) + 16
}

// Sentinel errors for the cap walker.
var (
	ErrCapChainTooLong = commonError("go-virtio/common: PCI cap-list walk exceeded MaxCapsToWalk (likely cyclic)")
	ErrCapChainBadPtr  = commonError("go-virtio/common: PCI cap-list pointer < 0x40 (outside standard config-space)")
)
