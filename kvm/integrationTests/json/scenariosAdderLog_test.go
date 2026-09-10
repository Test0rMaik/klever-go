package vmjsonintegrationtest

import (
	"testing"

	"github.com/klever-io/klever-go/config"
)

const expectedAdderLog = `starting log:
GetFunctionNames: [add add_payable getSum init upgrade]
ValidateFunctionArities: true
GetFunctionNames: [add add_payable getSum init upgrade]
HasFunction(init): true
CallFunction(init):
VM hook begin: CheckNoPayment()
GetPointsUsed: 5
SetPointsUsed: 105
VM hook end:   CheckNoPayment()
VM hook begin: GetNumArguments()
GetPointsUsed: 125
SetPointsUsed: 225
VM hook end:   GetNumArguments()
VM hook begin: BigIntGetUnsignedArgument(0, -101)
GetPointsUsed: 315
SetPointsUsed: 1315
VM hook end:   BigIntGetUnsignedArgument(0, -101)
VM hook begin: MBufferSetBytes(-102, 131072, 3)
GetPointsUsed: 1405
GetPointsUsed: 1405
SetPointsUsed: 3405
GetPointsUsed: 3405
GetPointsUsed: 3405
SetPointsUsed: 3555
VM hook end:   MBufferSetBytes(-102, 131072, 3)
VM hook begin: MBufferFromBigIntUnsigned(-103, -101)
GetPointsUsed: 3645
GetPointsUsed: 3645
SetPointsUsed: 5645
GetPointsUsed: 5645
GetPointsUsed: 5645
SetPointsUsed: 5695
VM hook end:   MBufferFromBigIntUnsigned(-103, -101)
VM hook begin: MBufferStorageStore(-102, -103)
GetPointsUsed: 5715
GetPointsUsed: 5715
SetPointsUsed: 80715
GetPointsUsed: 80715
GetPointsUsed: 80715
SetPointsUsed: 80865
GetPointsUsed: 80865
GetPointsUsed: 80865
SetPointsUsed: 80915
GetPointsUsed: 80915
GetPointsUsed: 80915
SetPointsUsed: 80915
GetPointsUsed: 80915
GetPointsUsed: 80915
SetPointsUsed: 90915
VM hook end:   MBufferStorageStore(-102, -103)
GetPointsUsed: 90930
GetPointsUsed: 90930
GetPointsUsed: 90930
GetPointsUsed: 90930
Reset: true
SetPointsUsed: 0
SetGasLimit: 9223372036853633107
SetBreakpointValue: 0
HasFunction(getSum): true
CallFunction(getSum):
VM hook begin: CheckNoPayment()
GetPointsUsed: 5
SetPointsUsed: 105
VM hook end:   CheckNoPayment()
VM hook begin: GetNumArguments()
GetPointsUsed: 125
SetPointsUsed: 225
VM hook end:   GetNumArguments()
VM hook begin: MBufferSetBytes(-101, 131072, 3)
GetPointsUsed: 320
GetPointsUsed: 320
SetPointsUsed: 2320
GetPointsUsed: 2320
GetPointsUsed: 2320
SetPointsUsed: 2470
VM hook end:   MBufferSetBytes(-101, 131072, 3)
VM hook begin: MBufferStorageLoad(-101, -102)
GetPointsUsed: 2555
GetPointsUsed: 2555
SetPointsUsed: 2705
GetPointsUsed: 2705
SetPointsUsed: 2755
GetPointsUsed: 2755
GetPointsUsed: 2755
SetPointsUsed: 18042
GetPointsUsed: 18042
GetPointsUsed: 18042
SetPointsUsed: 18092
VM hook end:   MBufferStorageLoad(-101, -102)
VM hook begin: MBufferToBigIntUnsigned(-102, -103)
GetPointsUsed: 18162
GetPointsUsed: 18162
SetPointsUsed: 20162
GetPointsUsed: 20162
GetPointsUsed: 20162
SetPointsUsed: 20212
VM hook end:   MBufferToBigIntUnsigned(-102, -103)
VM hook begin: BigIntFinishUnsigned(-103)
GetPointsUsed: 20232
SetPointsUsed: 21232
GetPointsUsed: 21232
SetPointsUsed: 22232
VM hook end:   BigIntFinishUnsigned(-103)
GetPointsUsed: 22237
GetPointsUsed: 22237
GetPointsUsed: 22237
GetPointsUsed: 22237
Reset: true
SetPointsUsed: 0
SetGasLimit: 3857300
SetBreakpointValue: 0
HasFunction(add): true
CallFunction(add):
VM hook begin: CheckNoPayment()
GetPointsUsed: 5
SetPointsUsed: 105
VM hook end:   CheckNoPayment()
VM hook begin: GetNumArguments()
GetPointsUsed: 125
SetPointsUsed: 225
VM hook end:   GetNumArguments()
VM hook begin: BigIntGetUnsignedArgument(0, -101)
GetPointsUsed: 315
SetPointsUsed: 1315
VM hook end:   BigIntGetUnsignedArgument(0, -101)
VM hook begin: MBufferSetBytes(-102, 131072, 3)
GetPointsUsed: 1405
GetPointsUsed: 1405
SetPointsUsed: 3405
GetPointsUsed: 3405
GetPointsUsed: 3405
SetPointsUsed: 3555
VM hook end:   MBufferSetBytes(-102, 131072, 3)
VM hook begin: MBufferStorageLoad(-102, -103)
GetPointsUsed: 3645
GetPointsUsed: 3645
SetPointsUsed: 3795
GetPointsUsed: 3795
SetPointsUsed: 3845
GetPointsUsed: 3845
GetPointsUsed: 3845
SetPointsUsed: 19132
GetPointsUsed: 19132
GetPointsUsed: 19132
SetPointsUsed: 19182
VM hook end:   MBufferStorageLoad(-102, -103)
VM hook begin: MBufferToBigIntUnsigned(-103, -104)
GetPointsUsed: 19252
GetPointsUsed: 19252
SetPointsUsed: 21252
GetPointsUsed: 21252
GetPointsUsed: 21252
SetPointsUsed: 21302
VM hook end:   MBufferToBigIntUnsigned(-103, -104)
VM hook begin: BigIntAdd(-104, -104, -101)
GetPointsUsed: 21352
SetPointsUsed: 23352
VM hook end:   BigIntAdd(-104, -104, -101)
VM hook begin: MBufferFromBigIntUnsigned(-105, -104)
GetPointsUsed: 23437
GetPointsUsed: 23437
SetPointsUsed: 25437
GetPointsUsed: 25437
GetPointsUsed: 25437
SetPointsUsed: 25487
VM hook end:   MBufferFromBigIntUnsigned(-105, -104)
VM hook begin: MBufferStorageStore(-102, -105)
GetPointsUsed: 25507
GetPointsUsed: 25507
SetPointsUsed: 100507
GetPointsUsed: 100507
GetPointsUsed: 100507
SetPointsUsed: 100657
GetPointsUsed: 100657
GetPointsUsed: 100657
SetPointsUsed: 100707
GetPointsUsed: 100707
GetPointsUsed: 100707
SetPointsUsed: 100707
GetPointsUsed: 100707
GetPointsUsed: 100707
SetPointsUsed: 101707
VM hook end:   MBufferStorageStore(-102, -105)
GetPointsUsed: 101722
GetPointsUsed: 101722
GetPointsUsed: 101722
GetPointsUsed: 101722
Clean: true
`

func TestRustAdderLog(t *testing.T) {
	ScenariosTest(t).
		Folder("adder/scenarios").
		WithExecutorLogs().
		Run().
		CheckNoError().
		CheckLog(expectedAdderLog)
}

// expectedAdderLogPreFork is the adder log exactly as the release this branch forked from
// produced it. Running the same scenarios with FixAuditChangesV5 scheduled in the future
// must reproduce it byte for byte: that pins the pre-fork gas path, which no per-hook test
// can see because the pre-fork helpers overdraw silently instead of failing.
const expectedAdderLogPreFork = `starting log:
GetFunctionNames: [add add_payable getSum init upgrade]
ValidateFunctionArities: true
GetFunctionNames: [add add_payable getSum init upgrade]
HasFunction(init): true
CallFunction(init):
VM hook begin: CheckNoPayment()
GetPointsUsed: 5
SetPointsUsed: 105
VM hook end:   CheckNoPayment()
VM hook begin: GetNumArguments()
GetPointsUsed: 125
SetPointsUsed: 225
VM hook end:   GetNumArguments()
VM hook begin: BigIntGetUnsignedArgument(0, -101)
GetPointsUsed: 315
SetPointsUsed: 1315
VM hook end:   BigIntGetUnsignedArgument(0, -101)
VM hook begin: MBufferSetBytes(-102, 131072, 3)
GetPointsUsed: 1405
SetPointsUsed: 3405
GetPointsUsed: 3405
SetPointsUsed: 3555
VM hook end:   MBufferSetBytes(-102, 131072, 3)
VM hook begin: MBufferFromBigIntUnsigned(-103, -101)
GetPointsUsed: 3645
SetPointsUsed: 5645
VM hook end:   MBufferFromBigIntUnsigned(-103, -101)
VM hook begin: MBufferStorageStore(-102, -103)
GetPointsUsed: 5665
SetPointsUsed: 80665
GetPointsUsed: 80665
GetPointsUsed: 80665
SetPointsUsed: 80665
GetPointsUsed: 80665
GetPointsUsed: 80665
SetPointsUsed: 90665
VM hook end:   MBufferStorageStore(-102, -103)
GetPointsUsed: 90680
GetPointsUsed: 90680
GetPointsUsed: 90680
GetPointsUsed: 90680
Reset: true
SetPointsUsed: 0
SetGasLimit: 9223372036853633107
SetBreakpointValue: 0
HasFunction(getSum): true
CallFunction(getSum):
VM hook begin: CheckNoPayment()
GetPointsUsed: 5
SetPointsUsed: 105
VM hook end:   CheckNoPayment()
VM hook begin: GetNumArguments()
GetPointsUsed: 125
SetPointsUsed: 225
VM hook end:   GetNumArguments()
VM hook begin: MBufferSetBytes(-101, 131072, 3)
GetPointsUsed: 320
SetPointsUsed: 2320
GetPointsUsed: 2320
SetPointsUsed: 2470
VM hook end:   MBufferSetBytes(-101, 131072, 3)
VM hook begin: MBufferStorageLoad(-101, -102)
GetPointsUsed: 2555
SetPointsUsed: 2605
GetPointsUsed: 2605
GetPointsUsed: 2605
SetPointsUsed: 17892
VM hook end:   MBufferStorageLoad(-101, -102)
VM hook begin: MBufferToBigIntUnsigned(-102, -103)
GetPointsUsed: 17962
SetPointsUsed: 19962
VM hook end:   MBufferToBigIntUnsigned(-102, -103)
VM hook begin: BigIntFinishUnsigned(-103)
GetPointsUsed: 19982
SetPointsUsed: 20982
GetPointsUsed: 20982
SetPointsUsed: 21982
VM hook end:   BigIntFinishUnsigned(-103)
GetPointsUsed: 21987
GetPointsUsed: 21987
GetPointsUsed: 21987
GetPointsUsed: 21987
Reset: true
SetPointsUsed: 0
SetGasLimit: 3857300
SetBreakpointValue: 0
HasFunction(add): true
CallFunction(add):
VM hook begin: CheckNoPayment()
GetPointsUsed: 5
SetPointsUsed: 105
VM hook end:   CheckNoPayment()
VM hook begin: GetNumArguments()
GetPointsUsed: 125
SetPointsUsed: 225
VM hook end:   GetNumArguments()
VM hook begin: BigIntGetUnsignedArgument(0, -101)
GetPointsUsed: 315
SetPointsUsed: 1315
VM hook end:   BigIntGetUnsignedArgument(0, -101)
VM hook begin: MBufferSetBytes(-102, 131072, 3)
GetPointsUsed: 1405
SetPointsUsed: 3405
GetPointsUsed: 3405
SetPointsUsed: 3555
VM hook end:   MBufferSetBytes(-102, 131072, 3)
VM hook begin: MBufferStorageLoad(-102, -103)
GetPointsUsed: 3645
SetPointsUsed: 3695
GetPointsUsed: 3695
GetPointsUsed: 3695
SetPointsUsed: 18982
VM hook end:   MBufferStorageLoad(-102, -103)
VM hook begin: MBufferToBigIntUnsigned(-103, -104)
GetPointsUsed: 19052
SetPointsUsed: 21052
VM hook end:   MBufferToBigIntUnsigned(-103, -104)
VM hook begin: BigIntAdd(-104, -104, -101)
GetPointsUsed: 21102
SetPointsUsed: 23102
VM hook end:   BigIntAdd(-104, -104, -101)
VM hook begin: MBufferFromBigIntUnsigned(-105, -104)
GetPointsUsed: 23187
SetPointsUsed: 25187
VM hook end:   MBufferFromBigIntUnsigned(-105, -104)
VM hook begin: MBufferStorageStore(-102, -105)
GetPointsUsed: 25207
SetPointsUsed: 100207
GetPointsUsed: 100207
GetPointsUsed: 100207
SetPointsUsed: 100207
GetPointsUsed: 100207
GetPointsUsed: 100207
SetPointsUsed: 101207
VM hook end:   MBufferStorageStore(-102, -105)
GetPointsUsed: 101222
GetPointsUsed: 101222
GetPointsUsed: 101222
GetPointsUsed: 101222
Clean: true
`

func TestRustAdderLogPreFork(t *testing.T) {
	ScenariosTest(t).
		Folder("adder/scenarios").
		WithEnableEpochs(config.EnableEpochs{FixAuditChangesV5: 1_000_000}).
		WithExecutorLogs().
		Run().
		CheckNoError().
		CheckLog(expectedAdderLogPreFork)
}
