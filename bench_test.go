// Driver hot-path micro-benchmarks for the split-virtqueue ring code.
//
// These measure ONLY go-virtio's controllable overhead: the pure-Go data
// manipulation the driver performs per buffer/chain (descriptor writes,
// available-ring publication, used-ring polling). No real device, no
// hypervisor, no DMA — the device side is simulated in-memory where a
// completion is needed. The numbers are therefore "how cheap is OUR code",
// not an end-to-end virtio throughput figure (that is dominated by the host
// VMM + the backing device; see BENCHMARKS.md).
//
// Run:  GOWORK=off go test -run x -bench . -benchmem ./...
//
// Benchmarks live in a _test.go file so they do NOT affect the 99%
// statement-coverage gate (they only execute under -bench).

package common

import "testing"

// benchVirtqueue builds an in-heap virtqueue of the given size with no
// device involved. Mirrors newTestVirtqueue but for the *testing.B path.
func benchVirtqueue(size uint16) *Virtqueue {
	layout := ComputeVirtqueueLayout(size)
	mem := make([]byte, int(layout.TotalSize))
	return NewVirtqueueFromAlloc(0xDEADBEEF000, mem, size, 0)
}

// The device side is simulated by simulateDeviceUsed (defined in
// virtqueue_test.go), which writes one used-ring entry and bumps the
// used-ring idx the driver's PollUsed reads.

// --- Layout math -------------------------------------------------------

func BenchmarkComputeVirtqueueLayout(b *testing.B) {
	b.ReportAllocs()
	var sink VirtqueueLayout
	for i := 0; i < b.N; i++ {
		sink = ComputeVirtqueueLayout(256)
	}
	_ = sink
}

// --- Single-buffer publish (virtio-net TX/RX per-frame path) -----------

// BenchmarkAddBuffer measures one descriptor write + available-ring
// publication + bookkeeping. The slot is freed each iteration so the linear
// free-slot scan always hits index 0 (best case).
func BenchmarkAddBuffer(b *testing.B) {
	q := benchVirtqueue(256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx, err := q.AddBuffer(0x1000, 0x1000, 1518, false)
		if err != nil {
			b.Fatal(err)
		}
		_ = q.Reclaim(idx)
	}
}

// BenchmarkPostAvail isolates just the available-ring publication (the
// atomic release-store of idx). No descriptor write, no free-slot scan.
func BenchmarkPostAvail(b *testing.B) {
	q := benchVirtqueue(256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := q.PostAvail(0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAddBufferReclaimRoundtrip measures the full driver TX cycle for
// one frame against a simulated device: publish a buffer, poll the used
// ring (device completed it), reclaim the descriptor.
func BenchmarkAddBufferReclaimRoundtrip(b *testing.B) {
	q := benchVirtqueue(256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx, err := q.AddBuffer(0x1000, 0x1000, 1518, false)
		if err != nil {
			b.Fatal(err)
		}
		simulateDeviceUsed(q, idx, 1518)
		gotIdx, _, ok := q.PollUsed()
		if !ok {
			b.Fatal("PollUsed: device entry not observed")
		}
		_ = q.Reclaim(gotIdx)
	}
}

// --- Descriptor chain (virtio-blk request path) ------------------------

// benchChain3 is the canonical virtio-blk request shape: read-only header,
// read/write data, device-writable status byte.
var benchChain3 = []ChainBuffer{
	{Addr: 0x1000, Phys: 0x1000, Len: 16, Writable: false},
	{Addr: 0x2000, Phys: 0x2000, Len: 4096, Writable: true},
	{Addr: 0x3000, Phys: 0x3000, Len: 1, Writable: true},
}

// BenchmarkAddChain3 measures building a 3-descriptor chain (the virtio-blk
// header/data/status shape) + available-ring publication. The chain is
// reclaimed each iteration so the free-slot scan starts fresh.
func BenchmarkAddChain3(b *testing.B) {
	q := benchVirtqueue(256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		head, err := q.AddChain(benchChain3)
		if err != nil {
			b.Fatal(err)
		}
		_ = q.ReclaimChain(head)
	}
}

// BenchmarkAddChainReclaimRoundtrip3 measures the full virtio-blk request
// cycle against a simulated device: build chain, poll completion, walk the
// next-links to reclaim.
func BenchmarkAddChainReclaimRoundtrip3(b *testing.B) {
	q := benchVirtqueue(256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		head, err := q.AddChain(benchChain3)
		if err != nil {
			b.Fatal(err)
		}
		simulateDeviceUsed(q, head, 4096)
		gotIdx, _, ok := q.PollUsed()
		if !ok {
			b.Fatal("PollUsed: device entry not observed")
		}
		_ = q.ReclaimChain(gotIdx)
	}
}

// --- Used-ring read (RX/completion poll) -------------------------------

// BenchmarkUsedIdx isolates the atomic acquire-load of the device-side used
// idx — the hot read every poll loop spins on.
func BenchmarkUsedIdx(b *testing.B) {
	q := benchVirtqueue(256)
	simulateDeviceUsed(q, 0, 1518)
	b.ReportAllocs()
	b.ResetTimer()
	var sink uint16
	for i := 0; i < b.N; i++ {
		sink = q.UsedIdx()
	}
	_ = sink
}

// BenchmarkPollUsedEmpty measures the cost of a poll that finds nothing
// (the common busy-poll case while waiting on the device): one atomic load
// + one comparison, no state mutation.
func BenchmarkPollUsedEmpty(b *testing.B) {
	q := benchVirtqueue(256)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, ok := q.PollUsed(); ok {
			b.Fatal("PollUsed unexpectedly reported an entry")
		}
	}
}
