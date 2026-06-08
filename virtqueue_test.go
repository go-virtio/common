// Tests for virtqueue.go.

package common

import (
	"encoding/binary"
	"errors"
	"testing"
)

// fakeAllocator returns Go-allocated pages. The physAddr it returns is
// the slice header's underlying address (not meaningful for the device,
// but uniqueness is all the tests care about).
type fakeAllocator struct {
	fail     bool
	zeroAddr bool
}

func (a *fakeAllocator) AllocatePages(count int) (uint64, []byte, error) {
	if a.fail {
		return 0, nil, errors.New("fake: alloc fail")
	}
	mem := make([]byte, count*int(PageSize))
	if a.zeroAddr {
		return 0, mem, nil
	}
	return 0xDEADBEEF000, mem, nil
}

// newTestVirtqueue allocates a backing buffer in Go heap memory and
// constructs a Virtqueue pointing at it.
func newTestVirtqueue(t *testing.T, size uint16, queueIdx uint16) *Virtqueue {
	t.Helper()
	layout := ComputeVirtqueueLayout(size)
	mem := make([]byte, int(layout.TotalSize))
	return NewVirtqueueFromAlloc(0xDEADBEEF, mem, size, queueIdx)
}

func TestComputeVirtqueueLayout_Size16(t *testing.T) {
	l := ComputeVirtqueueLayout(16)
	if l.Size != 16 {
		t.Errorf("Size: got %d, want 16", l.Size)
	}
	if l.DescTableOffset != 0 {
		t.Errorf("DescTableOffset: got %d, want 0", l.DescTableOffset)
	}
	if l.AvailRingOffset != 256 {
		t.Errorf("AvailRingOffset: got %d, want 256", l.AvailRingOffset)
	}
	if l.AvailUsedEventOffset != 256+36 {
		t.Errorf("AvailUsedEventOffset: got %d, want %d", l.AvailUsedEventOffset, 256+36)
	}
	if l.UsedRingOffset != 296 {
		t.Errorf("UsedRingOffset: got %d, want 296", l.UsedRingOffset)
	}
	if l.UsedAvailEventOffset != 296+132 {
		t.Errorf("UsedAvailEventOffset: got %d, want %d", l.UsedAvailEventOffset, 296+132)
	}
	if l.TotalSize != 296+132+2 {
		t.Errorf("TotalSize: got %d, want %d", l.TotalSize, 296+132+2)
	}
}

func TestComputeVirtqueueLayout_Size256(t *testing.T) {
	l := ComputeVirtqueueLayout(256)
	if l.AvailRingOffset != 4096 {
		t.Errorf("AvailRingOffset: got %d, want 4096", l.AvailRingOffset)
	}
	if l.AvailUsedEventOffset != 4096+516 {
		t.Errorf("AvailUsedEventOffset: got %d, want %d", l.AvailUsedEventOffset, 4096+516)
	}
}

func TestVirtqueue_DescriptorReadWrite(t *testing.T) {
	q := newTestVirtqueue(t, 16, 0)
	if err := q.writeDescriptor(3, 0x1000, 0x800, VirtqDescFWrite, 7); err != nil {
		t.Fatalf("writeDescriptor: %v", err)
	}
	addr, length, flags, next, err := q.readDescriptor(3)
	if err != nil {
		t.Fatalf("readDescriptor: %v", err)
	}
	if addr != 0x1000 || length != 0x800 || flags != VirtqDescFWrite || next != 7 {
		t.Errorf("got (0x%x, 0x%x, 0x%x, %d)", addr, length, flags, next)
	}
}

func TestVirtqueue_DescriptorInvalidIdx(t *testing.T) {
	q := newTestVirtqueue(t, 8, 0)
	if err := q.writeDescriptor(8, 0, 0, 0, 0); !errors.Is(err, ErrInvalidIdx) {
		t.Errorf("writeDescriptor(8) size=8: got %v", err)
	}
	if _, _, _, _, err := q.readDescriptor(99); !errors.Is(err, ErrInvalidIdx) {
		t.Errorf("readDescriptor(99): got %v", err)
	}
}

func TestVirtqueue_AddBufferAndPostAvail(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	idx, err := q.AddBuffer(0xCAFE, 0x100000, 1500, true)
	if err != nil {
		t.Fatalf("AddBuffer: %v", err)
	}
	if idx != 0 {
		t.Errorf("idx: got %d, want 0", idx)
	}
	if q.NextAvailIdx() != 1 {
		t.Errorf("nextAvailIdx: got %d, want 1", q.NextAvailIdx())
	}
	if !q.Buffers[0].InUse {
		t.Errorf("Buffers[0].InUse: false")
	}
	_, _, flags, _, _ := q.readDescriptor(0)
	if flags&VirtqDescFWrite == 0 {
		t.Errorf("descriptor 0 flags missing VIRTQ_DESC_F_WRITE")
	}
	if q.AvailIdx() != 1 {
		t.Errorf("avail.idx: got %d, want 1", q.AvailIdx())
	}
}

func TestVirtqueue_AddBufferQueueFull(t *testing.T) {
	q := newTestVirtqueue(t, 2, 0)
	if _, err := q.AddBuffer(0, 0, 100, false); err != nil {
		t.Fatalf("1: %v", err)
	}
	if _, err := q.AddBuffer(0, 0, 100, false); err != nil {
		t.Fatalf("2: %v", err)
	}
	if _, err := q.AddBuffer(0, 0, 100, false); !errors.Is(err, ErrQueueFull) {
		t.Errorf("3: got %v, want ErrQueueFull", err)
	}
}

func TestVirtqueue_AddBufferReclaimReusesSlot(t *testing.T) {
	q := newTestVirtqueue(t, 2, 0)
	idx0, _ := q.AddBuffer(0, 0, 100, false)
	idx1, _ := q.AddBuffer(0, 0, 100, false)
	if idx0 != 0 || idx1 != 1 {
		t.Fatalf("got (%d, %d), want (0, 1)", idx0, idx1)
	}
	if err := q.Reclaim(0); err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if q.Buffers[0].InUse {
		t.Errorf("after Reclaim: InUse=true")
	}
	idx2, _ := q.AddBuffer(0, 0, 100, false)
	if idx2 != 0 {
		t.Errorf("after reclaim: got idx=%d, want 0", idx2)
	}
}

func TestVirtqueue_PostAvailInvalidIdx(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	if err := q.PostAvail(99); !errors.Is(err, ErrInvalidIdx) {
		t.Errorf("PostAvail(99): got %v", err)
	}
}

func TestVirtqueue_ReclaimInvalidIdx(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	if err := q.Reclaim(99); !errors.Is(err, ErrInvalidIdx) {
		t.Errorf("Reclaim(99): got %v", err)
	}
}

func simulateDeviceUsed(q *Virtqueue, descIdx uint16, length uint32) {
	u := q.usedSlice()
	usedIdx := binary.LittleEndian.Uint16(u[2:4])
	slot := int(VirtqUsedHeaderSize) + (int(usedIdx)%int(q.Layout.Size))*VirtqUsedRingEntrySize
	binary.LittleEndian.PutUint32(u[slot:slot+4], uint32(descIdx))
	binary.LittleEndian.PutUint32(u[slot+4:slot+8], length)
	binary.LittleEndian.PutUint16(u[2:4], usedIdx+1)
}

func TestVirtqueue_PollUsedDrainsOneEntry(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	_, _ = q.AddBuffer(0, 0, 100, false)
	_, _ = q.AddBuffer(0, 0, 100, false)
	simulateDeviceUsed(q, 0, 100)
	simulateDeviceUsed(q, 1, 200)
	idx, length, ok := q.PollUsed()
	if !ok || idx != 0 || length != 100 {
		t.Errorf("PollUsed 1: got (%d, %d, %v)", idx, length, ok)
	}
	idx, length, ok = q.PollUsed()
	if !ok || idx != 1 || length != 200 {
		t.Errorf("PollUsed 2: got (%d, %d, %v)", idx, length, ok)
	}
	if _, _, ok := q.PollUsed(); ok {
		t.Errorf("PollUsed 3: ok=true, want false")
	}
}

func TestVirtqueue_PollUsedRingWrap(t *testing.T) {
	q := newTestVirtqueue(t, 2, 0)
	for round := 0; round < 5; round++ {
		_, _ = q.AddBuffer(0, 0, 100, false)
		_, _ = q.AddBuffer(0, 0, 100, false)
		simulateDeviceUsed(q, 0, 100)
		simulateDeviceUsed(q, 1, 200)
		idx0, _, ok := q.PollUsed()
		if !ok || idx0 != 0 {
			t.Errorf("round %d: got (%d, ok=%v)", round, idx0, ok)
		}
		idx1, _, ok := q.PollUsed()
		if !ok || idx1 != 1 {
			t.Errorf("round %d: got (%d, ok=%v)", round, idx1, ok)
		}
		_ = q.Reclaim(0)
		_ = q.Reclaim(1)
	}
	if q.LastSeenUsedIdx() != 10 {
		t.Errorf("after 5 rounds: lastSeenUsedIdx = %d, want 10", q.LastSeenUsedIdx())
	}
}

func TestVirtqueue_AvailFlags(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	if got := q.AvailFlags(); got != 0 {
		t.Errorf("default: got 0x%x", got)
	}
	a := q.availSlice()
	binary.LittleEndian.PutUint16(a[0:2], 0x0001)
	if got := q.AvailFlags(); got != 0x0001 {
		t.Errorf("after set: got 0x%x", got)
	}
}

func TestVirtqueue_UsedRingAtWrapping(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	u := q.usedSlice()
	slot6 := int(VirtqUsedHeaderSize) + 2*VirtqUsedRingEntrySize
	binary.LittleEndian.PutUint32(u[slot6:slot6+4], 0xAAAA)
	binary.LittleEndian.PutUint32(u[slot6+4:slot6+8], 0xBBBB)
	id, length := q.UsedRingAt(6)
	if id != 0xAAAA || length != 0xBBBB {
		t.Errorf("UsedRingAt(6): got (0x%x, 0x%x)", id, length)
	}
}

func TestVirtqueue_DescBytes(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	if err := q.writeDescriptor(2, 0x123456789abcdef0, 0xCAFE, VirtqDescFWrite, 0x55); err != nil {
		t.Fatalf("writeDescriptor: %v", err)
	}
	got := q.DescBytes(2)
	if len(got) != VirtqDescriptorSize {
		t.Fatalf("DescBytes len: got %d", len(got))
	}
	if a := binary.LittleEndian.Uint64(got[0:8]); a != 0x123456789abcdef0 {
		t.Errorf("addr: got 0x%x", a)
	}
	if l := binary.LittleEndian.Uint32(got[8:12]); l != 0xCAFE {
		t.Errorf("len: got 0x%x", l)
	}
	if f := binary.LittleEndian.Uint16(got[12:14]); f != VirtqDescFWrite {
		t.Errorf("flags: got 0x%x", f)
	}
	if n := binary.LittleEndian.Uint16(got[14:16]); n != 0x55 {
		t.Errorf("next: got 0x%x", n)
	}
	if q.DescBytes(99) != nil {
		t.Errorf("DescBytes(99): want nil")
	}
}

func TestVirtqueue_AvailHeaderBytes(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	if _, err := q.AddBuffer(0, 0xABCD, 16, false); err != nil {
		t.Fatalf("AddBuffer: %v", err)
	}
	got := q.AvailHeaderBytes()
	if len(got) != 8 {
		t.Fatalf("len: %d", len(got))
	}
	if idx := binary.LittleEndian.Uint16(got[2:4]); idx != 1 {
		t.Errorf("idx: %d", idx)
	}
	if ring0 := binary.LittleEndian.Uint16(got[4:6]); ring0 != 0 {
		t.Errorf("ring[0]: %d", ring0)
	}
}

func TestVirtqueue_UsedHeaderBytes(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	u := q.usedSlice()
	binary.LittleEndian.PutUint16(u[0:2], 0)
	binary.LittleEndian.PutUint16(u[2:4], 1)
	binary.LittleEndian.PutUint32(u[4:8], 0x42)
	binary.LittleEndian.PutUint32(u[8:12], 0x100)
	got := q.UsedHeaderBytes()
	if len(got) != 16 {
		t.Fatalf("len: %d", len(got))
	}
	if idx := binary.LittleEndian.Uint16(got[2:4]); idx != 1 {
		t.Errorf("idx: %d", idx)
	}
	if id := binary.LittleEndian.Uint32(got[4:8]); id != 0x42 {
		t.Errorf("id: 0x%x", id)
	}
	if l := binary.LittleEndian.Uint32(got[8:12]); l != 0x100 {
		t.Errorf("len: 0x%x", l)
	}
}

func TestVirtqueue_UsedIdxRaw(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	u := q.usedSlice()
	binary.LittleEndian.PutUint16(u[2:4], 0xABCD)
	if got := q.UsedIdxRaw(); got != 0xABCD {
		t.Errorf("UsedIdxRaw: got 0x%x", got)
	}
	if got := q.UsedIdx(); got != 0xABCD {
		t.Errorf("UsedIdx: got 0x%x", got)
	}
}

func TestNewVirtqueue_PowerOfTwoCheck(t *testing.T) {
	a := &fakeAllocator{}
	if _, err := NewVirtqueue(a, 0, 0, 0); !errors.Is(err, ErrInvalidQueueSize) {
		t.Errorf("size=0: got %v", err)
	}
	if _, err := NewVirtqueue(a, 3, 0, 0); !errors.Is(err, ErrInvalidQueueSize) {
		t.Errorf("size=3: got %v", err)
	}
	if _, err := NewVirtqueue(a, 5, 0, 0); !errors.Is(err, ErrInvalidQueueSize) {
		t.Errorf("size=5: got %v", err)
	}
}

func TestNewVirtqueue_AllocFailure(t *testing.T) {
	a := &fakeAllocator{fail: true}
	if _, err := NewVirtqueue(a, 4, 0, 0); err == nil {
		t.Errorf("expected alloc error")
	}
}

func TestNewVirtqueue_AllocReturnsZero(t *testing.T) {
	a := &fakeAllocator{zeroAddr: true}
	if _, err := NewVirtqueue(a, 4, 0, 0); !errors.Is(err, ErrAllocReturnedZero) {
		t.Errorf("got %v, want ErrAllocReturnedZero", err)
	}
}

func TestNewVirtqueue_Success(t *testing.T) {
	a := &fakeAllocator{}
	q, err := NewVirtqueue(a, 16, 1, 5)
	if err != nil {
		t.Fatalf("NewVirtqueue: %v", err)
	}
	if q.NotifyOff != 5 {
		t.Errorf("NotifyOff: got %d, want 5", q.NotifyOff)
	}
	if q.Index != 1 {
		t.Errorf("Index: got %d, want 1", q.Index)
	}
	if q.BasePhys == 0 {
		t.Errorf("BasePhys: got 0")
	}
	if q.Mem() == nil {
		t.Errorf("Mem(): nil")
	}
	if len(q.Buffers) != 16 {
		t.Errorf("Buffers len: %d", len(q.Buffers))
	}
}

func TestVirtqueue_AddBufferInvalidDescriptorIdx(t *testing.T) {
	q := newTestVirtqueue(t, 4, 0)
	if err := q.writeDescriptor(5, 0, 0, 0, 0); !errors.Is(err, ErrInvalidIdx) {
		t.Errorf("writeDescriptor(5) size=4: got %v", err)
	}
}

func TestVirtqDescFConstants(t *testing.T) {
	if VirtqDescFNext != 0x1 {
		t.Errorf("VirtqDescFNext: got 0x%x, want 0x1", VirtqDescFNext)
	}
	if VirtqDescFWrite != 0x2 {
		t.Errorf("VirtqDescFWrite: got 0x%x, want 0x2", VirtqDescFWrite)
	}
	if VirtqDescFIndirect != 0x4 {
		t.Errorf("VirtqDescFIndirect: got 0x%x, want 0x4", VirtqDescFIndirect)
	}
}
