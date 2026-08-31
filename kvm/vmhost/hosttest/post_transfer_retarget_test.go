package hostCoretest

import (
	"errors"
	"math/big"
	"testing"

	blockchainConfig "github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	mock "github.com/klever-io/klever-go/kvm/mock/context"
	"github.com/klever-io/klever-go/kvm/mock/contracts"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// Post-transfer execution of the KleverTransfer built-in (KLR-49).
//
// KleverTransfer reads its recipient from args[0] when that argument is a 32-byte value above 2^64
// (the inherited "at sender" encoding), so the follow-up execution can be retargeted to an address
// other than the one the call was addressed to. callBuiltinFunction decides which transfers that
// follow-up call is allowed to observe as its call value: a transfer settled before the built-in
// ran was settled to the pre-parse recipient, so once FixAuditChangesV5 is active a retargeted
// recipient no longer sees it. Transfers this built-in run settles itself belong to the recipient
// it settled them to, retargeted or not, and are always reported.
//
// These tests cover all three combinations through RunSmartContractCall, asserting what the callee
// is told it was paid against what it was actually paid in the world, and pin the pre-fork
// behaviour for block replay. The recording endpoint reads runtime.GetVMInput(), the same source
// every call-value hook uses (GetCallValue at baseOps.go:1347, GetNumKDATransfers at :1486,
// GetKDAValueByIndex at :1370).
//
// Note that WithEnableEpochs steers the host's ForkController, which is the one this path reads.
// The built-in container builds its own (worldmock/builtinFunctionsWrapper.go:49) and is unaffected.

// preForkV5EnableEpochs activates every fork at epoch 0 except FixAuditChangesV5, pinning the test
// to pre-KLR-49-fix behaviour. postForkV5EnableEpochs states the activation explicitly rather than
// relying on the zero value, so a post-fork test never inherits its subject silently.
func preForkV5EnableEpochs() blockchainConfig.EnableEpochs {
	return blockchainConfig.EnableEpochs{FixAuditChangesV5: 1_000_000}
}

func postForkV5EnableEpochs() blockchainConfig.EnableEpochs {
	return blockchainConfig.EnableEpochs{FixAuditChangesV5: 0}
}

var retargetedAddress = test.MakeTestSCAddressWithDefaultVM("retargetedSC")

const (
	retargetTransferAmount = int64(1000)
	retargetDeclaredAmount = int64(7)
	retargetInitialBalance = int64(5000)
	retargetFollowUpGas    = int64(500)
)

// observedCallValue is what the contract reached by the post-transfer execution was told it was
// paid, recorded from inside the callee rather than reconstructed from the VMOutput.
type observedCallValue struct {
	calledBy     []byte
	numTransfers int
	value        int64
}

// recordCallValueMock gives a contract a "deposit" endpoint that publishes its observed call value
// into the test. A real one would credit a balance here.
func recordCallValueMock(observed *observedCallValue) func(*mock.InstanceMock, interface{}) {
	return func(instance *mock.InstanceMock, _ interface{}) {
		instance.AddMockMethod("deposit", func() *mock.InstanceMock {
			vmInput := instance.Host.Runtime().GetVMInput()
			observed.calledBy = instance.Host.Runtime().GetContextAddress()
			observed.numTransfers = len(vmInput.KDATransfers)
			observed.value = vmInput.GetKDACallValue(test.KDATestTokenName).Int64()

			return instance
		})
	}
}

// transferThenCall settles a list of payments to dest and then invokes a follow-up function on it,
// the managedMultiTransferKDANFTExecute shape. Naming KleverTransfer as that follow-up re-enters
// the argument parser, which is where args[0] can retarget the execution.
func transferThenCall(dest []byte, followUpArgs [][]byte) func(vmhost.VMHost) {
	return func(host vmhost.VMHost) {
		transfer := &vmcommon.KDATransfer{
			KDAValue:     big.NewInt(retargetTransferAmount),
			KDATokenName: test.KDATestTokenName,
		}

		ret := vmhooks.TransferKDANFTExecuteWithTypedArgs(
			host,
			dest,
			[]*vmcommon.KDATransfer{transfer},
			retargetFollowUpGas,
			[]byte(core.BuiltInFunctionTransfer),
			followUpArgs,
		)
		if ret != 0 {
			host.Runtime().FailExecution(errors.New("transfer failed"))
		}
	}
}

// callBuiltinDirectly reaches the same built-in through executeOnDestContext, where the transfers
// are declared in the arguments and arrive at the built-in still pending.
func callBuiltinDirectly(dest []byte, args [][]byte) func(vmhost.VMHost) {
	return func(host vmhost.VMHost) {
		input := test.DefaultTestContractCallInput()
		input.CallerAddr = host.Runtime().GetContextAddress()
		input.RecipientAddr = dest
		input.Function = core.BuiltInFunctionTransfer
		input.GasProvided = uint64(retargetFollowUpGas)
		input.Arguments = args

		if ret := contracts.ExecuteOnDestContextInMockContracts(host, input, big.NewInt(0)); ret != 0 {
			host.Runtime().FailExecution(errors.New("execute on dest context failed"))
		}
	}
}

// runPostTransferCall wires caller -> dest -> retargeted, all three mock contracts, and runs one
// callerBody. Both dest and the retargeted address expose "deposit", so whichever one the parser
// settles on is the one that reports.
func runPostTransferCall(
	t *testing.T,
	callerBody func(vmhost.VMHost),
	epochs blockchainConfig.EnableEpochs,
) (*observedCallValue, *worldmock.MockWorld) {
	testConfig := makeTestConfig()
	observed := &observedCallValue{}

	var world *worldmock.MockWorld
	_, err := test.BuildMockInstanceCallTest(t).
		WithContracts(
			test.CreateMockContract(test.ParentAddress).
				WithBalance(testConfig.ParentBalance).
				WithConfig(testConfig).
				WithMethods(func(instance *mock.InstanceMock, _ interface{}) {
					instance.AddMockMethod("transferAndCall", func() *mock.InstanceMock {
						callerBody(instance.Host)
						return instance
					})
				}),
			test.CreateMockContract(test.ChildAddress).
				WithBalance(testConfig.ChildBalance).
				WithConfig(testConfig).
				WithMethods(recordCallValueMock(observed)),
			test.CreateMockContract(retargetedAddress).
				WithBalance(testConfig.ChildBalance).
				WithConfig(testConfig).
				WithMethods(recordCallValueMock(observed)),
		).
		WithInput(test.CreateTestContractCallInputBuilder().
			WithRecipientAddr(test.ParentAddress).
			WithGasProvided(testConfig.GasProvided).
			WithFunction("transferAndCall").
			Build()).
		WithSetup(func(host vmhost.VMHost, mockWorld *worldmock.MockWorld) {
			world = mockWorld
			createMockBuiltinFunctions(t, host, mockWorld)
			setZeroCodeCosts(host)
			host.Metering().GasSchedule().BuiltInCost.Transfer = 1

			caller, _ := mockWorld.AccountsCacher.LoadUser(test.ParentAddress)
			require.NoError(t, caller.AddToBalance(retargetInitialBalance, test.KDATestTokenName, true))
		}).
		WithEnableEpochs(epochs).
		AndAssertResults(func(_ *worldmock.MockWorld, verify *test.VMOutputVerifier) {
			verify.Ok()
		})
	require.NoError(t, err)

	return observed, world
}

func kdaBalanceOf(t *testing.T, world *worldmock.MockWorld, address []byte) int64 {
	account, err := world.AccountsCacher.LoadUser(address)
	require.NoError(t, err)

	kda, err := account.GetUserKDA(test.KDATestTokenName, nil, true)
	if err != nil || kda == nil {
		return 0
	}

	return kda.Balance
}

// Test_PostTransferRetarget_SettledElsewhereNotReported is the case the fix addresses: the caller
// settles 1000 to dest and then names KleverTransfer as the follow-up with a different address in
// args[0], so the execution lands on that address while the money stays with dest.
func Test_PostTransferRetarget_SettledElsewhereNotReported(t *testing.T) {
	// args[0] retargets the call, args[1] declares no further transfers, args[2:] are the follow-up
	// function and its arguments
	retargeted := transferThenCall(test.ChildAddress,
		[][]byte{retargetedAddress, {}, []byte("deposit"), {0x01}})

	t.Run("preFork", func(t *testing.T) {
		observed, world := runPostTransferCall(t, retargeted, preForkV5EnableEpochs())

		require.Equal(t, retargetedAddress, observed.calledBy)
		require.Equal(t, 1, observed.numTransfers, "legacy behaviour: transfers settled to dest are reported here too")
		require.Equal(t, retargetTransferAmount, observed.value)

		require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, test.ChildAddress))
		require.Zero(t, kdaBalanceOf(t, world, retargetedAddress))
	})

	t.Run("postFork", func(t *testing.T) {
		observed, world := runPostTransferCall(t, retargeted, postForkV5EnableEpochs())

		require.Equal(t, retargetedAddress, observed.calledBy, "the follow-up call itself is unchanged")
		require.Zero(t, observed.numTransfers, "a transfer settled to another recipient is not this one's call value")
		require.Zero(t, observed.value)

		// settlement is untouched by the fix: the money is where it was before
		require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, test.ChildAddress))
		require.Zero(t, kdaBalanceOf(t, world, retargetedAddress))
	})
}

// Test_PostTransferRetarget_MixedSettlementReportsOnlyItsOwn separates the two things a retargeted
// call can carry. One transfer is settled to dest before the built-in runs; a second is declared in
// the arguments and settled by that same run to the retargeted address. Only the reporting of the
// first changes across the fork - the second is a payment the callee genuinely received, and is
// reported either way. This is what makes the fix a reporting correction rather than a restriction
// on retargeted transfers.
func Test_PostTransferRetarget_MixedSettlementReportsOnlyItsOwn(t *testing.T) {
	// [payee, numTransfers, token, nonce, value, function], on top of the amount already settled to
	// dest by the hook itself
	mixed := transferThenCall(test.ChildAddress, [][]byte{
		retargetedAddress,
		{0x01},
		test.KDATestTokenName,
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		big.NewInt(retargetDeclaredAmount).Bytes(),
		[]byte("deposit"),
	})

	t.Run("preFork", func(t *testing.T) {
		observed, world := runPostTransferCall(t, mixed, preForkV5EnableEpochs())

		require.Equal(t, retargetedAddress, observed.calledBy)
		require.Equal(t, 2, observed.numTransfers)
		require.Equal(t, retargetTransferAmount+retargetDeclaredAmount, observed.value,
			"legacy behaviour: the amount settled to dest is reported here as well")

		require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, test.ChildAddress))
		require.Equal(t, retargetDeclaredAmount, kdaBalanceOf(t, world, retargetedAddress),
			"only the declared transfer ever reached this address")
	})

	t.Run("postFork", func(t *testing.T) {
		observed, world := runPostTransferCall(t, mixed, postForkV5EnableEpochs())

		require.Equal(t, retargetedAddress, observed.calledBy)
		require.Equal(t, 1, observed.numTransfers, "the transfer this run settled here is kept")
		require.Equal(t, retargetDeclaredAmount, observed.value,
			"reported call value now matches what this address was actually paid")

		// both settlements are unchanged: only the report was
		require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, test.ChildAddress))
		require.Equal(t, retargetDeclaredAmount, kdaBalanceOf(t, world, retargetedAddress))
	})
}

// Test_PostTransferRetarget_SameRecipientKeepsCallValue is the ordinary "pay X, then call X" flow,
// which the Rust SDK emits for every sync_call carrying a KDA payment: args[0] names the same
// address the transfers were settled to, so the call value is real and reported under both forks.
func Test_PostTransferRetarget_SameRecipientKeepsCallValue(t *testing.T) {
	sameRecipient := transferThenCall(test.ChildAddress,
		[][]byte{test.ChildAddress, {}, []byte("deposit"), {0x01}})

	for _, tc := range []struct {
		name   string
		epochs blockchainConfig.EnableEpochs
	}{
		{name: "preFork", epochs: preForkV5EnableEpochs()},
		{name: "postFork", epochs: postForkV5EnableEpochs()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed, world := runPostTransferCall(t, sameRecipient, tc.epochs)

			require.Equal(t, test.ChildAddress, observed.calledBy)
			require.Equal(t, 1, observed.numTransfers)
			require.Equal(t, retargetTransferAmount, observed.value)

			require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, test.ChildAddress))
		})
	}
}

// Test_PostTransferRetarget_PendingTransfersKeepCallValue covers the retargeted call whose transfers
// are settled by this very built-in run: the "at sender" leg, where naming the payee in args[0] is
// the correct encoding. Nothing is settled on entry, so everything is the payee's call value.
func Test_PostTransferRetarget_PendingTransfersKeepCallValue(t *testing.T) {
	// [payee, numTransfers, token, nonce, value, function]
	atSender := callBuiltinDirectly(test.ChildAddress, [][]byte{
		retargetedAddress,
		{0x01},
		test.KDATestTokenName,
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		big.NewInt(retargetTransferAmount).Bytes(),
		[]byte("deposit"),
	})

	for _, tc := range []struct {
		name   string
		epochs blockchainConfig.EnableEpochs
	}{
		{name: "preFork", epochs: preForkV5EnableEpochs()},
		{name: "postFork", epochs: postForkV5EnableEpochs()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed, world := runPostTransferCall(t, atSender, tc.epochs)

			require.Equal(t, retargetedAddress, observed.calledBy)
			require.Equal(t, 1, observed.numTransfers)
			require.Equal(t, retargetTransferAmount, observed.value)

			require.Equal(t, retargetTransferAmount, kdaBalanceOf(t, world, retargetedAddress))
			require.Zero(t, kdaBalanceOf(t, world, test.ChildAddress))
		})
	}
}
