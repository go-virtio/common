<p align="center"><img src="https://raw.githubusercontent.com/go-virtio/brand/main/social/go-virtio.png" alt="go-virtio/common" width="720"></p>

# go-virtio/common

[![Go Reference](https://pkg.go.dev/badge/github.com/go-virtio/common.svg)](https://pkg.go.dev/github.com/go-virtio/common)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD%203--Clause-blue.svg)](https://opensource.org/licenses/BSD-3-Clause)
[![CI](https://github.com/go-virtio/common/actions/workflows/ci.yml/badge.svg)](https://github.com/go-virtio/common/actions/workflows/ci.yml)

Transport-agnostic infrastructure for the `go-virtio` family of pure-Go
virtio drivers.

This package hosts the shared building blocks that every virtio device-class
driver needs and that do not themselves depend on a particular host
transport (UEFI, bare-metal MMIO, virtio-mmio, vhost-user, …):

  - **PCI capability walker** (`pci.go`) — parses the standard
    `struct virtio_pci_cap` chain published by every modern virtio
    device (Virtio 1.1 §4.1.4). Driven through a `PCIConfigReader`
    interface so the same walker covers any host.
  - **Modern transport layout** (`modern.go`) — the `ModernConfig`
    handle that pins the four required + one optional PCI capabilities
    (COMMON_CFG / NOTIFY_CFG / ISR_CFG / DEVICE_CFG / PCI_CFG) and the
    typed register accessors that route through a `BARMemoryAccessor`.
    Covers the full Virtio 1.1 §4.1.5 register table.
  - **Split-virtqueue layout + driver-side state machine**
    (`virtqueue.go`) — descriptor table, available ring, used ring,
    plus the `AddBuffer` / `PostAvail` / `PollUsed` / `Reclaim`
    bookkeeping. Backing pages come from a `PageAllocator`.
  - **Transport interfaces** (`transport.go`) — `PCIConfigReader`,
    `BARMemoryAccessor`, `PageAllocator`, `Transport`.

Mirrors the Linux kernel's `<linux/virtio.h>` shared-infrastructure
header pattern: per-device-class drivers (virtio-net, virtio-blk, …)
import this package for the transport-independent pieces and provide
their own spec-level driver on top.

## Sibling packages

  - [`github.com/go-virtio/net`](https://github.com/go-virtio/net) —
    pure-Go virtio-net (network device) driver.
  - [`github.com/go-virtio/blk`](https://github.com/go-virtio/blk) —
    pure-Go virtio-blk (block device) driver.
  - [`github.com/go-virtio/rng`](https://github.com/go-virtio/rng) —
    pure-Go virtio-rng (entropy) driver.
  - [`github.com/go-virtio/vsock`](https://github.com/go-virtio/vsock) —
    pure-Go virtio-vsock driver.
  - [`github.com/go-virtio/console`](https://github.com/go-virtio/console)
    — pure-Go virtio-console driver.
  - [`github.com/go-virtio/balloon`](https://github.com/go-virtio/balloon)
    — pure-Go virtio-balloon driver.
  - [`github.com/go-virtio/fs`](https://github.com/go-virtio/fs) —
    pure-Go virtio-fs (FUSE-over-virtio) driver.
  - [`github.com/go-virtio/input`](https://github.com/go-virtio/input) —
    pure-Go virtio-input driver.
  - [`github.com/go-virtio/sound`](https://github.com/go-virtio/sound) —
    pure-Go virtio-sound driver.
  - [`github.com/go-virtio/gpu`](https://github.com/go-virtio/gpu) —
    pure-Go virtio-gpu driver (2D framebuffer + virgl 3D + software
    rasterizer).
  - [`github.com/go-virtio/venus`](https://github.com/go-virtio/venus) —
    Vulkan-over-virtio (Venus) guest-side groundwork.
  - [`github.com/go-virtio/validate`](https://github.com/go-virtio/validate)
    — real-hardware validation harness.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
