package hostCoretest

import (
	"encoding/hex"
	"strings"
	"testing"

	blockchainConfig "github.com/klever-io/klever-go/config"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	test "github.com/klever-io/klever-go/kvm/testcommon"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/wasmer2"
	"github.com/klever-io/klever-go/vmcommon"
	"github.com/stretchr/testify/require"
)

// tableInitWasm assembles the fixture module. The middle is the passive element
// segment: 1,000 entries, each a single-byte funcref index, generated rather than
// pasted so the element count is visible instead of buried in a hex blob.
func tableInitWasm(t *testing.T) []byte {
	const (
		prefix     = "0061736d010000000104016000000304030000000407017001e807e8070503010001072903066d656d6f7279020008696e69745f6f6e65000111696e69745f6f6e655f74686f7573616e64000209ed07010100e807"
		suffix     = "0a1f0302000b0c00410041004101fc0c00000b0d004100410041e807fc0c00000b"
		segmentLen = 1000
	)
	t.Helper()

	code, err := hex.DecodeString(prefix + strings.Repeat("00", segmentLen) + suffix)
	require.NoError(t, err)

	return code
}

// newTableInitWorld seeds a world with the fixture contract. Callers that need two hosts
// to share a compiled-code cache pass the same world to both.
func newTableInitWorld(t *testing.T) *worldmock.MockWorld {
	t.Helper()

	world := worldmock.NewMockWorld()
	world.CreateAccount(test.UserAddress, world)
	scAccount := world.CreateSmartContractAccount(test.UserAddress, test.ParentAddress, tableInitWasm(t), world)
	world.PutAccount(scAccount)

	return world
}

func newTableInitHost(t *testing.T, world *worldmock.MockWorld, enableEpochs blockchainConfig.EnableEpochs) vmhost.VMHost {
	t.Helper()

	gasSchedule, err := blockchainConfig.LoadGasScheduleConfig("../../../config/node/gasScheduleV1.yaml")
	require.NoError(t, err)

	return test.NewTestHostBuilder(t).
		WithBlockchainHook(world).
		WithGasSchedule(gasSchedule).
		WithBuiltinFunctions().
		WithEnableEpochs(enableEpochs).
		WithExecutorFactory(wasmer2.ExecutorFactory()).
		Build()
}

func gasUsedForTableInit(t *testing.T, function string, enableEpochs blockchainConfig.EnableEpochs) uint64 {
	t.Helper()

	host := newTableInitHost(t, newTableInitWorld(t), enableEpochs)
	defer host.Reset()

	const gasProvided = uint64(50_000_000)
	vmOutput := runContractCall(t, host, function, gasProvided)
	require.Equal(t, vmcommon.Ok, vmOutput.ReturnCode, "%s should succeed", function)

	return gasProvided - vmOutput.GasRemaining
}

// TestGasUsed_TableInit_ScalesWithCopiedElementCount is the post-fork counterpart of
// TestGasUsed_TableGrow_FlatCostRegardlessOfSize: once the per-element charge is
// active, copying a thousand elements must cost measurably more than copying one.
func TestGasUsed_TableInit_ScalesWithCopiedElementCount(t *testing.T) {
	postFork := blockchainConfig.EnableEpochs{}

	initOne := gasUsedForTableInit(t, "init_one", postFork)
	initThousand := gasUsedForTableInit(t, "init_one_thousand", postFork)

	t.Logf("init_one=%d init_one_thousand=%d delta=%d", initOne, initThousand, int64(initThousand)-int64(initOne))

	// The schedule prices TableInitPerElement at 10, so 999 extra elements cost
	// 9,990 extra gas. Asserting the exact delta pins the charge to the element
	// count rather than merely to "more than before".
	require.Equal(t, uint64(9_990), initThousand-initOne,
		"table.init must be charged per copied element")
}

// TestGasUsed_TableInit_FlatCostBeforeFork keeps the pre-fork pricing observable:
// until the fork activates, the two endpoints must remain indistinguishable, so a
// re-syncing node replays historical blocks at the gas they were committed with.
func TestGasUsed_TableInit_FlatCostBeforeFork(t *testing.T) {
	preFork := blockchainConfig.EnableEpochs{FixAuditChangesV5: 10}

	initOne := gasUsedForTableInit(t, "init_one", preFork)
	initThousand := gasUsedForTableInit(t, "init_one_thousand", preFork)

	require.Equal(t, initOne, initThousand,
		"before the fork table.init must keep its flat cost")
}

// TestGasUsed_TableInit_FailsWhenGasCannotCoverTheCopy pins that a copy the caller cannot
// pay for fails out of gas and consumes the whole budget. It does not distinguish charging
// before the copy from charging after it - an out-of-gas reverts either way, so both
// orderings produce this same output. That ordering is pinned in the executor's own tests,
// where the table is observable after the trap.
func TestGasUsed_TableInit_FailsWhenGasCannotCoverTheCopy(t *testing.T) {
	postFork := blockchainConfig.EnableEpochs{}

	// Derived rather than hardcoded: the absolute figures are dominated by call overhead
	// read from the production schedule, so a literal would silently stop separating the
	// two cases the moment any unrelated charge on the path moved. The margin only has to
	// clear the one-element charge while staying well under the thousand-element one.
	const margin = uint64(1_000)
	budget := gasUsedForTableInit(t, "init_one", postFork) + margin

	world := newTableInitWorld(t)
	host := newTableInitHost(t, world, postFork)
	defer host.Reset()

	sufficientForOne := runContractCall(t, host, "init_one", budget)
	require.Equal(t, vmcommon.Ok, sufficientForOne.ReturnCode,
		"a one-element copy must fit in a budget derived from its own cost")

	underfunded := runContractCall(t, host, "init_one_thousand", budget)
	require.Equal(t, vmcommon.VMOutOfGas, underfunded.ReturnCode,
		"a thousand-element copy must run out of gas on a budget that only covers one, "+
			"not fail for some unrelated reason")
	require.Zero(t, underfunded.GasRemaining,
		"an out-of-gas failure must leave nothing unspent")
}

// TestGasUsed_TableInit_ChargeAppliesAtTheActivationEpoch covers the boundary itself. The
// other tests sit at the extremes - an activation epoch far in the future, or one long
// past - so nothing pins the first epoch on which the charge is due. The mock world runs
// at epoch 0, so an activation epoch of 0 is the activation epoch, and 1 is one short of
// it: this is what distinguishes a >= gate from a > gate.
func TestGasUsed_TableInit_ChargeAppliesAtTheActivationEpoch(t *testing.T) {
	atActivation := gasUsedForTableInit(t, "init_one_thousand",
		blockchainConfig.EnableEpochs{FixAuditChangesV5: 0})
	oneEpochShort := gasUsedForTableInit(t, "init_one_thousand",
		blockchainConfig.EnableEpochs{FixAuditChangesV5: 1})

	// 1,000 elements at the schedule's per-element price, charged in full on the very
	// first epoch the fork covers rather than the one after it.
	require.Equal(t, uint64(10_000), atActivation-oneEpochShort,
		"the charge must apply at the activation epoch, not from the epoch after it")
}

// TestGasUsed_TableInit_PriceIsAppliedAtInstantiationNotCompiledIn is the test for the
// property the design rests on. The per-element price is handed to the executor on every
// instantiation and read from a runtime global, rather than baked into the compiled body,
// because the compiled-code cache is keyed on code hash alone - it carries no record of the
// price in force when the artifact was produced.
//
// Both hosts share one world, so the second reuses the artifact the first compiled and
// cached. If the price were compiled in, the cached artifact would keep the price of the
// host that produced it and gas would depend on which node happened to hold a warm cache.
func TestGasUsed_TableInit_PriceIsAppliedAtInstantiationNotCompiledIn(t *testing.T) {
	world := newTableInitWorld(t)

	const gasProvided = uint64(50_000_000)
	measure := func(host vmhost.VMHost, function string) uint64 {
		out := runContractCall(t, host, function, gasProvided)
		require.Equal(t, vmcommon.Ok, out.ReturnCode, "%s should succeed", function)
		return gasProvided - out.GasRemaining
	}

	// Compile and cache the artifact under the post-fork price.
	postHost := newTableInitHost(t, world, blockchainConfig.EnableEpochs{})
	postThousand := measure(postHost, "init_one_thousand")
	postOne := measure(postHost, "init_one")
	postHost.Reset()
	require.Equal(t, uint64(9_990), postThousand-postOne, "post-fork host must charge per element")

	// A pre-fork host now instantiates from that cached artifact and must charge nothing
	// extra for the elements.
	preHost := newTableInitHost(t, world, blockchainConfig.EnableEpochs{FixAuditChangesV5: 10})
	preThousand := measure(preHost, "init_one_thousand")
	preOne := measure(preHost, "init_one")
	preHost.Reset()
	require.Equal(t, preOne, preThousand,
		"a pre-fork host reusing a post-fork artifact must not inherit its per-element price")

	// And back again, to show the artifact is not stuck at whichever price it first saw.
	againHost := newTableInitHost(t, world, blockchainConfig.EnableEpochs{})
	againThousand := measure(againHost, "init_one_thousand")
	againOne := measure(againHost, "init_one")
	againHost.Reset()
	require.Equal(t, uint64(9_990), againThousand-againOne,
		"a post-fork host reusing a pre-fork artifact must apply the per-element price")
}
