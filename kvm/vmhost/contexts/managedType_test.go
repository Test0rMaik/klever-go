package contexts

import (
	"bytes"
	"crypto/elliptic"
	"math/big"
	"testing"

	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/tools/check"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManagedTypes(t *testing.T) {
	t.Parallel()

	host := &contextmock.VMHostStub{}

	managedTypesCtx, err := NewManagedTypesContext(host)
	currentStateValues := managedTypesCtx.managedTypesValues

	require.Nil(t, err)
	require.False(t, managedTypesCtx.IsInterfaceNil())
	require.NotNil(t, currentStateValues.bigIntValues)
	require.NotNil(t, currentStateValues.bigFloatValues)
	require.NotNil(t, currentStateValues.ecValues)
	require.NotNil(t, currentStateValues.mBufferValues)
	require.NotNil(t, managedTypesCtx.managedTypesStack)
	require.Equal(t, 0, len(currentStateValues.bigIntValues))
	require.Equal(t, 0, len(currentStateValues.bigFloatValues))
	require.Equal(t, 0, len(currentStateValues.ecValues))
	require.Equal(t, 0, len(currentStateValues.mBufferValues))
	require.Equal(t, 0, len(managedTypesCtx.managedTypesStack))
}

func TestManagedTypesContext_Randomness(t *testing.T) {
	t.Parallel()

	mockRuntime := &contextmock.RuntimeContextMock{
		CurrentTxHash: []byte{0xf, 0xf, 0xf, 0xf, 0xf, 0xf},
	}
	host := &contextmock.VMHostMock{
		RuntimeContext: mockRuntime,
	}
	mockBlockchain := &contextmock.BlockchainHookStub{
		CurrentRandomSeedCalled: func() []byte {
			return []byte{0xf, 0xf, 0xf, 0xf, 0xa, 0xb}
		},
	}
	blockchainCtx, _ := NewBlockchainContext(host, mockBlockchain)
	host.BlockchainContext = blockchainCtx
	copyHost := host

	managedTypesCtx, _ := NewManagedTypesContext(host)
	require.Nil(t, managedTypesCtx.randomnessGenerator)
	managedTypesCtx.initRandomizer()
	firstRandomizer := managedTypesCtx.randomnessGenerator

	managedTypesCtxCopy, _ := NewManagedTypesContext(copyHost)
	require.Nil(t, managedTypesCtxCopy.randomnessGenerator)
	managedTypesCtxCopy.initRandomizer()
	secondRandomizer := managedTypesCtxCopy.randomnessGenerator

	require.Equal(t, firstRandomizer, secondRandomizer)

	prg := managedTypesCtx.GetRandReader()
	a := make([]byte, 100)
	_, _ = prg.Read(a)
	b := make([]byte, 100)
	for i := 0; i < 1000; i++ {
		_, _ = prg.Read(b)
		require.NotEqual(t, a, b)
		copy(a, b)
	}
}

func TestManagedTypesContext_ClearStateStack(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{
		BlockchainCalled: func() vmhost.BlockchainContext {
			return &contextmock.BlockchainContextMock{}
		},
		RuntimeCalled: func() vmhost.RuntimeContext {
			return &contextmock.RuntimeContextMock{CurrentTxHash: bytes.Repeat([]byte{1}, 32)}
		},
	}
	intValue1, intValue2 := int64(100), int64(200)
	floatValue1, floatValue2 := 307.72, 78.008
	p224ec, p256ec := elliptic.P224().Params(), elliptic.P256().Params()
	managedTypesCtx, _ := NewManagedTypesContext(host)
	managedTypesCtx.InitState()

	bigIntHandle1 := managedTypesCtx.NewBigIntFromInt64(intValue1)
	bigIntHandle2 := managedTypesCtx.NewBigIntFromInt64(intValue2)
	bigFloatHandle1, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue1))
	bigFloatHandle2, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue2))
	ecHandle1 := managedTypesCtx.PutEllipticCurve(p224ec)
	ecHandle2 := managedTypesCtx.PutEllipticCurve(p256ec)

	bigFloatHandle3, _ := managedTypesCtx.PutBigFloat(nil)
	bigFloat3, _ := managedTypesCtx.GetBigFloat(bigFloatHandle3)
	require.Equal(t, big.NewFloat(0), bigFloat3)

	_ = managedTypesCtx.GetRandReader()
	assert.False(t, check.IfNil(managedTypesCtx.randomnessGenerator))
	managedTypesCtx.PushState()
	require.Equal(t, 1, len(managedTypesCtx.managedTypesStack))
	managedTypesCtx.ClearStateStack()
	require.Equal(t, 0, len(managedTypesCtx.managedTypesStack))
	assert.True(t, check.IfNil(managedTypesCtx.randomnessGenerator))

	bigInt1, err := managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Nil(t, err)
	bigInt2, err := managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)
	bigFloat1, err := managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Nil(t, err)
	bigFloat2, err := managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)
	ec1, err := managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, err)
	require.Equal(t, p224ec, ec1)
	ec2, err := managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, err)
	require.Equal(t, p256ec, ec2)

	managedTypesCtx.InitState()
	bigInt1, err = managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Nil(t, bigInt1)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt2, err = managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Nil(t, bigInt2)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigFloat1, err = managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Nil(t, bigFloat1)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat2, err = managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Nil(t, bigFloat2)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	ec1, err = managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, ec1)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)
	ec2, err = managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, ec2)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)
}

func TestManagedTypesContext_InitPushPopState(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}
	intValue1, intValue2, intValue3 := int64(100), int64(200), int64(-42)
	floatValue1, floatValue2, floatValue3 := 307.72, 78.008, -37.84732
	p224ec, p256ec, p384ec, p521ec := elliptic.P224().Params(), elliptic.P256().Params(), elliptic.P384().Params(), elliptic.P521().Params()
	mBytes := []byte{2, 234, 64, 255}
	managedTypesCtx, _ := NewManagedTypesContext(host)
	managedTypesCtx.InitState()

	// Create 2 bigInt, 2 bigFloat, 2 EC, 2 managedBuffers on the active state
	bigIntHandle1 := managedTypesCtx.NewBigIntFromInt64(intValue1)
	require.Equal(t, int32(0), bigIntHandle1)
	bigIntHandle2 := managedTypesCtx.NewBigIntFromInt64(intValue2)
	require.Equal(t, int32(1), bigIntHandle2)

	bigInt1, err := managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Nil(t, err)
	bigInt2, err := managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)

	bigFloatHandle1, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue1))
	require.Equal(t, int32(0), bigFloatHandle1)
	bigFloatHandle2, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue2))
	require.Equal(t, int32(1), bigFloatHandle2)

	bigFloat1, err := managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Nil(t, err)
	bigFloat2, err := managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)

	ecHandle1 := managedTypesCtx.PutEllipticCurve(p224ec)
	require.Equal(t, int32(0), ecHandle1)
	ecHandle2 := managedTypesCtx.PutEllipticCurve(p256ec)
	require.Equal(t, int32(1), ecHandle2)

	ec1, err := managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, err)
	require.Equal(t, p224ec, ec1)
	ec2, err := managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, err)
	require.Equal(t, p256ec, ec2)

	mBufferHandle1 := managedTypesCtx.NewManagedBufferFromBytes(mBytes)
	mBuffer, _ := managedTypesCtx.GetBytes(mBufferHandle1)
	require.Equal(t, mBytes, mBuffer)

	p224NormalGasCostMultiplier := managedTypesCtx.Get100xCurveGasCostMultiplier(ecHandle1)
	require.Equal(t, int32(100), p224NormalGasCostMultiplier)
	p256NormalGasCostMultiplier := managedTypesCtx.Get100xCurveGasCostMultiplier(ecHandle2)
	require.Equal(t, int32(135), p256NormalGasCostMultiplier)
	p224ScalarMultGasCostMultiplier := managedTypesCtx.GetScalarMult100xCurveGasCostMultiplier(ecHandle1)
	require.Equal(t, int32(100), p224ScalarMultGasCostMultiplier)
	p256ScalarMultGasCostMultiplier := managedTypesCtx.GetScalarMult100xCurveGasCostMultiplier(ecHandle2)
	require.Equal(t, int32(110), p256ScalarMultGasCostMultiplier)
	p224UCompressedGasCostMultiplier := managedTypesCtx.GetUCompressed100xCurveGasCostMultiplier(ecHandle1)
	require.Equal(t, int32(2000), p224UCompressedGasCostMultiplier)
	p256UCompressedGasCostMultiplier := managedTypesCtx.GetUCompressed100xCurveGasCostMultiplier(ecHandle2)
	require.Equal(t, int32(100), p256UCompressedGasCostMultiplier)

	// Copy active state to stack, then clean it. The previous 2 values should not
	// be accessible.
	managedTypesCtx.PushState()
	require.Equal(t, 1, len(managedTypesCtx.managedTypesStack))
	managedTypesCtx.InitState()

	bigInt1, err = managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Nil(t, bigInt1)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt2, err = managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Nil(t, bigInt2)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt1, bigInt2, err = managedTypesCtx.GetTwoBigInt(bigIntHandle1, bigIntHandle2)
	require.Nil(t, bigInt1, bigInt2)
	require.Equal(t, err, vmhost.ErrNoBigIntUnderThisHandle)

	bigFloat1, err = managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Nil(t, bigFloat1)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat2, err = managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Nil(t, bigFloat2)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat1, bigFloat2, err = managedTypesCtx.GetTwoBigFloats(bigFloatHandle1, bigFloatHandle2)
	require.Nil(t, bigFloat1, bigFloat2)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)

	ec1, err = managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, ec1)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)
	ec2, err = managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, ec2)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)

	mBuffer, err = managedTypesCtx.GetBytes(mBufferHandle1)
	require.Nil(t, mBuffer)
	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)

	p224NormalGasCostMultiplier = managedTypesCtx.Get100xCurveGasCostMultiplier(ecHandle1)
	require.Equal(t, int32(-1), p224NormalGasCostMultiplier)
	p256NormalGasCostMultiplier = managedTypesCtx.Get100xCurveGasCostMultiplier(ecHandle2)
	require.Equal(t, int32(-1), p256NormalGasCostMultiplier)
	p224ScalarMultGasCostMultiplier = managedTypesCtx.GetScalarMult100xCurveGasCostMultiplier(ecHandle1)
	require.Equal(t, int32(-1), p224ScalarMultGasCostMultiplier)
	p256ScalarMultGasCostMultiplier = managedTypesCtx.GetScalarMult100xCurveGasCostMultiplier(ecHandle2)
	require.Equal(t, int32(-1), p256ScalarMultGasCostMultiplier)
	p224UCompressedGasCostMultiplier = managedTypesCtx.GetUCompressed100xCurveGasCostMultiplier(ecHandle1)
	require.Equal(t, int32(-1), p224UCompressedGasCostMultiplier)
	p256UCompressedGasCostMultiplier = managedTypesCtx.GetUCompressed100xCurveGasCostMultiplier(ecHandle2)
	require.Equal(t, int32(-1), p256UCompressedGasCostMultiplier)

	// Add a value on the current active state
	bigIntHandle3 := managedTypesCtx.NewBigIntFromInt64(intValue3)
	require.Equal(t, int32(0), bigIntHandle3)
	bigInt3, err := managedTypesCtx.GetBigInt(bigIntHandle3)
	require.Equal(t, big.NewInt(intValue3), bigInt3)
	require.Nil(t, err)

	bigFloatHandle3, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue3))
	require.Equal(t, int32(0), bigFloatHandle3)
	bigFloat3, err := managedTypesCtx.GetBigFloat(bigFloatHandle3)
	require.Equal(t, big.NewFloat(floatValue3), bigFloat3)
	require.Nil(t, err)

	ecHandle3 := managedTypesCtx.PutEllipticCurve(p384ec)
	require.Equal(t, int32(0), ecHandle3)
	ec3, err := managedTypesCtx.GetEllipticCurve(ecHandle3)
	require.Nil(t, err)
	require.Equal(t, p384ec, ec3)

	p384NormalGasCostMultiplier := managedTypesCtx.Get100xCurveGasCostMultiplier(ecHandle3)
	require.Equal(t, int32(200), p384NormalGasCostMultiplier)
	p384ScalarMultGasCostMultiplier := managedTypesCtx.GetScalarMult100xCurveGasCostMultiplier(ecHandle3)
	require.Equal(t, int32(150), p384ScalarMultGasCostMultiplier)
	p384UCompressedGasCostMultiplier := managedTypesCtx.GetUCompressed100xCurveGasCostMultiplier(ecHandle3)
	require.Equal(t, int32(200), p384UCompressedGasCostMultiplier)

	// Copy active state to stack, then clean it. The previous 3 values should not
	// be accessible.
	managedTypesCtx.PushState()
	require.Equal(t, 2, len(managedTypesCtx.managedTypesStack))
	managedTypesCtx.InitState()

	bigInt1, err = managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Nil(t, bigInt1)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt2, err = managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Nil(t, bigInt2)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt3, err = managedTypesCtx.GetBigInt(bigIntHandle3)
	require.Nil(t, bigInt3)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)

	bigFloat1, err = managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Nil(t, bigFloat1)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat2, err = managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Nil(t, bigFloat2)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat3, err = managedTypesCtx.GetBigFloat(bigFloatHandle3)
	require.Nil(t, bigFloat3)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)

	ec1, err = managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, ec1)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)
	ec2, err = managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, ec2)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)
	ec3, err = managedTypesCtx.GetEllipticCurve(ecHandle3)
	require.Nil(t, ec3)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)

	intValue4 := int64(84)
	bigIntHandle4 := managedTypesCtx.NewBigIntFromInt64(intValue4)
	require.Equal(t, int32(0), bigIntHandle4)
	bigInt4, err := managedTypesCtx.GetBigInt(bigIntHandle4)
	require.Equal(t, big.NewInt(intValue4), bigInt4)
	require.Nil(t, err)
	bigInt4, bigInt3, err = managedTypesCtx.GetTwoBigInt(bigIntHandle4, int32(1))
	require.Nil(t, bigInt3)
	require.Nil(t, bigInt4)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)

	floatValue4 := 89.3823
	bigFloatHandle4, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue4))
	require.Equal(t, int32(0), bigFloatHandle4)
	bigFloat4, err := managedTypesCtx.GetBigFloat(bigFloatHandle4)
	require.Equal(t, big.NewFloat(floatValue4), bigFloat4)
	require.Nil(t, err)
	bigFloat4, bigFloat3, err = managedTypesCtx.GetTwoBigFloats(bigFloatHandle4, int32(1))
	require.Nil(t, bigFloat3)
	require.Nil(t, bigFloat4)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)

	ecIndex4 := managedTypesCtx.PutEllipticCurve(p521ec)
	require.Equal(t, int32(0), ecIndex4)
	ec4, err := managedTypesCtx.GetEllipticCurve(ecIndex4)
	require.Equal(t, p521ec, ec4)
	require.Nil(t, err)

	p521NormalGasCostMultiplier := managedTypesCtx.Get100xCurveGasCostMultiplier(ecIndex4)
	require.Equal(t, int32(250), p521NormalGasCostMultiplier)
	p521ScalarMultGasCostMultiplier := managedTypesCtx.GetScalarMult100xCurveGasCostMultiplier(ecIndex4)
	require.Equal(t, int32(190), p521ScalarMultGasCostMultiplier)
	p521UCompressedGasCostMultiplier := managedTypesCtx.GetUCompressed100xCurveGasCostMultiplier(ecIndex4)
	require.Equal(t, int32(400), p521UCompressedGasCostMultiplier)

	// Discard the top of the stack, losing value3; value4 should still be
	// accessible, since it's in the active state.
	managedTypesCtx.PopDiscard()
	require.Equal(t, 1, len(managedTypesCtx.managedTypesStack))
	bigInt4, err = managedTypesCtx.GetBigInt(bigIntHandle4)
	require.Equal(t, big.NewInt(intValue4), bigInt4)
	require.Nil(t, err)

	bigFloat4, err = managedTypesCtx.GetBigFloat(bigFloatHandle4)
	require.Equal(t, big.NewFloat(floatValue4), bigFloat4)
	require.Nil(t, err)

	ec4, err = managedTypesCtx.GetEllipticCurve(ecIndex4)
	require.Equal(t, p521ec, ec4)
	require.Nil(t, err)
	// Restore the first active state by popping to the active state (which is
	// lost).
	managedTypesCtx.PopSetActiveState()
	require.Equal(t, 0, len(managedTypesCtx.managedTypesStack))

	bigInt1, err = managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Nil(t, err)
	bigInt2, err = managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)
	bigInt1, bigInt2, err = managedTypesCtx.GetTwoBigInt(bigIntHandle1, bigIntHandle2)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)

	bigFloat1, err = managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Nil(t, err)
	bigFloat2, err = managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)
	bigFloat1, bigFloat2, err = managedTypesCtx.GetTwoBigFloats(bigFloatHandle1, bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)

	ec1, err = managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, err)
	require.Equal(t, p224ec, ec1)
	ec2, err = managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, err)
	require.Equal(t, p256ec, ec2)
}

func TestManagedTypesContext_PutGetBigInt(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}

	intValue1, intValue2, intValue3, intValue4 := int64(100), int64(200), int64(-42), int64(-80)
	managedTypesCtx, _ := NewManagedTypesContext(host)

	bigIntHandle1 := managedTypesCtx.NewBigIntFromInt64(intValue1)
	require.Equal(t, int32(0), bigIntHandle1)
	bigIntHandle2 := managedTypesCtx.NewBigIntFromInt64(intValue2)
	require.Equal(t, int32(1), bigIntHandle2)
	bigIntHandle3 := managedTypesCtx.NewBigIntFromInt64(intValue3)
	require.Equal(t, int32(2), bigIntHandle3)

	bigInt1, err := managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Nil(t, err)
	bigInt2, err := managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)
	bigInt4, err := managedTypesCtx.GetBigInt(int32(3))
	require.Nil(t, bigInt4)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt4 = managedTypesCtx.GetBigIntOrCreate(3)
	require.Equal(t, big.NewInt(0), bigInt4)

	index4 := managedTypesCtx.NewBigIntFromInt64(intValue4)
	require.Equal(t, int32(4), index4)
	bigInt4 = managedTypesCtx.GetBigIntOrCreate(4)
	require.Equal(t, big.NewInt(intValue4), bigInt4)

	bigValue, err := managedTypesCtx.GetBigInt(123)
	require.Nil(t, bigValue)
	require.Equal(t, vmhost.ErrNoBigIntUnderThisHandle, err)
	bigInt1, err = managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Nil(t, err)
	bigInt2, err = managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)

	bigInt1, err = managedTypesCtx.GetBigInt(bigIntHandle1)
	require.Equal(t, big.NewInt(intValue1), bigInt1)
	require.Nil(t, err)
	bigInt2, err = managedTypesCtx.GetBigInt(bigIntHandle2)
	require.Equal(t, big.NewInt(intValue2), bigInt2)
	require.Nil(t, err)
	bigInt3, err := managedTypesCtx.GetBigInt(bigIntHandle3)
	require.Equal(t, big.NewInt(intValue3), bigInt3)
	require.Nil(t, err)
}

func TestManagedTypesContext_PutGetBigFloat(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}

	floatValue1, floatValue2, floatValue3, floatValue4 := 23.56, 62.8453, -8234.6512, -0.0001
	managedTypesCtx, _ := NewManagedTypesContext(host)

	bigFloatHandle1, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue1))
	require.Equal(t, int32(0), bigFloatHandle1)
	bigFloatHandle2, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue2))
	require.Equal(t, int32(1), bigFloatHandle2)
	bigFloatHandle3, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue3))
	require.Equal(t, int32(2), bigFloatHandle3)

	bigFloat1, err := managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Nil(t, err)
	bigFloat2, err := managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)
	bigFloat4, err := managedTypesCtx.GetBigFloat(int32(3))
	require.Nil(t, bigFloat4)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat4, err = managedTypesCtx.GetBigFloatOrCreate(3)
	require.Equal(t, big.NewFloat(0), bigFloat4)
	require.Nil(t, err)

	bigFloatHandle4, _ := managedTypesCtx.PutBigFloat(new(big.Float).SetFloat64(floatValue4))
	require.Equal(t, int32(4), bigFloatHandle4)
	bigFloat4, err = managedTypesCtx.GetBigFloatOrCreate(4)
	require.Equal(t, big.NewFloat(floatValue4), bigFloat4)
	require.Nil(t, err)

	bigValue, err := managedTypesCtx.GetBigFloat(123)
	require.Nil(t, bigValue)
	require.Equal(t, vmhost.ErrNoBigFloatUnderThisHandle, err)
	bigFloat1, err = managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Nil(t, err)
	bigFloat2, err = managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)

	bigFloat1, err = managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Equal(t, big.NewFloat(floatValue1), bigFloat1)
	require.Nil(t, err)
	bigFloat2, err = managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Equal(t, big.NewFloat(floatValue2), bigFloat2)
	require.Nil(t, err)
	bigFloat3, err := managedTypesCtx.GetBigFloat(bigFloatHandle3)
	require.Equal(t, big.NewFloat(floatValue3), bigFloat3)
	require.Nil(t, err)

	bigFloat1.SetInf(true)
	bigFloat2.SetInf(false)

	infFloat1, err := managedTypesCtx.GetBigFloat(bigFloatHandle1)
	require.Nil(t, infFloat1)
	require.Equal(t, vmhost.ErrInfinityFloatOperation, err)

	infFloat2, err := managedTypesCtx.GetBigFloat(bigFloatHandle2)
	require.Nil(t, infFloat2)
	require.Equal(t, vmhost.ErrInfinityFloatOperation, err)

	infFloat1, err = managedTypesCtx.GetBigFloatOrCreate(bigFloatHandle1)
	require.Nil(t, infFloat1)
	require.Equal(t, vmhost.ErrInfinityFloatOperation, err)

	infFloat2, err = managedTypesCtx.GetBigFloatOrCreate(bigFloatHandle2)
	require.Nil(t, infFloat2)
	require.Equal(t, vmhost.ErrInfinityFloatOperation, err)

	infFloat1, infFloat2, err = managedTypesCtx.GetTwoBigFloats(bigFloatHandle1, bigFloatHandle2)
	require.Nil(t, infFloat1)
	require.Nil(t, infFloat2)
	require.Equal(t, vmhost.ErrInfinityFloatOperation, err)

	infFloat1, nonInfFloat, err := managedTypesCtx.GetTwoBigFloats(bigFloatHandle1, bigFloatHandle3)
	require.Nil(t, infFloat1)
	require.Nil(t, nonInfFloat)
	require.Equal(t, vmhost.ErrInfinityFloatOperation, err)
}
func TestManagedTypesContext_NewBigIntCopied(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}
	managedTypesCtx, _ := NewManagedTypesContext(host)

	originalBigInt := big.NewInt(3)
	index1 := managedTypesCtx.NewBigInt(originalBigInt)

	retrievedValue, err := managedTypesCtx.GetBigInt(index1)
	require.Nil(t, err)
	retrievedValue.Add(retrievedValue, big.NewInt(100)) // simulate a change of the value in the contract

	require.Equal(t, big.NewInt(3), originalBigInt)
}

func TestManagedTypesContext_PutGetEllipticCurves(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}

	p224ec, p256ec, p384ec, p521ec := elliptic.P224().Params(), elliptic.P256().Params(), elliptic.P384().Params(), elliptic.P521().Params()
	managedTypesCtx, _ := NewManagedTypesContext(host)

	ecHandle1 := managedTypesCtx.PutEllipticCurve(p224ec)
	require.Equal(t, int32(0), ecHandle1)
	ecHandle2 := managedTypesCtx.PutEllipticCurve(p256ec)
	require.Equal(t, int32(1), ecHandle2)
	ecHandle3 := managedTypesCtx.PutEllipticCurve(p384ec)
	require.Equal(t, int32(2), ecHandle3)

	p224PrivKeyByteLength := managedTypesCtx.GetPrivateKeyByteLengthEC(ecHandle1)
	require.Equal(t, int32(28), p224PrivKeyByteLength)
	p256PrivKeyByteLength := managedTypesCtx.GetPrivateKeyByteLengthEC(ecHandle2)
	require.Equal(t, int32(32), p256PrivKeyByteLength)
	p384PrivKeyByteLength := managedTypesCtx.GetPrivateKeyByteLengthEC(ecHandle3)
	require.Equal(t, int32(48), p384PrivKeyByteLength)
	nonExistentCurvePrivKeyByteLength := managedTypesCtx.GetPrivateKeyByteLengthEC(int32(3))
	require.Equal(t, int32(-1), nonExistentCurvePrivKeyByteLength)

	ec1, err := managedTypesCtx.GetEllipticCurve(ecHandle1)
	require.Nil(t, err)
	require.Equal(t, p224ec, ec1)
	ec2, err := managedTypesCtx.GetEllipticCurve(ecHandle2)
	require.Nil(t, err)
	require.Equal(t, p256ec, ec2)
	ec4, err := managedTypesCtx.GetEllipticCurve(int32(3))
	require.Nil(t, ec4)
	require.Equal(t, vmhost.ErrNoEllipticCurveUnderThisHandle, err)

	ecHandle4 := managedTypesCtx.PutEllipticCurve(p521ec)
	require.Equal(t, int32(3), ecHandle4)
	ec4, err = managedTypesCtx.GetEllipticCurve(ecHandle4)
	require.Nil(t, err)
	require.Equal(t, p521ec, ec4)
}

func TestManagedTypesContext_ManagedBuffersFunctionalities(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}
	managedTypesCtx, _ := NewManagedTypesContext(host)
	mBytes := []byte{2, 234, 64, 255}
	emptyBuffer := make([]byte, 0)

	// Calls for non-existent buffers
	noBufHandle := int32(379)
	byteArray, err := managedTypesCtx.GetBytes(noBufHandle)
	require.Nil(t, byteArray)
	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)
	newBuf, err := managedTypesCtx.DeleteSlice(noBufHandle, 0, 3)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)
	newBuf, err = managedTypesCtx.GetSlice(noBufHandle, -3, 2)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)
	lengthOfmBuffer := managedTypesCtx.GetLength(noBufHandle)
	require.Equal(t, int32(-1), lengthOfmBuffer)
	isSuccess := managedTypesCtx.AppendBytes(noBufHandle, mBytes)
	require.False(t, isSuccess)
	newBuf, err = managedTypesCtx.InsertSlice(noBufHandle, 0, mBytes)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)

	// New/Get/Set Buffer
	mBufferHandle1 := managedTypesCtx.NewManagedBuffer()
	require.Equal(t, int32(0), mBufferHandle1)
	byteArray, err = managedTypesCtx.GetBytes(mBufferHandle1)
	require.Nil(t, err)
	require.Equal(t, emptyBuffer, byteArray)
	managedTypesCtx.SetBytes(mBufferHandle1, mBytes)
	mBufferBytes, _ := managedTypesCtx.GetBytes(mBufferHandle1)
	require.Equal(t, mBytes, mBufferBytes)
	mBufferHandle2 := managedTypesCtx.NewManagedBufferFromBytes(mBytes)
	require.Equal(t, int32(1), mBufferHandle2)
	mBufferBytes, _ = managedTypesCtx.GetBytes(mBufferHandle2)
	require.Equal(t, mBytes, mBufferBytes)

	// Get Slice
	bufSlice, err := managedTypesCtx.GetSlice(noBufHandle, int32(3), int32(0))
	require.Nil(t, bufSlice)
	require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)
	bufSlice, err = managedTypesCtx.GetSlice(mBufferHandle1, int32(1), int32(10))
	require.Nil(t, bufSlice)
	require.Equal(t, vmhost.ErrBadBounds, err)
	bufSlice, err = managedTypesCtx.GetSlice(mBufferHandle1, int32(4), int32(-1))
	require.Nil(t, bufSlice)
	require.Equal(t, vmhost.ErrBadBounds, err)
	bufSlice, err = managedTypesCtx.GetSlice(mBufferHandle1, int32(3), int32(0))
	require.Nil(t, err)
	require.Equal(t, emptyBuffer, bufSlice)

	// Delete Slice
	newBuf, err = managedTypesCtx.DeleteSlice(mBufferHandle1, 3, 1)
	require.Nil(t, err)
	require.Equal(t, mBytes[:3], newBuf)
	newBuf, err = managedTypesCtx.DeleteSlice(mBufferHandle1, 3, 0)
	require.Nil(t, err)
	require.Equal(t, mBytes[:3], newBuf)
	newBuf, err = managedTypesCtx.DeleteSlice(mBufferHandle1, -1, 0)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrBadBounds, err)
	newBuf, err = managedTypesCtx.DeleteSlice(mBufferHandle1, 0, -1)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrBadBounds, err)
	newBuf, err = managedTypesCtx.DeleteSlice(mBufferHandle1, 0, 10)
	require.Nil(t, err)
	require.Equal(t, emptyBuffer, newBuf)

	// Append, GetLength
	isSuccess = managedTypesCtx.AppendBytes(mBufferHandle1, mBytes)
	require.True(t, isSuccess)
	lengthOfmBuffer = managedTypesCtx.GetLength(mBufferHandle1)
	require.Equal(t, int32(4), lengthOfmBuffer)
	isSuccess = managedTypesCtx.AppendBytes(mBufferHandle1, mBytes)
	require.True(t, isSuccess)
	mBufferBytes, _ = managedTypesCtx.GetBytes(mBufferHandle1)
	require.Equal(t, append(mBytes, mBytes...), mBufferBytes)
	isSuccess = managedTypesCtx.AppendBytes(mBufferHandle1, emptyBuffer)
	require.True(t, isSuccess)
	mBufferBytes, _ = managedTypesCtx.GetBytes(mBufferHandle1)
	require.Equal(t, append(mBytes, mBytes...), mBufferBytes)

	managedTypesCtx.SetBytes(mBufferHandle1, mBytes)
	mBufferBytes, _ = managedTypesCtx.GetBytes(mBufferHandle1)
	require.Equal(t, mBytes, mBufferBytes)

	// Insert Slice
	newBuf, err = managedTypesCtx.InsertSlice(mBufferHandle1, -1, mBytes)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrBadBounds, err)
	newBuf, err = managedTypesCtx.InsertSlice(mBufferHandle1, 4, mBytes)
	require.Nil(t, newBuf)
	require.Equal(t, vmhost.ErrBadBounds, err)
	bytesWithNewSlice := []byte{2, 234, 64, 2, 234, 64, 255, 255}
	newBuf, err = managedTypesCtx.InsertSlice(mBufferHandle1, 3, mBytes)
	require.Nil(t, err)
	require.Equal(t, bytesWithNewSlice, newBuf)
	bytesWithNewSlice = []byte{2, 234, 64, 255, 2, 234, 64, 2, 234, 64, 255, 255}
	newBuf, err = managedTypesCtx.InsertSlice(mBufferHandle1, 0, mBytes)
	require.Nil(t, err)
	require.Equal(t, bytesWithNewSlice, newBuf)

	mBufferBytes, _ = managedTypesCtx.GetBytes(mBufferHandle1)
	require.Equal(t, bytesWithNewSlice, mBufferBytes)
}

// TestManagedTypesContext_GetBytesCopyOnReadInvariant asserts the actual invariant this PR
// establishes: GetBytes must hand out an unaliased copy, so that a caller mutating the returned
// slice can never retroactively change the stored buffer (or a second, independent GetBytes call).
func TestManagedTypesContext_GetBytesCopyOnReadInvariant(t *testing.T) {
	t.Parallel()

	original := []byte{1, 2, 3, 4}

	host := &contextmock.VMHostStub{}
	managedTypesCtx, _ := NewManagedTypesContext(host)
	mBufferHandle := managedTypesCtx.NewManagedBufferFromBytes(original)

	firstRead, err := managedTypesCtx.GetBytes(mBufferHandle)
	require.Nil(t, err)
	require.Equal(t, original, firstRead)

	firstRead[0] = 0xFF

	secondRead, err := managedTypesCtx.GetBytes(mBufferHandle)
	require.Nil(t, err)
	require.Equal(t, original, secondRead,
		"mutating a previous GetBytes result must not affect a later GetBytes call")
}

// TestManagedTypesContext_SetByteSlice covers the branches SetByteSlice added to collapse
// GetBytes+SetBytes into a single buffer copy: handle-not-found, out-of-bounds, and the
// write path.
func TestManagedTypesContext_SetByteSlice(t *testing.T) {
	t.Parallel()

	t.Run("handle not found", func(t *testing.T) {
		managedTypesCtx, _ := NewManagedTypesContext(newForkedHostStub())

		ok, err := managedTypesCtx.SetByteSlice(999, 0, 1, []byte{0xAA})
		require.False(t, ok)
		require.Equal(t, vmhost.ErrNoManagedBufferUnderThisHandle, err)
	})

	t.Run("out of bounds", func(t *testing.T) {
		managedTypesCtx, _ := NewManagedTypesContext(newForkedHostStub())
		mBufferHandle := managedTypesCtx.NewManagedBufferFromBytes([]byte{1, 2, 3, 4})

		ok, err := managedTypesCtx.SetByteSlice(mBufferHandle, 2, 10, []byte{0xAA})
		require.False(t, ok)
		require.Nil(t, err)

		ok, err = managedTypesCtx.SetByteSlice(mBufferHandle, -1, 1, []byte{0xAA})
		require.False(t, ok)
		require.Nil(t, err)

		ok, err = managedTypesCtx.SetByteSlice(mBufferHandle, 0, -1, []byte{0xAA})
		require.False(t, ok)
		require.Nil(t, err)

		ok, err = managedTypesCtx.SetByteSlice(mBufferHandle, 2147483647, 1, []byte{0xAA})
		require.False(t, ok)
		require.Nil(t, err)
	})

	t.Run("single-copy write", func(t *testing.T) {
		host := newForkedHostStub()
		managedTypesCtx, _ := NewManagedTypesContext(host)
		mBufferHandle := managedTypesCtx.NewManagedBufferFromBytes([]byte{1, 2, 3, 4})

		ok, err := managedTypesCtx.SetByteSlice(mBufferHandle, 1, 2, []byte{0xAA, 0xBB})
		require.True(t, ok)
		require.Nil(t, err)

		result, err := managedTypesCtx.GetBytes(mBufferHandle)
		require.Nil(t, err)
		require.Equal(t, []byte{1, 0xAA, 0xBB, 4}, result)
	})
}

func TestManagedTypesContext_PopSetActiveStateIfStackIsEmptyShouldNotPanic(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}

	managedTypesCtx, _ := NewManagedTypesContext(host)
	managedTypesCtx.PopSetActiveState()

	require.Equal(t, 0, len(managedTypesCtx.managedTypesStack))
}

func TestManagedTypesContext_PopDiscardIfStackIsEmptyShouldNotPanic(t *testing.T) {
	t.Parallel()
	host := &contextmock.VMHostStub{}

	managedTypesCtx, _ := NewManagedTypesContext(host)
	managedTypesCtx.PopDiscard()

	require.Equal(t, 0, len(managedTypesCtx.managedTypesStack))
}

func TestManagedTypesContext_CleanBackTransfersMustEmptyItsFields(t *testing.T) {
	t.Parallel()

	// Setup managed types context
	host := &contextmock.VMHostMock{}
	managedTypesCtx, _ := NewManagedTypesContext(host)
	managedTypesCtx.InitState()

	// Fills the backtransfers fields
	callValue := big.NewInt(1_000_000)
	managedTypesCtx.AddValueOnlyBackTransfer(callValue)
	transfers := []*vmcommon.KDATransfer{
		{
			KDATokenName:  []byte("TSF-1HIU"),
			KDATokenType:  0,
			KDAValue:      big.NewInt(1_000),
			KDATokenNonce: 0,
		},
		{
			KDATokenName:  []byte("TSN-2BA7"),
			KDATokenType:  1,
			KDAValue:      big.NewInt(1),
			KDATokenNonce: 1,
		},
		{
			KDATokenName:  []byte("TSS-3Y8G"),
			KDATokenType:  2,
			KDAValue:      big.NewInt(1),
			KDATokenNonce: 2,
		},
	}
	managedTypesCtx.AddBackTransfers(transfers)

	// Verifies the fields are filled
	assert.Equal(t, callValue, managedTypesCtx.managedTypesValues.backTransfers.CallValue)
	assert.EqualValues(t, transfers, managedTypesCtx.managedTypesValues.backTransfers.KDATransfers)

	// Calling the method to clean the fields
	managedTypesCtx.CleanBackTransfers()

	// Verifies the fields are empty
	assert.Equal(t, big.NewInt(0), managedTypesCtx.managedTypesValues.backTransfers.CallValue)
	assert.Len(t, managedTypesCtx.managedTypesValues.backTransfers.KDATransfers, 0)
}

func newForkedHostStub() *contextmock.VMHostStub {
	return &contextmock.VMHostStub{
		ForkControllerCalled: func() core.ForkController {
			return commonMock.NewForkControllerStub()
		},
	}
}

func TestGrowManagedBuffer(t *testing.T) {
	t.Parallel()

	t.Run("a buffer with spare capacity is returned untouched", func(t *testing.T) {
		t.Parallel()

		buffer := make([]byte, 4, 16)
		grown := growManagedBuffer(buffer, 10)

		require.Equal(t, 16, cap(grown))
		require.Equal(t, 4, len(grown))
		// same backing array, so nothing was copied
		require.Equal(t, &buffer[0], &grown[0])
	})

	t.Run("a full buffer doubles", func(t *testing.T) {
		t.Parallel()

		buffer := make([]byte, 8, 8)
		grown := growManagedBuffer(buffer, 9)

		require.Equal(t, 16, cap(grown))
		require.Equal(t, 8, len(grown))
	})

	t.Run("doubling that still does not fit grows to exactly what is needed", func(t *testing.T) {
		t.Parallel()

		buffer := make([]byte, 8, 8)
		grown := growManagedBuffer(buffer, 100)

		require.Equal(t, 100, cap(grown))
	})

	t.Run("growing from empty allocates exactly what is needed", func(t *testing.T) {
		t.Parallel()

		grown := growManagedBuffer(make([]byte, 0), 7)

		require.Equal(t, 7, cap(grown))
		require.Equal(t, 0, len(grown))
	})

	t.Run("the existing bytes survive the move", func(t *testing.T) {
		t.Parallel()

		buffer := []byte("abcd")
		grown := growManagedBuffer(buffer[:len(buffer):len(buffer)], 5)

		require.Equal(t, []byte("abcd"), grown)
	})
}

// TestAppendBytesCapacityIsDeterministic pins the capacity a managed buffer has after a fixed
// sequence of appends. The gas charged for an append depends on whether the buffer has to
// reallocate, so the capacity is consensus-critical: if it were left to Go's append, the
// allocator's size classes and growth formula (both unspecified, both changed between releases)
// would decide it, and two nodes built with different toolchains could charge different gas for
// the same transaction. A failure here means a repricing, not just a refactor.
func TestAppendBytesCapacityIsDeterministic(t *testing.T) {
	t.Parallel()

	host := &contextmock.VMHostStub{}
	managedTypesCtx, err := NewManagedTypesContext(host)
	require.Nil(t, err)

	handle := managedTypesCtx.NewManagedBuffer()

	// each step appends 10 bytes; capacity doubles only when the append does not fit
	expectedCapacities := []int{10, 20, 40, 40, 80, 80, 80, 80, 160, 160}
	for step, expectedCapacity := range expectedCapacities {
		require.True(t, managedTypesCtx.AppendBytes(handle, make([]byte, 10)))

		buffer := managedTypesCtx.managedTypesValues.mBufferValues[handle]
		require.Equalf(t, (step+1)*10, len(buffer), "length after step %d", step)
		require.Equalf(t, expectedCapacity, cap(buffer), "capacity after step %d", step)
	}
}

// TestSetBytesLeavesNoSpareCapacity pins the property the audit finding rests on: a buffer seeded
// through SetBytes has len == cap, so the very next append does copy the whole accumulator and
// must be charged for it.
func TestSetBytesLeavesNoSpareCapacity(t *testing.T) {
	t.Parallel()

	host := &contextmock.VMHostStub{}
	managedTypesCtx, err := NewManagedTypesContext(host)
	require.Nil(t, err)

	handle := managedTypesCtx.NewManagedBuffer()
	managedTypesCtx.SetBytes(handle, bytes.Repeat([]byte{'a'}, 1000))

	buffer := managedTypesCtx.managedTypesValues.mBufferValues[handle]
	require.Equal(t, 1000, len(buffer))
	require.Equal(t, 1000, cap(buffer))
	require.True(t, appendWillReallocate(buffer, 1))
}

// TestSetByteSliceLeavesNoSpareCapacity does the same for the other path that replaces a buffer's
// backing array, so no hook can leave a runtime-chosen capacity behind in the map
func TestSetByteSliceLeavesNoSpareCapacity(t *testing.T) {
	t.Parallel()

	managedTypesCtx, err := NewManagedTypesContext(newForkedHostStub())
	require.Nil(t, err)

	handle := managedTypesCtx.NewManagedBuffer()
	managedTypesCtx.SetBytes(handle, bytes.Repeat([]byte{'a'}, 100))
	// grow the capacity past the length so the check below is meaningful
	require.True(t, managedTypesCtx.AppendBytes(handle, []byte{'b'}))
	require.Greater(t, cap(managedTypesCtx.managedTypesValues.mBufferValues[handle]), 101)

	ok, err := managedTypesCtx.SetByteSlice(handle, 0, 4, []byte("zzzz"))
	require.Nil(t, err)
	require.True(t, ok)

	buffer := managedTypesCtx.managedTypesValues.mBufferValues[handle]
	require.Equal(t, 101, len(buffer))
	require.Equal(t, 101, cap(buffer))
}

// TestInsertSliceLeavesAPolicyChosenCapacity closes the last path that could leave a runtime-chosen
// capacity in the map. InsertSlice used to build its result with append, so the allocator's size
// classes decided the capacity, and the gas a later append charges reads cap(). Two nodes built
// with different toolchains would then have charged different gas for the same call. The hook is
// not wired to the VM today, which is exactly why it needs a test: wiring it later must not
// silently reintroduce the divergence.
func TestInsertSliceLeavesAPolicyChosenCapacity(t *testing.T) {
	t.Parallel()

	managedTypesCtx, err := NewManagedTypesContext(newForkedHostStub())
	require.Nil(t, err)

	handle := managedTypesCtx.NewManagedBuffer()
	managedTypesCtx.SetBytes(handle, bytes.Repeat([]byte{'a'}, 1000))

	_, err = managedTypesCtx.InsertSlice(handle, 0, make([]byte, 100))
	require.Nil(t, err)

	buffer := managedTypesCtx.managedTypesValues.mBufferValues[handle]
	require.Equal(t, 1100, len(buffer))
	// what the growth policy dictates for 1000 bytes that must hold 1100, not a size class
	require.Equal(t, cap(growManagedBuffer(make([]byte, 1000, 1000), 1100)), cap(buffer))
}

// TestInsertSliceDoesNotMutateTheCallerSlice pins the other half of the same rewrite. The old
// implementation appended the buffer's tail onto the caller's slice, so a slice handed in with
// spare capacity had that spare capacity overwritten.
func TestInsertSliceDoesNotMutateTheCallerSlice(t *testing.T) {
	t.Parallel()

	managedTypesCtx, err := NewManagedTypesContext(newForkedHostStub())
	require.Nil(t, err)

	handle := managedTypesCtx.NewManagedBuffer()
	managedTypesCtx.SetBytes(handle, []byte("HELLO-WORLD"))

	inserted := make([]byte, 3, 16)
	copy(inserted, "xyz")
	spare := inserted[:16][3:]
	for i := range spare {
		spare[i] = '.'
	}

	buffer, err := managedTypesCtx.InsertSlice(handle, 5, inserted)
	require.Nil(t, err)
	require.Equal(t, []byte("HELLOxyz-WORLD"), buffer)
	require.Equal(t, bytes.Repeat([]byte{'.'}, 13), spare)
}

// TestDeleteSliceKeepsThePolicyCapacity is the last write path into mBufferValues. DeleteSlice
// only ever shrinks, so it reuses the backing array and cannot introduce a runtime-chosen
// capacity - but it is asserted rather than assumed, so that the four paths together state the
// invariant the append gas depends on: every capacity in the map is one growManagedBuffer chose.
func TestDeleteSliceKeepsThePolicyCapacity(t *testing.T) {
	t.Parallel()

	managedTypesCtx, err := NewManagedTypesContext(newForkedHostStub())
	require.Nil(t, err)

	handle := managedTypesCtx.NewManagedBuffer()
	managedTypesCtx.SetBytes(handle, bytes.Repeat([]byte{'a'}, 1000))
	require.True(t, managedTypesCtx.AppendBytes(handle, make([]byte, 100)))
	capacityBefore := cap(managedTypesCtx.managedTypesValues.mBufferValues[handle])

	_, err = managedTypesCtx.DeleteSlice(handle, 10, 50)
	require.Nil(t, err)

	buffer := managedTypesCtx.managedTypesValues.mBufferValues[handle]
	require.Equal(t, 1050, len(buffer))
	require.Equal(t, capacityBefore, cap(buffer))
}
