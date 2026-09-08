package dataValidators_test

import (
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/core/process/dataValidators"
	cryptoMock "github.com/klever-io/klever-go/crypto/mock"
	disabledSig "github.com/klever-io/klever-go/crypto/signing/disabled/singlesig"
	"github.com/klever-io/klever-go/data"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kvm/mock/stub"
	"github.com/stretchr/testify/require"
)

func TestTxValidator_KDAFeeRequiresFullSignedBalance(t *testing.T) {
	for _, v5 := range []bool{false, true} {
		for _, balance := range []int64{100, 50000} {
			name := "before V5"
			if v5 {
				name = "after V5"
			}
			if balance == 100 {
				name += "/only quote covered"
			} else {
				name += "/signed amount covered"
			}
			t.Run(name, func(t *testing.T) {
				address := makeAddressMock("sender")
				fee := &transaction.Transaction_KDAFee{KDA: []byte("KFI"), Amount: 50000}
				accounts := &mock.AccountsStub{GetExistingAccountCalled: func(addr []byte) (state.AccountHandler, error) {
					acc, err := state.NewUserAccount(addr)
					require.NoError(t, err)
					require.NoError(t, acc.AddToBalance(balance, fee.KDA, true))
					return acc, nil
				}}
				validated := false
				pool := &stub.KDAFeesPoolKappStub{ValidateCalled: func(sender []byte, klvFee int64, info data.KDAFeeHandler) error {
					validated = true
					require.Equal(t, address, sender)
					require.Equal(t, int64(1000), klvFee)
					require.Same(t, fee, info)
					return nil
				}}
				controller := &stub.KAppControllerStub{
					GetKDAFeesPoolKAppCalled: func() kapp.KDAFeesPoolKapp { return pool },
					GetForkControllerCalled:  func() core.ForkController { return mock.NewForkControllerStub().SetFork("FixAuditChangesV5", v5) },
				}
				validator, err := dataValidators.NewTxValidator(accounts, storageTest, getTxPoolsHolder(), &mock.WhiteListHandlerStub{}, mock.NewPubkeyConverterMock(32), &disabledSig.DisabledSingleSig{}, &cryptoMock.KeyGenMock{}, controller, core.MaxTxNonceDeltaAllowed)
				require.NoError(t, err)
				handler := getTxValidatorHandler(address, 1000, 0).(*mock.TxValidatorHandlerStub)
				handler.KDAFeeCalled = func() data.KDAFeeHandler { return fee }
				err = validator.CheckTxValidity(handler)
				if balance < fee.Amount {
					require.ErrorIs(t, err, process.ErrInsufficientFunds)
					require.Contains(t, err.Error(), "wanted 50000")
				} else {
					require.NoError(t, err)
				}
				require.True(t, validated)
			})
		}
	}
}
