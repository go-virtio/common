// Tests for transport.go — interface conformance + commonError sentinel.

package common

import (
	"errors"
	"testing"
)

// Compile-time interface conformance assertions. The fakes in
// modern_test.go must implement the three transport interfaces.
var (
	_ PCIConfigReader   = (*memDevice)(nil)
	_ BARMemoryAccessor = (*memBAR)(nil)
	_ BARMemoryAccessor = (*featureBAR)(nil)
	_ PageAllocator     = (*fakeAllocator)(nil)
)

// `memDevice` also satisfies Transport (embeds memBAR and implements
// PCIConfigReader + PageAllocator).
var _ Transport = (*memDevice)(nil)

func TestCommonError(t *testing.T) {
	e := commonError("test message")
	if e.Error() != "test message" {
		t.Errorf("Error() = %q, want %q", e.Error(), "test message")
	}
	// errors.Is sentinel match.
	if !errors.Is(e, e) {
		t.Errorf("errors.Is(e, e) = false")
	}
}
