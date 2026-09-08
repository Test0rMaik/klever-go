package kda_test

import (
	"errors"
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/kapp/kda"
	"github.com/klever-io/klever-go/crypto/hashing/sha256"
	cryptoMock "github.com/klever-io/klever-go/crypto/mock"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errAccessorTest = errors.New("accessor failure")

// newStandaloneKDAKApp builds a KDA KApp straight onto stubs, so each guard in
// the trie/marshalizer accessors can be driven on its own.
func newStandaloneKDAKApp(
	t *testing.T,
	marshalizer marshal.Marshalizer,
	cacher state.AccountsCacher,
) kapp.KDAKapp {
	t.Helper()

	if marshalizer == nil {
		marshalizer = marshal.NewProtoMarshalizer()
	}

	app, err := kda.NewKDAKApp(&kda.ArgsNewKDAKApp{
		Hasher:         &sha256.Sha256{},
		Marshalizer:    marshalizer,
		PubkeyConv:     cryptoMock.NewPubkeyConverterMock(32),
		ForkController: mock.NewForkControllerStub(),
	})
	require.NoError(t, err)
	require.NoError(t, app.SetAccountsCacher(cacher))

	return app
}

// kappWithTrie returns a KApp account whose data trie answers with the given
// value or error.
func kappWithTrie(value []byte, retrieveErr, saveErr error) state.KAppAccountHandler {
	return &mock.KAppAccountHandlerStub{
		DataTrieTrackerCalled: func() state.DataTrieTracker {
			return &mock.DataTrieTrackerStub{
				RetrieveValueCalled: func(key []byte) ([]byte, error) {
					return value, retrieveErr
				},
				SaveKeyValueCalled: func(key []byte, val []byte) error {
					return saveErr
				},
			}
		},
	}
}

func TestKDAKapp_UserAccountAccessorsPropagateErrors(t *testing.T) {
	t.Parallel()

	app, err := kda.NewKDAKApp(&kda.ArgsNewKDAKApp{
		Hasher:         &sha256.Sha256{},
		Marshalizer:    marshal.NewProtoMarshalizer(),
		PubkeyConv:     cryptoMock.NewPubkeyConverterMock(32),
		ForkController: mock.NewForkControllerStub(),
	})
	require.NoError(t, err)
	require.NoError(t, app.SetAccountsCacher(&mock.AccountsCacherStub{
		GetExistingUserCalled: func(address []byte) (state.UserAccountHandler, error) {
			return nil, errAccessorTest
		},
		LoadUserCalled: func(address []byte) (state.UserAccountHandler, error) {
			return nil, errAccessorTest
		},
	}))

	acc, err := app.GetExistingUserAccount(defaultSender)
	assert.Nil(t, acc)
	require.ErrorIs(t, err, errAccessorTest)

	acc, err = app.LoadUserAccount(defaultSender)
	assert.Nil(t, acc)
	require.ErrorIs(t, err, errAccessorTest)
}

func TestKDAKapp_GetKDAPropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("a KApp that cannot be loaded", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			GetExistingKappCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return nil, errAccessorTest
			},
		})

		_, _, err := app.GetKDA([]byte("KFI"))

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("a trie read that fails", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			GetExistingKappCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return kappWithTrie(nil, errAccessorTest, nil), nil
			},
		})

		_, _, err := app.GetKDA([]byte("KFI"))

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("stored bytes that are not a KDA record", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			GetExistingKappCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return kappWithTrie([]byte{0xFF, 0xFF, 0xFF}, nil, nil), nil
			},
		})

		_, _, err := app.GetKDA([]byte("KFI"))

		require.Error(t, err)
		require.NotErrorIs(t, err, common.ErrAssetNotFound)
	})

	t.Run("a nil asset id returns only the KApp handler", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			GetExistingKappCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return kappWithTrie(nil, nil, nil), nil
			},
		})

		kdaKapp, kdaData, err := app.GetKDA(nil)

		require.NoError(t, err)
		require.NotNil(t, kdaKapp)
		assert.Nil(t, kdaData)
	})
}

func TestKDAKapp_GetKDAUncachedPropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("a KApp that cannot be loaded", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			LoadKAppUncachedCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return nil, errAccessorTest
			},
		})

		_, err := app.GetKDAUncached([]byte("KFI"))

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("a trie read that fails", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			LoadKAppUncachedCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return kappWithTrie(nil, errAccessorTest, nil), nil
			},
		})

		_, err := app.GetKDAUncached([]byte("KFI"))

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("stored bytes that are not a KDA record", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			LoadKAppUncachedCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return kappWithTrie([]byte{0xFF, 0xFF, 0xFF}, nil, nil), nil
			},
		})

		_, err := app.GetKDAUncached([]byte("KFI"))

		require.Error(t, err)
		require.NotErrorIs(t, err, common.ErrAssetNotFound)
	})
}

func TestKDAKapp_GetStakingPropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("a KApp that cannot be loaded", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			GetExistingKappCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return nil, errAccessorTest
			},
		})

		_, _, err := app.GetStaking([]byte("KFI"))

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("a nil asset id returns only the KApp handler", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{
			GetExistingKappCalled: func(address []byte) (state.KAppAccountHandler, error) {
				return kappWithTrie(nil, nil, nil), nil
			},
		})

		stakingKapp, staking, err := app.GetStaking(nil)

		require.NoError(t, err)
		require.NotNil(t, stakingKapp)
		assert.Nil(t, staking)
	})
}

func TestKDAKapp_SettersPropagateErrors(t *testing.T) {
	t.Parallel()

	failingMarshalizer := &mock.MarshalizerStub{
		MarshalCalled: func(obj interface{}) ([]byte, error) {
			return nil, errAccessorTest
		},
	}

	t.Run("SetKDA reports a marshal failure", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, failingMarshalizer, &mock.AccountsCacherStub{})

		err := app.SetKDA(kappWithTrie(nil, nil, nil), []byte("KFI"), &kapps.KDAData{})

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("SetKDA reports a trie write failure", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{})

		err := app.SetKDA(kappWithTrie(nil, nil, errAccessorTest), []byte("KFI"), &kapps.KDAData{})

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("SetStaking reports a marshal failure", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, failingMarshalizer, &mock.AccountsCacherStub{})

		err := app.SetStaking(kappWithTrie(nil, nil, nil), []byte("KFI"), &kapps.StakingData{})

		require.ErrorIs(t, err, errAccessorTest)
	})

	t.Run("SetStaking reports a trie write failure", func(t *testing.T) {
		app := newStandaloneKDAKApp(t, nil, &mock.AccountsCacherStub{})

		err := app.SetStaking(kappWithTrie(nil, nil, errAccessorTest), []byte("KFI"), &kapps.StakingData{})

		require.ErrorIs(t, err, errAccessorTest)
	})
}
