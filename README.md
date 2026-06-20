<p align="center"><img src="https://raw.githubusercontent.com/go-virtio/brand/main/social/go-virtio.png" alt="go-virtio/common" width="720"></p>

# go-virtio/common

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
    placeholder for a future pure-Go virtio-blk driver.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
