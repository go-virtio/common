// Package common holds the transport-agnostic infrastructure shared by
// every device-class virtio driver in the go-virtio family (virtio-net,
// virtio-blk, …). The high-level shape mirrors Linux's
// <linux/virtio.h> + virtio_ring.h split: this package owns the PCI
// capability walker, the modern transport layout + register accessors,
// and the split-virtqueue layout + driver-side state machine. The
// per-device-class drivers (go-virtio/net, go-virtio/blk) sit on top
// and import this package for everything below the wire format.
//
// Transports plug in through three small interfaces:
//
//   - PCIConfigReader exposes PCI configuration-space reads for the
//     capability walker. Implementations bridge to whatever the host
//     offers: EFI_PCI_IO_PROTOCOL.Pci.Read on UEFI, direct MMIO config
//     reads on bare metal, ioread* on Linux user-space, etc.
//
//   - BARMemoryAccessor exposes BAR-window memory reads + writes for
//     the modern transport's register accesses. Implementations bridge
//     to EFI_PCI_IO_PROTOCOL.Mem.Read/Write, raw MMIO, or whatever the
//     host exposes.
//
//   - PageAllocator returns DMA-capable, physically-contiguous,
//     page-aligned memory for virtqueue backing storage. Implementations
//     bridge to gBS->AllocatePages on UEFI, sysfs `dma_alloc_coherent`-
//     equivalents on Linux, etc.
//
// Bundling all three behind a single `Transport` typedef is a
// convenience — most callers want to pass one value to the driver-level
// `Open(transport)` constructors and let the package extract whichever
// sub-interface it needs.
//
// References (cited at every layout/register decision in the package):
//
//   - Virtio 1.1 (committee specification 01, 2019-04-11):
//       * §4.1.2.1 "PCI Device Discovery" — vendor/device IDs.
//       * §4.1.4   "Virtio Structure PCI Capabilities" — cap layout.
//       * §4.1.5   "PCI-specific Initialization And Device Operation"
//                  — COMMON_CFG / NOTIFY_CFG / ISR_CFG / DEVICE_CFG /
//                    PCI_CFG register layouts.
//       * §2.6     "Virtqueues" — split-ring layout (this package
//                  implements split-ring; packed-ring is not supported).
//       * §3.1.1   "Driver Requirements: Device Initialization" — the
//                  status-bit choreography device-class drivers follow.
//   - Linux drivers/virtio/virtio_pci_modern.c — canonical
//     Go-translatable reference for the COMMON_CFG handshake.
//   - Linux drivers/virtio/virtio_ring.c — canonical reference for the
//     descriptor + ring helpers.
package common

// PCIConfigReader exposes PCI configuration-space reads for the virtio
// capability walker (Virtio 1.1 §4.1.4). The walker only reads from the
// standard PCI cap-list area (0x40..0xFF) and only ever needs 8-bit and
// 32-bit accesses; 16-bit is exposed for completeness because some
// transports (Linux, EFI) have a native 16-bit read primitive that's
// cheaper than two byte reads.
//
// Implementations:
//
//   - UEFI: EFI_PCI_IO_PROTOCOL.Pci.Read with Width=Uint8/Uint16/Uint32.
//   - Bare metal: direct MMIO config-space access (post-EBS on platforms
//     where the firmware exposes a flat config-space window).
//   - virtio-mmio: this interface is unused (mmio devices don't have a
//     PCI cap chain — the equivalent metadata lives at MMIO offsets
//     0x00..0xFF on the device's register window).
//
// Errors returned MUST be non-nil whenever the read failed and the
// returned scalar is meaningless; the walker short-circuits on the first
// non-nil error and surfaces it to the caller along with any caps
// collected so far.
type PCIConfigReader interface {
	ReadConfig8(offset uint8) (uint8, error)
	ReadConfig16(offset uint8) (uint16, error)
	ReadConfig32(offset uint8) (uint32, error)
}

// BARMemoryAccessor exposes typed reads + writes against a virtio
// modern device's BAR-window registers (Virtio 1.1 §4.1.5). All accesses
// are little-endian; on every Go-supported arch that's the native
// byte-order so callers don't need to swap.
//
// `bar` is the PCI BAR index (0..5) the virtio capability published.
// `offset` is the byte offset within that BAR. Implementations route
// (bar, offset) into the host's BAR-access primitive:
//
//   - UEFI: EFI_PCI_IO_PROTOCOL.Mem.Read/Write with Width=Uint{8,16,32,64}.
//   - Bare metal: direct MMIO at (BAR-physical-base + offset).
//
// The 64-bit accessors exist for the QueueDesc / QueueDriver /
// QueueDevice registers (Virtio 1.1 §4.1.5.1). Some firmware tolerates
// only two consecutive 32-bit halves; if a host's MMIO primitive lacks
// a 64-bit access, the implementation MAY decompose Read64/Write64
// into two Read32/Write32 internally.
type BARMemoryAccessor interface {
	Read8(bar uint8, offset uint64) (uint8, error)
	Read16(bar uint8, offset uint64) (uint16, error)
	Read32(bar uint8, offset uint64) (uint32, error)
	Read64(bar uint8, offset uint64) (uint64, error)
	Write8(bar uint8, offset uint64, val uint8) error
	Write16(bar uint8, offset uint64, val uint16) error
	Write32(bar uint8, offset uint64, val uint32) error
	Write64(bar uint8, offset uint64, val uint64) error
}

// PageAllocator returns physically-contiguous, page-aligned, DMA-capable
// pages suitable for virtqueue backing storage. Virtio 1.1 §2.6
// requires the descriptor / available-ring / used-ring regions to be
// naturally aligned (16 / 2 / 4 bytes respectively); allocating a single
// 4 KiB page for the lot trivially satisfies all three.
//
// Implementations:
//
//   - UEFI: gBS->AllocatePages(EfiBootServicesData, count) — identity-
//     mapped during Boot Services so the physical address returned is
//     also a usable Go-side pointer.
//   - Bare-metal Linux user-space: hugepage backing + an IOMMU mapping.
//
// `count` is the number of 4 KiB pages to allocate. The returned
// `physAddr` is the physical address the device will see (published via
// QueueDesc / QueueDriver / QueueDevice MMIO registers); `mem` is a
// host-side Go-byte view of the same memory that the driver writes
// descriptors + available-ring entries into. On UEFI-style identity-
// mapped hosts these may correspond to the same address; on hosts with
// separate physical / virtual address spaces, the implementation
// provides the translation.
//
// Implementations MUST return zero-initialised memory — Virtio 1.1
// doesn't strictly require it, but a stale used-ring `idx` would make
// the driver's first PollUsed() spuriously see a "completed" entry.
type PageAllocator interface {
	AllocatePages(count int) (physAddr uint64, mem []byte, err error)
}

// Transport bundles the three transport-level interfaces a virtio
// device-class driver needs. Most drivers want to pass a single value
// to their `Open(transport)` constructor and let the package extract
// whichever sub-interface it needs; the alternative is to pass three
// separate values, which gets noisy.
//
// Implementations are free to satisfy `Transport` via a single struct
// that embeds all three methods, or via three separate struct fields —
// either shape works as long as the methods are present.
type Transport interface {
	PCIConfigReader
	BARMemoryAccessor
	PageAllocator
}

// commonError is a tiny sentinel-error type with no external
// dependencies (the package avoids importing `errors` to keep the
// dependency footprint trivial for embedded targets where the linker
// may otherwise pull a chunk of runtime machinery in).
type commonError string

// Error implements the `error` interface for sentinel errors defined
// throughout the package.
func (e commonError) Error() string { return string(e) }
