package dataValidators_test

import (
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/core/process/dataValidators"
	"github.com/klever-io/klever-go/crypto"
	cryptoMock "github.com/klever-io/klever-go/crypto/mock"
	disabledSig "github.com/klever-io/klever-go/crypto/signing/disabled/singlesig"
	"github.com/klever-io/klever-go/data"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/data/transaction"
	"github.com/klever-io/klever-go/kvm/mock/stub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTxValidatorWith builds a validator with the pieces a test wants to vary,
// falling back to the same defaults generateTxValidator uses.
func newTxValidatorWith(
	t *testing.T,
	accounts state.AccountsAdapter,
	singleSigner crypto.SingleSigner,
	kAppController kapp.KAppController,
) process.TxValidator {
	t.Helper()

	if accounts == nil {
		accounts = getAccAdapter(0)
	}
	if singleSigner == nil {
		singleSigner = &cryptoMock.SingleSignerStub{}
	}
	if kAppController == nil {
		kAppController = getKAppController()
	}

	txValidator, err := dataValidators.NewTxValidator(
		accounts,
		storageTest,
		getTxPoolsHolder(),
		&mock.WhiteListHandlerStub{},
		mock.NewPubkeyConverterMock(32),
		singleSigner,
		&cryptoMock.KeyGenMock{
			PublicKeyFromByteArrayMock: func(b []byte) (crypto.PublicKey, error) {
				return &cryptoMock.PublicKeyMock{}, nil
			},
		},
		kAppController,
		core.MaxTxNonceDeltaAllowed,
	)
	require.NoError(t, err)

	return txValidator
}

// asInterceptedTx pairs a tx handler with intercepted data, the shape the
// interceptors actually feed the validator; without it CheckTxValidity has no
// hash to verify signatures against.
func asInterceptedTx(handler process.TxValidatorHandler) process.TxValidatorHandler {
	return struct {
		process.InterceptedData
		process.TxValidatorHandler
	}{
		InterceptedData:    &mock.InterceptedDataStub{},
		TxValidatorHandler: handler,
	}
}

// kdaFeeTxHandler builds a tx that pays its fee in a KDA asset instead of KLV.
func kdaFeeTxHandler(sender []byte, klvFee int64, kdaFee *transaction.Transaction_KDAFee) process.TxValidatorHandler {
	handler := getTxValidatorHandler(sender, klvFee, 0).(*mock.TxValidatorHandlerStub)
	handler.KDAFeeCalled = func() data.KDAFeeHandler {
		return kdaFee
	}

	return handler
}

func TestTxValidator_CheckTxValidityRejectsOutOfRangeNonces(t *testing.T) {
	t.Parallel()

	address := makeAddressMock("address")

	// the account sits at nonce 10; anything below it, or too far above it,
	// must never reach the pool
	accounts := &mock.AccountsStub{
		GetExistingAccountCalled: func(addr []byte) (state.AccountHandler, error) {
			acc, _ := state.NewUserAccount(addr)
			acc.Nonce = 10
			return acc, nil
		},
	}

	t.Run("a nonce below the account nonce is rejected", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, accounts, nil, nil)

		err := txValidator.CheckTxValidity(getTxValidatorHandler(address, 0, 9))

		require.ErrorIs(t, err, process.ErrWrongTransaction)
		assert.Contains(t, err.Error(), "lowerNonceInTx: true")
		assert.Contains(t, err.Error(), "veryHighNonceInTx: false")
	})

	t.Run("a nonce beyond the allowed delta is rejected", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, accounts, nil, nil)

		tooHigh := uint64(10 + core.MaxTxNonceDeltaAllowed + 1)
		err := txValidator.CheckTxValidity(getTxValidatorHandler(address, 0, tooHigh))

		require.ErrorIs(t, err, process.ErrWrongTransaction)
		assert.Contains(t, err.Error(), "lowerNonceInTx: false")
		assert.Contains(t, err.Error(), "veryHighNonceInTx: true")
	})

	t.Run("a nonce inside the allowed window is accepted", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, accounts, nil, nil)

		err := txValidator.CheckTxValidity(
			asInterceptedTx(getTxValidatorHandler(address, 0, 10+uint64(core.MaxTxNonceDeltaAllowed))))

		require.NoError(t, err)
	})
}

func TestTxValidator_CheckTxValidityWithKDAFee(t *testing.T) {
	t.Parallel()

	address := makeAddressMock("address")

	// a controller whose fee pool answers exactly what each case needs
	newController := func(validateErr error) kapp.KAppController {
		return &stub.KAppControllerStub{
			GetKDAFeesPoolKAppCalled: func() kapp.KDAFeesPoolKapp {
				return &stub.KDAFeesPoolKappStub{
					ValidateCalled: func(senderAddress []byte, klvFee int64, info data.KDAFeeHandler) error {
						return validateErr
					},
				}
			},
			GetForkControllerCalled: func() core.ForkController {
				return mock.NewForkControllerStub()
			},
		}
	}

	t.Run("a fee pool that rejects the asset fails the transaction", func(t *testing.T) {
		expectedErr := errors.New("pool is inactive")
		txValidator := newTxValidatorWith(t, nil, nil, newController(expectedErr))

		err := txValidator.CheckTxValidity(kdaFeeTxHandler(address, 100,
			&transaction.Transaction_KDAFee{KDA: []byte("KFI"), Amount: 50}))

		require.ErrorIs(t, err, expectedErr)
		assert.Contains(t, err.Error(), "fail to validate KDA fee")
	})

	t.Run("the balance check switches to the KDA asset", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, nil, nil, newController(nil))

		// the sender holds no KFI, so the shortfall is reported against KFI
		// rather than against the KLV fee the transaction declared
		err := txValidator.CheckTxValidity(kdaFeeTxHandler(address, 100,
			&transaction.Transaction_KDAFee{KDA: []byte("KFI"), Amount: 50}))

		require.ErrorIs(t, err, process.ErrInsufficientFunds)
		assert.Contains(t, err.Error(), "kda: KFI")
		assert.Contains(t, err.Error(), "wanted 50")
	})

	t.Run("a KDA fee the sender can cover passes", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, nil, nil, newController(nil))

		// a zero-amount KDA fee is covered by the sender's zero KFI balance,
		// so validation carries on past the balance check
		err := txValidator.CheckTxValidity(asInterceptedTx(kdaFeeTxHandler(address, 100,
			&transaction.Transaction_KDAFee{KDA: []byte("KFI"), Amount: 0})))

		require.NoError(t, err)
	})
}

func TestTxValidator_CheckTxValidityInsufficientKLVNamesKLV(t *testing.T) {
	t.Parallel()

	txValidator := newTxValidatorWith(t, getAccAdapter(10), nil, nil)

	err := txValidator.CheckTxValidity(getTxValidatorHandler(makeAddressMock("address"), 100, 0))

	require.ErrorIs(t, err, process.ErrInsufficientFunds)
	// with no KDA fee the asset defaults to KLV in the error message
	assert.Contains(t, err.Error(), "kda: KLV")
}

func TestTxValidator_CheckTxValiditySkipsSignatureChecksWhenSigningIsDisabled(t *testing.T) {
	t.Parallel()

	// an account whose only permission is not the one the tx asks for: with a
	// real signer this would fail at GetPermission, so reaching nil proves the
	// disabled signer short-circuits before any permission or signature work
	accounts := &mock.AccountsStub{
		GetExistingAccountCalled: func(addr []byte) (state.AccountHandler, error) {
			acc, _ := state.NewUserAccount(addr)
			acc.Permissions = []*state.Permission{{ID: 5, Type: state.Permission_User, Threshold: 1}}
			return acc, nil
		},
	}
	address := makeAddressMock("address")

	t.Run("the disabled signer accepts the transaction", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, accounts, &disabledSig.DisabledSingleSig{}, nil)

		require.NoError(t, txValidator.CheckTxValidity(asInterceptedTx(getTxValidatorHandler(address, 0, 0))))
	})

	t.Run("a real signer still resolves the permission and fails", func(t *testing.T) {
		txValidator := newTxValidatorWith(t, accounts, nil, nil)

		err := txValidator.CheckTxValidity(asInterceptedTx(getTxValidatorHandler(address, 0, 0)))

		require.ErrorIs(t, err, state.ErrInvalidPermissionID)
	})
}

func TestTxValidator_CheckTxWhiteList(t *testing.T) {
	t.Parallel()

	newValidator := func(whiteListed bool) process.TxValidator {
		txValidator, err := dataValidators.NewTxValidator(
			getAccAdapter(0),
			storageTest,
			getTxPoolsHolder(),
			&mock.WhiteListHandlerStub{
				IsWhiteListedCalled: func(interceptedData process.InterceptedData) bool {
					return whiteListed
				},
			},
			mock.NewPubkeyConverterMock(32),
			&cryptoMock.SingleSignerStub{},
			&cryptoMock.KeyGenMock{},
			getKAppController(),
			core.MaxTxNonceDeltaAllowed,
		)
		require.NoError(t, err)

		return txValidator
	}

	t.Run("a whitelisted intercepted data passes", func(t *testing.T) {
		require.NoError(t, newValidator(true).CheckTxWhiteList(&mock.InterceptedDataStub{}))
	})

	t.Run("anything else is refused", func(t *testing.T) {
		err := newValidator(false).CheckTxWhiteList(&mock.InterceptedDataStub{})

		require.ErrorIs(t, err, common.ErrTransactionIsNotWhitelisted)
	})
}
