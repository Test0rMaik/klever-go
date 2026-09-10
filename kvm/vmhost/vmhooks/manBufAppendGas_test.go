package vmhooks_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/klever-io/klever-go/kvm/executor"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/require"
)

// The gas schedule used by these tests (kvmConfig.MakeGasMapForTests) prices every entry at 1, so
// the gas a single append costs is exactly:
//
//	1 (flat) + len(data) + len(accumulator) when the append has to reallocate, 1 + len(data) otherwise
//
// which makes every figure below readable as a byte count.
const (
	appendFlatCost      = uint64(1)
	appendGasPerByte    = uint64(1)
	appendTestGasBudget = uint64(1) << 40
)

// appendDriver drives one of the two append hooks behind a common signature, so every comparison
// in this file runs against both MBufferAppend and MBufferAppendBytes
type appendDriver struct {
	name   string
	append func(t *testing.T, hooks *vmhooks.VMHooksImpl, accumulatorHandle int32, data []byte) int32
}

func appendDrivers() []appendDriver {
	const memOffset = executor.MemPtr(0)

	return []appendDriver{
		{
			name: "MBufferAppend",
			append: func(t *testing.T, hooks *vmhooks.VMHooksImpl, accumulatorHandle int32, data []byte) int32 {
				t.Helper()
				dataHandle := hooks.GetManagedTypesContext().NewManagedBufferFromBytes(data)
				return hooks.MBufferAppend(accumulatorHandle, dataHandle)
			},
		},
		{
			name: "MBufferAppendBytes",
			append: func(t *testing.T, hooks *vmhooks.VMHooksImpl, accumulatorHandle int32, data []byte) int32 {
				t.Helper()
				writeToMemory(t, hooks, memOffset, data)
				return hooks.MBufferAppendBytes(accumulatorHandle, memOffset, executor.MemLength(len(data)))
			},
		},
	}
}

// buildBufferByAppending performs appendCount appends of chunkSize bytes onto a fresh buffer and
// returns the gas the appends consumed. Everything that is not the hook call itself - allocating
// the source buffer, writing the chunk into WASM memory - happens outside the measured region.
func buildBufferByAppending(
	t *testing.T,
	driver appendDriver,
	fixAuditChangesV5 bool,
	appendCount int,
	chunkSize int,
) uint64 {
	t.Helper()

	hooks := newManBufHooks(t, fixAuditChangesV5)
	resetGas(hooks, appendTestGasBudget)

	accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
	chunk := bytes.Repeat([]byte{'x'}, chunkSize)

	var gasUsed uint64
	for i := 0; i < appendCount; i++ {
		gasUsed += gasConsumedBy(hooks, func() {
			require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, chunk))
		})
	}

	require.NoError(t, hooks.GetRuntimeContext().GetAllErrors())
	require.Equal(t, int32(appendCount*chunkSize), hooks.GetManagedTypesContext().GetLength(accumulatorHandle))

	return gasUsed
}

// unconditionalAccumulatorCharge is what charging the accumulator on every append - regardless of
// whether the append actually copies it - would have billed for the same loop: the sum of every
// intermediate length, which is quadratic in the number of appends. It is not what the code does;
// it is the figure the tests below compare against.
func unconditionalAccumulatorCharge(appendCount int, chunkSize int) uint64 {
	total := uint64(0)
	for i := 0; i < appendCount; i++ {
		total += uint64(i * chunkSize)
	}

	return total * appendGasPerByte
}

// On the mainnet schedule (MBufferAppend 2000, DataCopyPerByte 50) the "1000 appends of 100
// bytes" row below translates to 7.0 M gas pre-fork and 12.1 M post-fork, against 2.50 B had the
// accumulator been charged on every append.
//
// TestAppendGasPreForkVersusPostFork is the headline comparison: for a buffer built in a loop, the
// post-fork charge must stay within a constant factor of the pre-fork one. The accumulator copy is
// real work and has to be paid for, but Go-style geometric growth copies each byte a bounded number
// of times over the whole build, so the total stays linear in the buffer's final length.
func TestAppendGasPreForkVersusPostFork(t *testing.T) {
	scenarios := []struct {
		name        string
		appendCount int
		chunkSize   int
	}{
		{name: "10 appends of 32 bytes", appendCount: 10, chunkSize: 32},
		{name: "100 appends of 32 bytes", appendCount: 100, chunkSize: 32},
		{name: "1000 appends of 10 bytes", appendCount: 1000, chunkSize: 10},
		{name: "1000 appends of 100 bytes", appendCount: 1000, chunkSize: 100},
		{name: "2000 appends of 1 byte", appendCount: 2000, chunkSize: 1},
	}

	for _, driver := range appendDrivers() {
		t.Run(driver.name, func(t *testing.T) {
			for _, scenario := range scenarios {
				t.Run(scenario.name, func(t *testing.T) {
					preFork := buildBufferByAppending(t, driver, false, scenario.appendCount, scenario.chunkSize)
					postFork := buildBufferByAppending(t, driver, true, scenario.appendCount, scenario.chunkSize)

					finalLength := uint64(scenario.appendCount * scenario.chunkSize)
					quadratic := preFork + unconditionalAccumulatorCharge(scenario.appendCount, scenario.chunkSize)

					t.Logf("pre-fork %d | post-fork %d (%.2fx) | charging the accumulator every time would be %d (%.2fx)",
						preFork, postFork, float64(postFork)/float64(preFork),
						quadratic, float64(quadratic)/float64(preFork))

					// the pre-fork charge is the flat cost plus the incoming bytes, nothing else
					require.Equal(t,
						uint64(scenario.appendCount)*appendFlatCost+finalLength*appendGasPerByte,
						preFork,
						"pre-fork charging must be untouched by the fix")

					// post-fork pays for the reallocation copies on top, and geometric growth
					// copies each byte fewer than twice across the whole build
					require.Greater(t, postFork, preFork, "the reallocation copies must be charged")
					require.LessOrEqual(t, postFork-preFork, 2*finalLength*appendGasPerByte,
						"the accumulator copies charged must stay below twice the final length")
				})
			}
		})
	}
}

// TestAppendGasGrowsLinearlyInTheNumberOfAppends is the asymptotics guard. Doubling the number of
// appends must roughly double the gas; a quadratic charge would roughly quadruple it. This is the
// property that a repricing cannot buy back - a contract that fits its gas limit today must not
// stop fitting it because it appends in a loop.
func TestAppendGasGrowsLinearlyInTheNumberOfAppends(t *testing.T) {
	const chunkSize = 20
	const baseAppendCount = 500

	for _, driver := range appendDrivers() {
		t.Run(driver.name, func(t *testing.T) {
			single := buildBufferByAppending(t, driver, true, baseAppendCount, chunkSize)
			double := buildBufferByAppending(t, driver, true, 2*baseAppendCount, chunkSize)

			growth := float64(double) / float64(single)
			quadraticSingle := unconditionalAccumulatorCharge(baseAppendCount, chunkSize)
			quadraticDouble := unconditionalAccumulatorCharge(2*baseAppendCount, chunkSize)

			t.Logf("%d appends: %d gas | %d appends: %d gas | growth %.2fx (quadratic charging would grow %.2fx)",
				baseAppendCount, single, 2*baseAppendCount, double, growth,
				float64(quadraticDouble)/float64(quadraticSingle))

			require.Less(t, growth, 2.5,
				"doubling the appends must roughly double the gas, not quadruple it")
		})
	}
}

// TestAppendChargesAccumulatorOnlyWhenItReallocates pins the exact per-call gas either side of a
// reallocation. A buffer seeded through SetBytes has len == cap, so the append right after it does
// copy the whole accumulator and is charged for it; the append after that fits in the capacity the
// first one allocated, copies nothing, and is charged only for the incoming bytes.
func TestAppendChargesAccumulatorOnlyWhenItReallocates(t *testing.T) {
	const accumulatorLength = 100
	const chunkSize = 10

	for _, driver := range appendDrivers() {
		t.Run(driver.name, func(t *testing.T) {
			hooks := newManBufHooks(t, true)
			resetGas(hooks, appendTestGasBudget)

			accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
			hooks.GetManagedTypesContext().SetBytes(accumulatorHandle, bytes.Repeat([]byte{'a'}, accumulatorLength))
			chunk := bytes.Repeat([]byte{'x'}, chunkSize)

			reallocating := gasConsumedBy(hooks, func() {
				require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, chunk))
			})
			inPlace := gasConsumedBy(hooks, func() {
				require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, chunk))
			})

			t.Logf("append that reallocates: %d gas | append into spare capacity: %d gas", reallocating, inPlace)

			require.Equal(t,
				appendFlatCost+uint64(chunkSize)*appendGasPerByte+uint64(accumulatorLength)*appendGasPerByte,
				reallocating,
				"the append after SetBytes copies the accumulator and must pay for it")
			require.Equal(t,
				appendFlatCost+uint64(chunkSize)*appendGasPerByte,
				inPlace,
				"an append into spare capacity copies nothing but the incoming bytes")
		})
	}
}

// TestAppendRejectsAnUnfundedReallocation is the security regression guard for the original audit
// finding: a contract must not be able to force the copy of a large accumulator while paying only
// for the byte it appends.
func TestAppendRejectsAnUnfundedReallocation(t *testing.T) {
	const accumulatorLength = 1_000_000
	accumulator := bytes.Repeat([]byte{'a'}, accumulatorLength)
	oneByte := []byte("b")

	// what the pre-fork path charges: the flat cost and the single incoming byte
	const flatAndIncomingByte = appendFlatCost + appendGasPerByte

	for _, driver := range appendDrivers() {
		t.Run(driver.name, func(t *testing.T) {
			t.Run("post-fork: the copy is refused when only the incoming byte is funded", func(t *testing.T) {
				hooks := newManBufHooks(t, true)
				accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
				hooks.GetManagedTypesContext().SetBytes(accumulatorHandle, accumulator)
				resetGas(hooks, flatAndIncomingByte)

				require.Equal(t, int32(1), driver.append(t, hooks, accumulatorHandle, oneByte))
				requireNotEnoughGas(t, hooks)
				requireBufferEquals(t, hooks, accumulatorHandle, accumulator)
			})

			t.Run("post-fork: funding the copy lets it through", func(t *testing.T) {
				hooks := newManBufHooks(t, true)
				accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
				hooks.GetManagedTypesContext().SetBytes(accumulatorHandle, accumulator)
				resetGas(hooks, flatAndIncomingByte+uint64(accumulatorLength)*appendGasPerByte)

				require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, oneByte))
				require.NoError(t, hooks.GetRuntimeContext().GetAllErrors())
				require.Zero(t, hooks.GetMeteringContext().GasLeft())
				requireBufferEquals(t, hooks, accumulatorHandle, append(bytes.Clone(accumulator), oneByte...))
			})

			t.Run("pre-fork: the same budget still goes through, unchanged", func(t *testing.T) {
				hooks := newManBufHooks(t, false)
				accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
				hooks.GetManagedTypesContext().SetBytes(accumulatorHandle, accumulator)
				resetGas(hooks, flatAndIncomingByte)

				require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, oneByte))
				requireBufferEquals(t, hooks, accumulatorHandle, append(bytes.Clone(accumulator), oneByte...))
			})
		})
	}
}

// TestRepeatedForcedReallocationsStayPaidFor closes the amortisation argument from the attacker's
// side: forcing the copy over and over is not free, because after each reallocation the capacity
// doubles and the accumulator has to be grown back through paid appends before the next copy.
func TestRepeatedForcedReallocationsStayPaidFor(t *testing.T) {
	const accumulatorLength = 10_000
	const rounds = 50

	for _, driver := range appendDrivers() {
		t.Run(driver.name, func(t *testing.T) {
			hooks := newManBufHooks(t, true)
			resetGas(hooks, appendTestGasBudget)

			accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
			hooks.GetManagedTypesContext().SetBytes(accumulatorHandle, bytes.Repeat([]byte{'a'}, accumulatorLength))
			oneByte := []byte("b")

			copiesCharged := 0
			for i := 0; i < rounds; i++ {
				gasUsed := gasConsumedBy(hooks, func() {
					require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, oneByte))
				})
				if gasUsed > appendFlatCost+appendGasPerByte {
					copiesCharged++
				}
			}

			t.Logf("%d single-byte appends onto a %d byte accumulator triggered %d charged copies",
				rounds, accumulatorLength, copiesCharged)

			require.Equal(t, 1, copiesCharged,
				"only the first append reallocates; the capacity doubling absorbs the rest")
		})
	}
}

// appendChargeModel is an independent statement of what an append must charge, written from the
// policy rather than from the code: the incoming bytes are always copied, the accumulator is
// copied only when the append does not fit the current capacity, and capacity doubles or jumps
// straight to what is needed when doubling still does not fit.
type appendChargeModel struct{ length, capacity int }

func (m *appendChargeModel) reseed(byteLen int) {
	m.length, m.capacity = byteLen, byteLen
}

func (m *appendChargeModel) charge(dataLength int) uint64 {
	charged := dataLength
	if m.length+dataLength > m.capacity {
		charged += m.length

		grown := 2 * m.capacity
		if grown < m.length+dataLength {
			grown = m.length + dataLength
		}
		m.capacity = grown
	}
	m.length += dataLength

	return uint64(charged) * appendGasPerByte
}

// TestAppendChargeMatchesTheModelOnRandomSequences drives random interleavings of appends and
// reseeds - a reseed collapses capacity back to length, so the append after one always
// reallocates - and compares the gas actually consumed against the model above. The fixed
// scenarios elsewhere in this file pin the shapes that were reasoned about; this one covers the
// interleavings that were not, which is where a repricing would otherwise hide.
func TestAppendChargeMatchesTheModelOnRandomSequences(t *testing.T) {
	for _, driver := range appendDrivers() {
		t.Run(driver.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(1))

			for sequence := 0; sequence < 200; sequence++ {
				hooks := newManBufHooks(t, true)
				resetGas(hooks, appendTestGasBudget)

				accumulatorHandle := hooks.GetManagedTypesContext().NewManagedBuffer()
				model := &appendChargeModel{}

				for step := 0; step < 1+rng.Intn(25); step++ {
					if rng.Intn(4) == 0 {
						seedLength := rng.Intn(300)
						hooks.GetManagedTypesContext().SetBytes(accumulatorHandle, make([]byte, seedLength))
						model.reseed(seedLength)
						continue
					}

					dataLength := rng.Intn(200)
					expected := appendFlatCost + model.charge(dataLength)

					actual := gasConsumedBy(hooks, func() {
						require.Equal(t, int32(0), driver.append(t, hooks, accumulatorHandle, make([]byte, dataLength)))
					})

					require.Equalf(t, expected, actual,
						"sequence %d step %d: appending %d bytes onto length %d",
						sequence, step, dataLength, model.length-dataLength)
				}

				require.NoError(t, hooks.GetRuntimeContext().GetAllErrors())
				require.Equal(t, int32(model.length), hooks.GetManagedTypesContext().GetLength(accumulatorHandle))
			}
		})
	}
}
