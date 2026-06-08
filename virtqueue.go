// go-virtio/common — Virtio split-virtqueue layout + driver-side data
// structures.
//
// Transport-agnostic: the layout constants, the `Virtqueue` struct, the
// descriptor-table / available-ring / used-ring offset math, and the
// AddBuffer / PostAvail / PollUsed state machine are all pure Go data
// manipulation. The live page allocation routes through the
// `PageAllocator` interface (see transport.go); per-host wiring lives
// in the host transport adapter.
//
// References:
//
//   - Virtio 1.1 §2.6   "Virtqueues" — the split-ring layout this file
//     implements: descriptor table, available ring (driver area), used
//     ring (device area). The "packed virtqueue" variant from Virtio
//     1.1 §2.7 is NOT supported here; device-class drivers negotiate it
//     OUT in their feature mask.
//   - Virtio 1.1 §2.6.5 "The Virtqueue Descriptor Table" — desc[i] is
//     16 bytes: { addr (le64), len (le32), flags (le16), next (le16) }.
//   - Virtio 1.1 §2.6.6 "The Virtqueue Available Ring":
//         struct { le16 flags; le16 idx; le16 ring[queue_size]; le16 used_event; }
//     Length: 6 + 2*queue_size bytes.
//   - Virtio 1.1 §2.6.8 "The Virtqueue Used Ring":
//         struct { le16 flags; le16 idx; struct { le32 id; le32 len; } ring[queue_size]; le16 avail_event; }
//     Length: 6 + 8*queue_size bytes.
//   - Linux drivers/virtio/virtio_ring.c — canonical Go-translatable
//     reference for the descriptor + ring helpers.

package common

import (
	"encoding/binary"
	"sync/atomic"
	"unsafe"
)

// PageSize is the UEFI / common page size — 4 KiB on every UEFI arch
// (UEFI 2.10 §2.3) and the natural unit virtio backends expect for
// virtqueue backing. Exposed so transport adapters can match.
const PageSize uintptr = 4096

// VirtqDescriptorSize is the on-the-wire size of one descriptor
// (Virtio 1.1 §2.6.5). Sixteen bytes:
//
//	0..7   addr   (le64) — guest-physical address of the buffer
//	8..11  len    (le32) — buffer length
//	12..13 flags  (le16) — VIRTQ_DESC_F_*
//	14..15 next   (le16) — descriptor index for chains
const VirtqDescriptorSize = 16

// VIRTQ_DESC_F_* flags (Virtio 1.1 §2.6.5).
const (
	VirtqDescFNext     uint16 = 0x1 // descriptor chain continues at .next
	VirtqDescFWrite    uint16 = 0x2 // buffer is device-write-only (RX)
	VirtqDescFIndirect uint16 = 0x4 // descriptor refers to an indirect table (Virtio 1.1 §2.6.5.3)
)

// VirtqAvail* — components of the available ring (Virtio 1.1 §2.6.6).
const (
	VirtqAvailHeaderSize    = 4 // flags + idx (2 + 2)
	VirtqAvailRingEntrySize = 2 // ring[i] is le16
	VirtqAvailUsedEventSize = 2 // trailing `used_event` field
)

// VirtqUsed* — components of the used ring (Virtio 1.1 §2.6.8).
const (
	VirtqUsedHeaderSize     = 4 // flags + idx (2 + 2)
	VirtqUsedRingEntrySize  = 8 // ring[i] is { id (le32), len (le32) }
	VirtqUsedAvailEventSize = 2 // trailing `avail_event` field
)

// VirtqueueLayout describes the byte-offset layout of one split
// virtqueue inside a single contiguous allocation.
//
// Per Virtio 1.1 §2.6, the three regions are independently aligned:
//
//	descriptor table : 16-byte aligned
//	available ring   : 2-byte aligned
//	used ring        : 4-byte aligned
//
// We allocate ALL THREE on a single 4 KiB page so every alignment
// constraint is trivially met. There's no in-page padding between the
// descriptor table and the available ring; the used ring is placed at
// the next 4-byte boundary after the available ring's `used_event`.
type VirtqueueLayout struct {
	// Size is the queue size (power-of-two count of descriptors).
	Size uint16

	// DescTableOffset is the byte offset of the descriptor table from
	// the allocation base. Always 0.
	DescTableOffset uint32

	// AvailRingOffset is the byte offset of the available ring's
	// `flags` field from the allocation base. = Size * 16.
	AvailRingOffset uint32

	// AvailUsedEventOffset is the byte offset of the `used_event`
	// field (Virtio 1.1 §2.6.7) — appended after the available
	// ring's `ring[]`.
	AvailUsedEventOffset uint32

	// UsedRingOffset is the byte offset of the used ring's `flags`
	// field from the allocation base. 4-byte aligned.
	UsedRingOffset uint32

	// UsedAvailEventOffset is the byte offset of the `avail_event`
	// field — appended after the used ring's `ring[]`.
	UsedAvailEventOffset uint32

	// TotalSize is the total byte size of the allocation needed to
	// hold all three regions.
	TotalSize uint32
}

// ComputeVirtqueueLayout returns the byte-offset layout for a split
// virtqueue of the given size. Size MUST be a power of two between 1
// and 32768 (Virtio 1.1 §2.6 — `queue_size` is le16 with the
// power-of-two constraint). Callers validate `size` before calling;
// this routine accepts any value and returns the resulting layout.
func ComputeVirtqueueLayout(size uint16) VirtqueueLayout {
	l := VirtqueueLayout{Size: size}
	l.DescTableOffset = 0
	descTableSize := uint32(size) * VirtqDescriptorSize
	l.AvailRingOffset = descTableSize
	availBodySize := uint32(VirtqAvailHeaderSize) + uint32(size)*uint32(VirtqAvailRingEntrySize)
	l.AvailUsedEventOffset = l.AvailRingOffset + availBodySize
	// used ring is 4-byte aligned within the page; round up the
	// available-ring end (incl. used_event).
	availEnd := l.AvailUsedEventOffset + uint32(VirtqAvailUsedEventSize)
	l.UsedRingOffset = (availEnd + 3) &^ 3
	usedBodySize := uint32(VirtqUsedHeaderSize) + uint32(size)*uint32(VirtqUsedRingEntrySize)
	l.UsedAvailEventOffset = l.UsedRingOffset + usedBodySize
	l.TotalSize = l.UsedAvailEventOffset + uint32(VirtqUsedAvailEventSize)
	return l
}

// Virtqueue is the driver-side handle for one split virtqueue.
//
// `mem` is the host-side Go-byte view of the queue allocation (the
// PageAllocator returned this). The driver writes descriptors and
// available-ring entries through this slice. `BasePhys` is the same
// region's physical-address view — what the device sees through DMA;
// it's published via SetQueueDesc / SetQueueDriver / SetQueueDevice.
//
// `Buffers` is the per-descriptor driver-side bookkeeping (the
// host-visible pointer + the physical address + the length + an InUse
// marker).
type Virtqueue struct {
	// Index is the queue's index inside the device (e.g. 0 = rxq, 1 =
	// txq on virtio-net per Virtio 1.1 §5.1.2).
	Index uint16

	// Layout is the byte-offset map for this queue's allocation.
	Layout VirtqueueLayout

	// mem is the host-side byte view of the queue allocation. Held
	// unexported so callers go through the typed accessors below; the
	// raw view is reachable via the (Desc|Avail|Used)Slice helpers.
	mem []byte

	// BasePhys is the physical-address view of `mem[0]` — what the
	// device sees. Published via SetQueueDesc / SetQueueDriver /
	// SetQueueDevice.
	BasePhys uint64

	// NotifyOff is the device-published `queue_notify_off` (read from
	// COMMON_CFG after QueueSelect). Used to compute the per-queue
	// notification BAR offset.
	NotifyOff uint16

	// nextAvailIdx is the driver's running index into the available
	// ring (Virtio 1.1 §2.6.6). Modulo Size determines the ring slot;
	// the raw value is what's written to `available.idx`.
	nextAvailIdx uint16

	// lastSeenUsedIdx is the driver's view of the used ring's `idx`
	// field — used by PollUsed() to know whether the device added a
	// new entry since the last poll.
	lastSeenUsedIdx uint16

	// Buffers holds the driver-side bookkeeping for each descriptor.
	Buffers []VirtqueueBuffer
}

// VirtqueueBuffer is the driver's per-descriptor bookkeeping. Not part
// of the on-the-wire layout; lives in normal Go heap.
type VirtqueueBuffer struct {
	Addr  uintptr // host-virtual address of the data buffer
	Phys  uint64  // physical address (what the device sees)
	Len   uint32  // buffer length in bytes
	InUse bool    // true between AddBuffer and Reclaim
}

// NewVirtqueueFromAlloc constructs a Virtqueue from a pre-zeroed
// memory allocation. `phys` is the page's physical base; `mem` is the
// host-side byte view (slice into the same region); `size` is the
// queue size (power of two); `index` is the queue index inside the
// device.
//
// The allocator MUST have zeroed `mem` — Virtio 1.1 §2.6 doesn't
// strictly require it but a stale used-ring `idx` would make PollUsed
// think the device already published frames.
//
// `mem` MUST be at least `ComputeVirtqueueLayout(size).TotalSize` bytes
// long; this is the caller's responsibility (no runtime length check —
// passing too-short backing would only manifest as out-of-bounds slice
// access in the typed accessors, which is preferable to silent corruption).
func NewVirtqueueFromAlloc(phys uint64, mem []byte, size uint16, index uint16) *Virtqueue {
	return &Virtqueue{
		Index:    index,
		Layout:   ComputeVirtqueueLayout(size),
		mem:      mem,
		BasePhys: phys,
		Buffers:  make([]VirtqueueBuffer, size),
	}
}

// NewVirtqueue allocates the backing memory for a split virtqueue of
// the given size via the supplied PageAllocator, zero-initializes it,
// and returns a driver-side handle ready for descriptor publication.
//
// The caller next calls cfg.SetQueueDesc/Driver/Device(q.BasePhys +
// offset) to publish the per-region physical addresses to the device,
// then cfg.SetQueueEnable(1).
//
// `size` MUST be a non-zero power of two; otherwise ErrInvalidQueueSize.
func NewVirtqueue(a PageAllocator, size uint16, queueIdx uint16, notifyOff uint16) (*Virtqueue, error) {
	if size == 0 || (size&(size-1)) != 0 {
		return nil, ErrInvalidQueueSize
	}
	layout := ComputeVirtqueueLayout(size)
	// Round up to whole pages.
	pages := (int(layout.TotalSize) + int(PageSize) - 1) / int(PageSize)
	if pages == 0 {
		pages = 1
	}
	phys, mem, err := a.AllocatePages(pages)
	if err != nil {
		return nil, err
	}
	if phys == 0 {
		return nil, ErrAllocReturnedZero
	}
	// Defensive zero — PageAllocator implementations are required to
	// return zeroed memory but it's cheap and safe to do once more.
	for i := range mem {
		mem[i] = 0
	}
	q := NewVirtqueueFromAlloc(phys, mem, size, queueIdx)
	q.NotifyOff = notifyOff
	return q, nil
}

// descSlice returns a Go-side view of the descriptor table as a
// `[]byte` of (Size * 16) bytes.
func (q *Virtqueue) descSlice() []byte {
	n := int(q.Layout.Size) * VirtqDescriptorSize
	return q.mem[q.Layout.DescTableOffset : int(q.Layout.DescTableOffset)+n]
}

// availSlice returns a Go-side view of the available ring region
// (header + ring[] + used_event), as one byte slice.
func (q *Virtqueue) availSlice() []byte {
	n := int(q.Layout.AvailUsedEventOffset+uint32(VirtqAvailUsedEventSize)) - int(q.Layout.AvailRingOffset)
	return q.mem[q.Layout.AvailRingOffset : int(q.Layout.AvailRingOffset)+n]
}

// usedSlice returns a Go-side view of the used ring region (header +
// ring[] + avail_event), as one byte slice.
func (q *Virtqueue) usedSlice() []byte {
	n := int(q.Layout.UsedAvailEventOffset+uint32(VirtqUsedAvailEventSize)) - int(q.Layout.UsedRingOffset)
	return q.mem[q.Layout.UsedRingOffset : int(q.Layout.UsedRingOffset)+n]
}

// writeDescriptor populates desc[idx] with (addr, length, flags, next).
// Mirrors `struct vring_desc` from Linux's virtio_ring.h.
func (q *Virtqueue) writeDescriptor(idx uint16, addr uint64, length uint32, flags uint16, next uint16) error {
	if idx >= q.Layout.Size {
		return ErrInvalidIdx
	}
	d := q.descSlice()
	off := int(idx) * VirtqDescriptorSize
	binary.LittleEndian.PutUint64(d[off:off+8], addr)
	binary.LittleEndian.PutUint32(d[off+8:off+12], length)
	binary.LittleEndian.PutUint16(d[off+12:off+14], flags)
	binary.LittleEndian.PutUint16(d[off+14:off+16], next)
	return nil
}

// readDescriptor reads desc[idx] back. Used by tests + diagnostics.
func (q *Virtqueue) readDescriptor(idx uint16) (addr uint64, length uint32, flags uint16, next uint16, err error) {
	if idx >= q.Layout.Size {
		err = ErrInvalidIdx
		return
	}
	d := q.descSlice()
	off := int(idx) * VirtqDescriptorSize
	addr = binary.LittleEndian.Uint64(d[off : off+8])
	length = binary.LittleEndian.Uint32(d[off+8 : off+12])
	flags = binary.LittleEndian.Uint16(d[off+12 : off+14])
	next = binary.LittleEndian.Uint16(d[off+14 : off+16])
	return
}

// PostAvail publishes descriptor[descIdx] in the available ring at
// position `nextAvailIdx % Size` and bumps the published `idx` counter.
// Per Virtio 1.1 §2.6.13 ("Drivers MUST suppress device interrupts
// before checking the available ring"), the device MUST observe
// `ring[]` before `idx`.
//
// Ordering: the available ring's header is two adjacent uint16 fields
// — `flags` at offset 0, `idx` at offset 2 — together forming a 4-byte
// naturally-aligned word at the start of the region. We publish `idx`
// via an `atomic.StoreUint32` on that word, preserving the current
// `flags` value in the low 16 bits and writing the new idx into the
// high 16 bits. This is a release-store on every Go-supported
// architecture, so the device's subsequent read of `idx`
// happens-after our write to `ring[]`.
//
// Single-driver invariant: only this PostAvail writes to the
// available-ring header word.
func (q *Virtqueue) PostAvail(descIdx uint16) error {
	if descIdx >= q.Layout.Size {
		return ErrInvalidIdx
	}
	a := q.availSlice()
	slot := int(VirtqAvailHeaderSize) + (int(q.nextAvailIdx)%int(q.Layout.Size))*VirtqAvailRingEntrySize
	binary.LittleEndian.PutUint16(a[slot:slot+2], descIdx)
	q.nextAvailIdx++
	flags := binary.LittleEndian.Uint16(a[0:2])
	headerWord := uint32(flags) | uint32(q.nextAvailIdx)<<16
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&a[0])), headerWord)
	return nil
}

// AvailFlags returns the current `flags` field of the available ring
// (Virtio 1.1 §2.6.6 — VIRTQ_AVAIL_F_NO_INTERRUPT).
func (q *Virtqueue) AvailFlags() uint16 {
	a := q.availSlice()
	return binary.LittleEndian.Uint16(a[0:2])
}

// AvailIdx returns the current `idx` field — the running count of
// available-ring publications.
func (q *Virtqueue) AvailIdx() uint16 {
	a := q.availSlice()
	return binary.LittleEndian.Uint16(a[2:4])
}

// UsedIdx returns the device-side `idx` field of the used ring. Reads
// with acquire semantics so the subsequent ring[] read sees a committed
// entry.
//
// Layout mirror of the available ring: used-ring header is two
// adjacent uint16 fields at offset 0 (`flags` at +0, `idx` at +2),
// together forming a 4-byte naturally-aligned word. We
// atomic.LoadUint32 the word and extract `idx` from the high 16 bits.
// The load is an acquire on every Go-supported arch.
func (q *Virtqueue) UsedIdx() uint16 {
	u := q.usedSlice()
	headerWord := atomic.LoadUint32((*uint32)(unsafe.Pointer(&u[0])))
	return uint16(headerWord >> 16)
}

// UsedRingAt returns the device's `(id, len)` tuple at slot
// `usedIdx % Size`. Caller computes usedIdx as a counter; this function
// reads the ring entry at the wrapping position.
func (q *Virtqueue) UsedRingAt(usedIdx uint16) (id uint32, length uint32) {
	u := q.usedSlice()
	slot := int(VirtqUsedHeaderSize) + (int(usedIdx)%int(q.Layout.Size))*VirtqUsedRingEntrySize
	id = binary.LittleEndian.Uint32(u[slot : slot+4])
	length = binary.LittleEndian.Uint32(u[slot+4 : slot+8])
	return
}

// AddBuffer finds the first free descriptor slot, fills it with the
// given buffer's (phys, len, flags), bookkeeps it, publishes it in the
// available ring, and returns the descriptor index. `writable` drives
// the VIRTQ_DESC_F_WRITE flag (true = device-write-only, i.e. RX;
// false = device-read-only, i.e. TX).
func (q *Virtqueue) AddBuffer(addr uintptr, phys uint64, length uint32, writable bool) (uint16, error) {
	for i := uint16(0); i < q.Layout.Size; i++ {
		if q.Buffers[i].InUse {
			continue
		}
		flags := uint16(0)
		if writable {
			flags |= VirtqDescFWrite
		}
		if err := q.writeDescriptor(i, phys, length, flags, 0); err != nil {
			return 0, err
		}
		q.Buffers[i] = VirtqueueBuffer{
			Addr:  addr,
			Phys:  phys,
			Len:   length,
			InUse: true,
		}
		if err := q.PostAvail(i); err != nil {
			return 0, err
		}
		return i, nil
	}
	return 0, ErrQueueFull
}

// Reclaim marks descriptor `descIdx` as free (caller has consumed the
// device's used-ring report and copied the data out). Idempotent.
func (q *Virtqueue) Reclaim(descIdx uint16) error {
	if descIdx >= q.Layout.Size {
		return ErrInvalidIdx
	}
	q.Buffers[descIdx].InUse = false
	return nil
}

// PollUsed checks whether the device has added a new used-ring entry
// since the last call. Returns (descIdx, length, ok=true) if so;
// (0, 0, false) otherwise. Mutates `lastSeenUsedIdx` only on a
// successful poll, so retrying after a false result is cheap.
func (q *Virtqueue) PollUsed() (uint16, uint32, bool) {
	curUsed := q.UsedIdx()
	if curUsed == q.lastSeenUsedIdx {
		return 0, 0, false
	}
	id, length := q.UsedRingAt(q.lastSeenUsedIdx)
	q.lastSeenUsedIdx++
	return uint16(id), length, true
}

// LastSeenUsedIdx returns the driver's view of the used-ring index.
func (q *Virtqueue) LastSeenUsedIdx() uint16 { return q.lastSeenUsedIdx }

// NextAvailIdx returns the driver's view of the available-ring index.
func (q *Virtqueue) NextAvailIdx() uint16 { return q.nextAvailIdx }

// DescBytes returns a copy of the 16 bytes of descriptor[idx]. Exposed
// for diagnostic / observability dumps. Returns nil if `idx` is
// out-of-range — callers always get either a full copy or nil, never a
// partial / aliased view.
func (q *Virtqueue) DescBytes(idx uint16) []byte {
	if idx >= q.Layout.Size {
		return nil
	}
	d := q.descSlice()
	off := int(idx) * VirtqDescriptorSize
	out := make([]byte, VirtqDescriptorSize)
	copy(out, d[off:off+VirtqDescriptorSize])
	return out
}

// AvailHeaderBytes returns a copy of the first 8 bytes of the avail-ring
// region (flags, idx, ring[0..1]). Exposed for diagnostic dumps.
func (q *Virtqueue) AvailHeaderBytes() []byte {
	a := q.availSlice()
	out := make([]byte, 8)
	if len(a) < 8 {
		copy(out, a)
		return out
	}
	copy(out, a[:8])
	return out
}

// UsedHeaderBytes returns a copy of the first 16 bytes of the used-ring
// region (flags, idx, ring[0]). Exposed for diagnostic dumps.
func (q *Virtqueue) UsedHeaderBytes() []byte {
	u := q.usedSlice()
	out := make([]byte, 16)
	if len(u) < 16 {
		copy(out, u)
		return out
	}
	copy(out, u[:16])
	return out
}

// UsedIdxRaw returns the raw bytes of the used-ring idx field
// (offset 2..4 — two bytes) without going through the atomic.LoadUint32
// path. Useful for cross-checking the atomic load against the raw
// memory view in case of cache-coherency questions on weakly-ordered
// arches.
func (q *Virtqueue) UsedIdxRaw() uint16 {
	u := q.usedSlice()
	return binary.LittleEndian.Uint16(u[2:4])
}

// Mem returns the underlying byte view of the virtqueue's backing
// memory. Exposed for advanced diagnostic / test scenarios that need to
// manipulate the bytes directly (e.g. simulating device-side writes in
// host tests).
func (q *Virtqueue) Mem() []byte { return q.mem }

// Sentinel errors for the virtqueue path.
var (
	ErrQueueFull         = commonError("go-virtio/common: virtqueue: descriptor table full")
	ErrInvalidIdx        = commonError("go-virtio/common: virtqueue: descriptor index out of range")
	ErrInvalidQueueSize  = commonError("go-virtio/common: virtqueue: queue size must be a non-zero power of two")
	ErrAllocReturnedZero = commonError("go-virtio/common: virtqueue: PageAllocator returned addr=0 with success")
)
