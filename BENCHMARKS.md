# Performance — go-virtio driver hot-path efficiency (2026-06-22)

go-virtio is a **guest-side** virtio driver: pure Go (CGO=0), running on
bare-metal/tamago or inside a VM. The split-virtqueue ring code in this
`common` package is the part of the data path we fully control — descriptor
writes, available-ring publication, used-ring polling, chain build/reclaim.
These benchmarks isolate *that* code with **no device, no hypervisor, no
DMA**: the device side is simulated in-memory only where a completion is
needed to drive `PollUsed`.

## What is and isn't comparable to "the reference"

The natural "reference" is the **Linux kernel's** `drivers/virtio/virtio_ring.c`.
A like-for-like throughput number against it is **not apples-to-apples** and
we deliberately do not fabricate one:

- **Different runtime/OS.** Our driver is Go in a guest; the kernel driver
  is C in ring-0 Linux. Same virtual device, but the surrounding stack
  (scheduler, IRQ vs. busy-poll, memory model) differs.
- **End-to-end virtio perf is dominated by the host VMM + the backing
  device**, not the guest ring code. virtio-net pps and virtio-blk IOPS are
  set by the hypervisor's vhost/backend and the physical NIC/disk, which are
  identical regardless of whose guest driver rings the doorbell. The guest
  ring manipulation is a *single-digit-nanosecond* slice of a microsecond-
  scale round trip.
- What we **can** measure fairly and report below: **our driver's
  controllable per-operation overhead** — ns/op, ops/s, allocs/op for each
  ring hot path. That is the number we can actually move with code changes.

Linux's `virtqueue_add` / `virtqueue_get_buf` are the structural analogues
of our `AddBuffer`/`AddChain` and `PollUsed`; both are a handful of
descriptor/ring writes plus one ordered index store/load. Our figures below
(0 allocs, ~3–26 ns) are in the same order of magnitude as that C code — but
we present them as **isolated-micro**, not a head-to-head claim.

## Methodology

- **CPU:** Apple M4 Max (16 logical CPUs). **OS:** macOS 26.5. **Go:** 1.26.4
  (`darwin/arm64`). **CGO_ENABLED=0**, `GOWORK=off`.
- **Isolated-micro:** every row is the guest ring code only; the device side
  is a simulated in-memory used-ring write where a completion is required.
  No syscalls, no real virtio device.
- Best-of-3 (`-count=3`), `-benchmem`. Ring size 256. Values are the median.
- Reproduce:
  `GOWORK=off CGO_ENABLED=0 go test -run '^$' -bench . -benchmem -count=3 ./...`
- Benchmarks live in `bench_test.go` (a `_test.go` file) so they do **not**
  affect the 99% statement-coverage CI gate — they execute only under `-bench`.

## Results (isolated-micro — our driver's controllable overhead)

| path | ns/op | ops/s | allocs/op | note |
|------|------:|------:|----------:|------|
| `ComputeVirtqueueLayout` | 2.29 | 436 M | 0 | pure offset math, per-queue (once at setup) |
| `UsedIdx` (atomic acquire-load) | 0.55 | 1.83 B | 0 | the value a busy-poll spins on |
| `PollUsed` (empty / no completion) | 1.55 | 645 M | 0 | the common wait-for-device poll |
| `PostAvail` (avail-ring publish) | 2.71 | 369 M | 0 | atomic release-store of `idx` |
| `AddBuffer` (1 desc + publish) | 5.50 | 182 M | 0 | virtio-net per-frame TX/RX publish |
| `AddBuffer`+`PollUsed`+`Reclaim` | 9.13 | 110 M | 0 | full 1-frame driver cycle vs. simulated device |
| `AddChain` (3 desc: hdr/data/status) | 20.8 | 48 M | 0 | virtio-blk request build + publish |
| `AddChain`+`PollUsed`+`ReclaimChain` | 25.8 | 39 M | 0 | full blk request cycle vs. simulated device |

Reference column intentionally omitted — see "What is and isn't
comparable" above. A fair kernel comparison would require driving the *same*
virtual device from both our guest driver and a Linux guest and measuring
end-to-end device throughput, where the guest ring code is not the
bottleneck.

## Summary

- **Zero allocations on every ring hot path.** No per-buffer or per-request
  garbage; the GC never runs because of the data path. This is the headline
  property for a guest driver that may run under tamago with a minimal heap.
- **Single-digit-nanosecond per-buffer cost.** Publishing a virtio-net frame
  is ~5.5 ns of guest CPU; a full TX cycle against an instant device is
  ~9 ns. A virtio-blk 3-descriptor request is ~21 ns to build, ~26 ns for a
  full cycle. Against real device round-trips (µs scale), our overhead is
  noise — the right place to be for a guest driver.
- **The atomic-ordered index store/load is the only synchronization** in the
  path (release-store on `avail.idx`, acquire-load on `used.idx`), correct on
  every Go arch including big-endian s390x (validated by the 6-arch CI).

### Action items (our controllable overhead)

1. **`AddChain` slot collection.** `AddChain` builds a `slots []uint16`
   scratch slice per call. It stays on the stack today (0 allocs measured),
   but a `[N]uint16` fixed array (chains are ≤ a few descriptors) would make
   that guarantee independent of escape analysis. **Low priority — already
   0-alloc.**
2. **Linear free-slot scan in `AddBuffer`/`AddChain`.** Both scan
   `Buffers[]` from index 0 for the first free slot — O(ring size) worst
   case under a full ring. A free-descriptor freelist (head index + `next`
   links, the layout the kernel uses) would make it O(1). Currently masked
   by always-free-slot-0 benchmarks; matters under sustained in-flight depth.
3. **Batched availability.** `PostAvail` does one atomic store per buffer.
   Posting N buffers then a single `idx` store (Virtio 1.1 §2.6.13 allows
   batching) would amortize the release-store across a burst — relevant for
   the rxq pre-post and any multi-frame TX path.
