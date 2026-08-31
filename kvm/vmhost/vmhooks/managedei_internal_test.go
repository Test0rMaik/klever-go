package vmhooks

import (
	"bytes"
	"encoding/hex"
	"testing"

	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/kvm/config"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	hostmock "github.com/klever-io/klever-go/kvm/vmhost/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockVMHostForHexAndBuiltinFunction builds a lightweight host (mocked ManagedTypes and
// Metering, real gas math) for testing ManagedBufferToHex and ManagedIsBuiltinFunction's gas
// scaling and size-cap behavior directly, without needing a full instance/contract call. This
// file is package vmhooks (not vmhooks_test) specifically so these tests can reference
// maxManagedBufferToHexLength and maxBuiltinFunctionNameLength directly rather than keeping a
// separate copy that could silently drift from the real constants (KLC-2566 / Certik KLR-43).
// newMockVMHostForHexAndBuiltinFunction builds the host with DataCopyPerByte: 1, so the byte
// counts these tests assert read directly as byte counts. Use
// newMockVMHostForHexAndBuiltinFunctionWithPerByteCost to assert cost under the shipped
// schedule instead.
func newMockVMHostForHexAndBuiltinFunction(fixAuditChangesV5 bool) (host *contextmock.VMHostMock, managedTypes *hostmock.ManagedTypesContextMock, gasUsed func() uint64, bytesGasUsed func() uint64, runtimeErr *error) {
	return newMockVMHostForHexAndBuiltinFunctionWithPerByteCost(fixAuditChangesV5, 1)
}

// newMockVMHostForHexAndBuiltinFunctionWithPerByteCost drives ConsumeGasForBytes off the gas
// schedule instead of counting raw bytes, so a case can assert the concrete cost under the
// per-byte price the fork will actually activate under (gasScheduleV1.yaml: DataCopyPerByte 50).
func newMockVMHostForHexAndBuiltinFunctionWithPerByteCost(fixAuditChangesV5 bool, dataCopyPerByte uint64) (host *contextmock.VMHostMock, managedTypes *hostmock.ManagedTypesContextMock, gasUsed func() uint64, bytesGasUsed func() uint64, runtimeErr *error) {
	var consumedBytesGas uint64

	managedTypes = &hostmock.ManagedTypesContextMock{
		ConsumeGasForBytesCalled: func(bytes []byte) {
			consumedBytesGas += uint64(len(bytes)) * dataCopyPerByte
		},
	}

	metering := &contextmock.MeteringContextMock{
		GasCost: &config.GasCost{
			ManagedBufferAPICost: config.ManagedBufferAPICost{
				MBufferSetBytes: 2000,
			},
			BaseOpsAPICost: config.BaseOpsAPICost{
				IsBuiltinFunction: 10,
			},
			BaseOperationCost: config.BaseOperationCost{
				DataCopyPerByte: dataCopyPerByte,
			},
		},
		GasLeftMock:     1_000_000_000,
		GasProvidedMock: 1_000_000_000,
	}

	runtimeErr = new(error)
	host = &contextmock.VMHostMock{
		ManagedTypesContext:   managedTypes,
		MeteringContext:       metering,
		ForkControllerContext: &commonMock.ForkControllerStub{FixAuditChangesV5Value: fixAuditChangesV5},
		RuntimeContext: &contextmock.RuntimeContextWrapper{
			BaseOpsErrorShouldFailExecutionFunc: func() bool {
				return true
			},
			FailExecutionFunc: func(err error) {
				*runtimeErr = err
			},
		},
	}

	gasUsed = func() uint64 {
		return (metering.GasProvidedMock - metering.GasLeftMock) + consumedBytesGas
	}
	bytesGasUsed = func() uint64 {
		return consumedBytesGas
	}

	return host, managedTypes, gasUsed, bytesGasUsed, runtimeErr
}

func TestManagedBufferToHexWithHost_GasCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		purpose     string
		source      []byte
		expectedGas uint64
	}{
		{
			purpose:     "small buffer: base cost + 3x per-byte charge (N read + 2N write)",
			source:      []byte{1, 2, 3},
			expectedGas: 2000 + 3*3,
		},
		{
			purpose:     "larger buffer scales linearly",
			source:      bytes.Repeat([]byte{7}, 100),
			expectedGas: 2000 + 3*100,
		},
	}

	for _, tt := range cases {
		t.Run(tt.purpose, func(t *testing.T) {
			host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunction(true)

			var setBytes []byte
			managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return tt.source, nil }
			managedTypes.SetBytesCalled = func(_ int32, b []byte) { setBytes = b }

			ManagedBufferToHexWithHost(host, 1, 2)

			assert.Nil(t, *runtimeErr)
			assert.Equal(t, tt.expectedGas, gasUsed())
			assert.Equal(t, hex.EncodeToString(tt.source), string(setBytes))
		})
	}
}

func TestManagedBufferToHexWithHost_AcceptsExactBoundary(t *testing.T) {
	t.Parallel()

	atCap := bytes.Repeat([]byte{7}, maxManagedBufferToHexLength)

	host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunction(true)
	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return atCap, nil }
	var setBytes []byte
	managedTypes.SetBytesCalled = func(_ int32, b []byte) { setBytes = b }

	ManagedBufferToHexWithHost(host, 1, 2)

	assert.Nil(t, *runtimeErr)
	assert.Equal(t, hex.EncodeToString(atCap), string(setBytes))
	assert.Equal(t, uint64(2000+3*maxManagedBufferToHexLength), gasUsed(), "a source exactly at the cap must succeed, not just under it")
}

func TestManagedBufferToHexWithHost_RejectsOversizedBuffer(t *testing.T) {
	t.Parallel()

	oversized := bytes.Repeat([]byte{7}, maxManagedBufferToHexLength+1)

	host, managedTypes, _, bytesGasUsed, runtimeErr := newMockVMHostForHexAndBuiltinFunction(true)
	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return oversized, nil }
	setBytesCalled := false
	managedTypes.SetBytesCalled = func(int32, []byte) { setBytesCalled = true }

	ManagedBufferToHexWithHost(host, 1, 2)

	require.NotNil(t, *runtimeErr)
	assert.ErrorIs(t, *runtimeErr, vmhost.ErrManagedBufferToHexLengthExceedsMaximum)
	assert.False(t, setBytesCalled, "must fail before allocating/writing the output")
	// bytesGasUsed tracks ConsumeGasForBytesCalled specifically, which ToHex no longer uses at
	// all (its per-byte charge goes through UseGasBounded directly) - this stays 0 for both
	// rejected and successful calls, so it isn't meaningful gas evidence here on its own; the
	// real proof this rejects before any per-byte work is setBytesCalled being false above.
	// (Total gasUsed() isn't useful post-rejection either: WithFaultAndHost burns all
	// remaining gas on failure, so it would read as ~full provided gas regardless of how much
	// was actually charged before the fault.)
	assert.Equal(t, uint64(0), bytesGasUsed())
}

// TestManagedBufferToHexWithHost_InsufficientGasFailsBeforeAllocation regression-tests the
// fix for a bug where the per-byte charge was accounted via ConsumeGasForBytes, which never
// fails on insufficient gas - so a call landing exactly on empty gas would still run the full
// encode/allocate/write unpaid, with the trap only firing on the next metered block. Both
// charges now go through UseGasBounded, which must fail atomically, before SetBytes ever runs.
func TestManagedBufferToHexWithHost_InsufficientGasFailsBeforeAllocation(t *testing.T) {
	t.Parallel()

	source := bytes.Repeat([]byte{7}, 100) // needs 2000 (base) + 300 (3x per-byte) to succeed

	host, managedTypes, _, _, runtimeErr := newMockVMHostForHexAndBuiltinFunction(true)
	metering := host.MeteringContext.(*contextmock.MeteringContextMock)
	metering.GasProvidedMock = 2050 // covers the flat base cost, not the per-byte charge
	metering.GasLeftMock = 2050

	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return source, nil }
	setBytesCalled := false
	managedTypes.SetBytesCalled = func(int32, []byte) { setBytesCalled = true }

	ManagedBufferToHexWithHost(host, 1, 2)

	require.NotNil(t, *runtimeErr)
	assert.ErrorIs(t, *runtimeErr, vmhost.ErrNotEnoughGas)
	assert.False(t, setBytesCalled, "must fail before allocating/writing the output")
}

func TestManagedBufferToHexWithHost_PreForkAllowsOversizedBuffer(t *testing.T) {
	t.Parallel()

	oversized := bytes.Repeat([]byte{7}, maxManagedBufferToHexLength+1)

	host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunction(false)
	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return oversized, nil }
	var setBytes []byte
	managedTypes.SetBytesCalled = func(_ int32, b []byte) { setBytes = b }

	ManagedBufferToHexWithHost(host, 1, 2)

	assert.Nil(t, *runtimeErr)
	assert.Equal(t, hex.EncodeToString(oversized), string(setBytes))
	assert.Equal(t, uint64(2000), gasUsed(), "pre-fork: flat cost only, no per-byte metering")
}

func TestManagedIsBuiltinFunctionWithHost_GasCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		purpose     string
		name        []byte
		expectedGas uint64
	}{
		{
			purpose:     "short name: base cost + per-byte charge",
			name:        []byte("KDATransfer"),
			expectedGas: 10 + 11,
		},
		{
			purpose:     "longer name scales linearly",
			name:        bytes.Repeat([]byte{'a'}, 200),
			expectedGas: 10 + 200,
		},
		{
			purpose:     "exactly one under the cap: still the normal, uncapped charge",
			name:        bytes.Repeat([]byte{'a'}, maxBuiltinFunctionNameLength-1),
			expectedGas: 10 + (maxBuiltinFunctionNameLength - 1),
		},
	}

	for _, tt := range cases {
		t.Run(tt.purpose, func(t *testing.T) {
			host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunction(true)
			managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return tt.name, nil }

			ManagedIsBuiltinFunctionWithHost(host, 1)

			assert.Nil(t, *runtimeErr)
			assert.Equal(t, tt.expectedGas, gasUsed())
		})
	}
}

// TestManagedIsBuiltinFunctionWithHost_CapsGasAndReturnsFalseAboveMax covers the gas cap and
// the behavior change from reverting the whole tx to returning a plain "not a builtin" (0):
// this is a pure predicate, and a name at or over the cap can never be a real builtin function
// name, so reverting would be a new failure mode this call never had pre-fork, for input that
// used to just return false.
//
// It deliberately does NOT claim to pin the >= vs > boundary. Now that an oversized name
// returns 0 rather than reverting, the two spellings are indistinguishable at exactly 256 -
// same result, same capped charge - so this case exercises the boundary without being able to
// fail on it. See the comment on maxBuiltinFunctionNameLength.
func TestManagedIsBuiltinFunctionWithHost_CapsGasAndReturnsFalseAboveMax(t *testing.T) {
	t.Parallel()

	cases := []struct {
		purpose string
		name    []byte
	}{
		{purpose: "exactly at the cap (behavior only - see doc comment, not a >=/> discriminator)", name: bytes.Repeat([]byte{'a'}, maxBuiltinFunctionNameLength)},
		{purpose: "well over the cap", name: bytes.Repeat([]byte{'a'}, maxBuiltinFunctionNameLength+44)},
	}

	for _, tt := range cases {
		t.Run(tt.purpose, func(t *testing.T) {
			host, managedTypes, _, bytesGasUsed, runtimeErr := newMockVMHostForHexAndBuiltinFunction(true)
			managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return tt.name, nil }

			result := ManagedIsBuiltinFunctionWithHost(host, 1)

			assert.Nil(t, *runtimeErr, "must not revert the tx - this is a pure predicate")
			assert.Equal(t, int32(0), result, "a name at/over the cap can never be a real builtin")
			assert.Equal(t, uint64(maxBuiltinFunctionNameLength), bytesGasUsed(), "the charge must stay capped regardless of the actual input length")
		})
	}
}

func TestManagedIsBuiltinFunctionWithHost_PreForkAllowsOversizedName(t *testing.T) {
	t.Parallel()

	oversized := bytes.Repeat([]byte{'a'}, maxBuiltinFunctionNameLength+1)

	host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunction(false)
	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return oversized, nil }

	result := ManagedIsBuiltinFunctionWithHost(host, 1)

	assert.Nil(t, *runtimeErr)
	assert.Equal(t, int32(0), result)
	assert.Equal(t, uint64(10), gasUsed(), "pre-fork: flat cost only, no per-byte metering")
}

// shippedDataCopyPerByte is the per-byte price in config/node/gasScheduleV1.yaml. Every other
// gas assertion in this file runs with 1, so those numbers read as byte counts; the two tests
// below pin the concrete cost under the schedule this fork actually activates against, so a
// change to the per-byte price or to which schedule field is charged fails here.
const shippedDataCopyPerByte = 50

func TestManagedBufferToHexWithHost_GasCostUnderShippedSchedule(t *testing.T) {
	t.Parallel()

	source := bytes.Repeat([]byte{0xab}, 32) // a 32-byte hash, the common case

	host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunctionWithPerByteCost(true, shippedDataCopyPerByte)
	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return source, nil }
	managedTypes.SetBytesCalled = func(int32, []byte) {}

	ManagedBufferToHexWithHost(host, 1, 2)

	assert.Nil(t, *runtimeErr)
	// 2000 base + 3x32 bytes (N read + 2N written as hex) at 50/byte.
	assert.Equal(t, uint64(2000+3*32*shippedDataCopyPerByte), gasUsed())
}

func TestManagedIsBuiltinFunctionWithHost_GasCostUnderShippedSchedule(t *testing.T) {
	t.Parallel()

	name := []byte("KDATransfer")

	host, managedTypes, gasUsed, _, runtimeErr := newMockVMHostForHexAndBuiltinFunctionWithPerByteCost(true, shippedDataCopyPerByte)
	managedTypes.GetBytesCalled = func(int32) ([]byte, error) { return name, nil }

	ManagedIsBuiltinFunctionWithHost(host, 1)

	assert.Nil(t, *runtimeErr)
	// 10 base + 11 bytes at 50/byte.
	assert.Equal(t, uint64(10+11*shippedDataCopyPerByte), gasUsed())
}
