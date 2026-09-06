package address_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	mmock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/network/api/address"
	apiErrors "github.com/klever-io/klever-go/network/api/errors"
	"github.com/klever-io/klever-go/network/api/middleware"
	"github.com/klever-io/klever-go/network/api/mock"
	"github.com/klever-io/klever-go/network/api/models"
	"github.com/klever-io/klever-go/network/api/shared"
	"github.com/klever-io/klever-go/network/api/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validAddress = "klv17e8zzgn73h6ehe3c6q9vlt77kuxk5euddmhymy5uhv2rhv0dc0nqlfp0ap"

func init() {
	gin.SetMode(gin.TestMode)
}

func TestAddressRoute_EmptyTrailReturns404(t *testing.T) {
	t.Parallel()
	facade := mock.Facade{}
	ws := startNodeServer(&facade)

	req, _ := http.NewRequest("GET", "/address", nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestGetKDA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetQuery   string
		mockHandler  func(address, asset string) (*kapps.UserKDA, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid address and asset",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*kapps.UserKDA, error) {
				return &kapps.UserKDA{Balance: 100}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"userKDA": map[string]interface{}{"Balance": 100.0}, "address": validAddress, "asset": "KLV"},
		},
		{
			name:         "Success with default asset",
			addressParam: validAddress,
			assetQuery:   "",
			mockHandler: func(address, asset string) (*kapps.UserKDA, error) {
				return &kapps.UserKDA{Balance: 100}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"userKDA": map[string]interface{}{"Balance": 100.0}, "address": validAddress, "asset": "KLV"},
		},
		{
			name:         "Missing address parameter",
			addressParam: "",
			assetQuery:   "KLV",
			mockHandler:  nil, // Not needed since the handler won't be called
			expectedCode: http.StatusBadRequest,
			expectedErr:  "get userKDA error: address is empty",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*kapps.UserKDA, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set up mock facade
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.GetUserKDAHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)

			// Construct URL
			url := fmt.Sprintf("/address/%s/kda", tc.addressParam)
			if tc.assetQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response map[string]interface{}
			if tc.expectedErr != "" {
				// Verify error response
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Contains(t, response["error"], tc.expectedErr)
			} else {
				// Verify success response
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				for key, value := range tc.expectedData {
					assert.Equal(t, value, response["data"].(map[string]interface{})[key])
				}
			}
		})
	}
}

func TestGetAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		mockHandler  func(address string) (state.UserAccountHandler, error)
		expectedCode int
		expectedErr  string
	}{
		{
			name:         "Success with valid address",
			addressParam: validAddress,
			mockHandler: func(address string) (state.UserAccountHandler, error) {
				return &mmock.AccountWrapMock{}, nil
			},
			expectedCode: http.StatusOK,
		},
		{
			name:         "Error from facade",
			addressParam: "invalidAddress",
			mockHandler: func(address string) (state.UserAccountHandler, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			facade.GetAccountHandler = tc.mockHandler

			ws := startNodeServer(&facade)
			req, _ := http.NewRequest("GET", fmt.Sprintf("/address/%s", tc.addressParam), nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Empty(t, response.Error)
				assert.NotNil(t, response.Data)
			}
		})
	}
}

func TestGetBalance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetQuery   string
		mockHandler  func(address, asset string) (*models.BalanceResponse, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid address and asset",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*models.BalanceResponse, error) {
				return &models.BalanceResponse{Balance: 1000}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"balance": float64(1000)},
		},
		{
			name:         "Success with valid address and assetDefault",
			addressParam: validAddress,
			assetQuery:   "",
			mockHandler: func(address, asset string) (*models.BalanceResponse, error) {
				if asset == "KLV" {
					return &models.BalanceResponse{Balance: 1000}, nil
				}

				return &models.BalanceResponse{Balance: 0}, fmt.Errorf("asset not found")
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{"balance": float64(1000)},
		},
		{
			name:         "Empty address",
			addressParam: "",
			assetQuery:   "KLV",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "address is empty",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, asset string) (*models.BalanceResponse, error) {
				return &models.BalanceResponse{Balance: 0}, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.BalanceHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf("/address/%s/balance", tc.addressParam)
			if tc.assetQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAccountNonce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		mockHandler  func(address string) (*models.AccountNonceResponse, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid address",
			addressParam: validAddress,
			mockHandler: func(address string) (*models.AccountNonceResponse, error) {
				return &models.AccountNonceResponse{
					Nonce:             1,
					FirstPendingNonce: 2,
					TxPending:         3,
				}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{
				"nonce":             float64(1),
				"firstPendingNonce": float64(2),
				"txPending":         float64(3),
			},
		},
		{
			name:         "Error from facade",
			addressParam: "invalidAddress",
			mockHandler: func(address string) (*models.AccountNonceResponse, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			facade.GetNextNonceHandler = tc.mockHandler

			ws := startNodeServer(&facade)
			req, _ := http.NewRequest("GET", fmt.Sprintf("/address/%s/nonce", tc.addressParam), nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAvailableClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetQuery   string
		mockHandler  func(address, assetId string) (*models.AvailableClaimResponse, error)
		expectedCode int
		expectedData map[string]interface{}
		expectedErr  string
	}{
		{
			name:         "Success with valid parameters",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return &models.AvailableClaimResponse{
					StakingRewards:    100,
					AllStakingRewards: map[string]int64{"KLV": 200},
					Allowance:         300,
				}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{
				"stakingRewards":    float64(100),
				"allStakingRewards": map[string]any{"KLV": float64(200)},
				"allowance":         float64(300),
			},
		},
		{
			name:         "Empty address",
			addressParam: "",
			assetQuery:   "KLV",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "address is empty",
		},
		{
			name:         "Empty asset",
			addressParam: validAddress,
			assetQuery:   "",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "could not get rewards for requested account asset: assetId is empty",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetQuery:   "KLV",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return nil, fmt.Errorf("facade error")
			},
			expectedCode: http.StatusInternalServerError,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.RewardsAvailableToClaimHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf("/address/%s/allowance", tc.addressParam)
			if tc.assetQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAvailableClaimList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		addressParam string
		assetsQuery  string
		mockHandler  func(address, assetId string) (*models.AvailableClaimResponse, error)
		expectedCode int
		expectedData map[string]any
		expectedErr  string
	}{
		{
			name:         "Success with valid parameters",
			addressParam: validAddress,
			assetsQuery:  "KLV,BTC",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return &models.AvailableClaimResponse{
					StakingRewards:    100,
					AllStakingRewards: map[string]int64{assetId: 200},
					Allowance:         300,
				}, nil
			},
			expectedCode: http.StatusOK,
			expectedData: map[string]interface{}{
				"assets": map[string]interface{}{
					"KLV": map[string]interface{}{
						"stakingRewards":    float64(100),
						"allStakingRewards": map[string]interface{}{"KLV": float64(200)},
						"allowance":         float64(300),
					},
					"BTC": map[string]interface{}{
						"stakingRewards":    float64(100),
						"allStakingRewards": map[string]interface{}{"BTC": float64(200)},
						"allowance":         float64(300),
					},
				},
			},
		},
		{
			name:         "Empty address",
			addressParam: "",
			assetsQuery:  "KLV,BTC",
			mockHandler:  nil,
			expectedCode: http.StatusBadRequest,
			expectedErr:  "address is empty",
		},
		{
			name:         "Empty assets",
			addressParam: validAddress,
			assetsQuery:  "",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return nil, fmt.Errorf("invalid assetID")
			},
			expectedCode: http.StatusBadRequest,
			expectedErr:  "could not get rewards for requested account asset list: assetId is empty",
		},
		{
			name:         "Invalid assets",
			addressParam: validAddress,
			assetsQuery:  "KLV,INVALID",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				if assetId == "INVALID" {
					return nil, fmt.Errorf("invalid assetID")
				}

				return &models.AvailableClaimResponse{
					StakingRewards:    100,
					AllStakingRewards: map[string]int64{assetId: 200},
					Allowance:         300,
				}, nil
			},
			expectedCode: http.StatusBadRequest,
			expectedErr:  "could not get rewards for requested account asset list: map[INVALID:invalid assetID]",
		},
		{
			name:         "Error from facade",
			addressParam: validAddress,
			assetsQuery:  "KLV,BTC",
			mockHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
				return nil, fmt.Errorf("facade error for %s", assetId)
			},
			expectedCode: http.StatusBadRequest,
			expectedErr:  "facade error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facade := mock.Facade{}
			if tc.mockHandler != nil {
				facade.RewardsAvailableToClaimHandler = tc.mockHandler
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf("/address/%s/allowance/list", tc.addressParam)
			if tc.assetsQuery != "" {
				url = fmt.Sprintf("%s?asset=%s", url, tc.assetsQuery)
			}

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Contains(t, response.Error, tc.expectedErr)
			} else {
				assert.Equal(t, tc.expectedData, response.Data)
			}
		})
	}
}

func TestGetAvailableClaimListDeduplicatesRequestedAssets(t *testing.T) {
	t.Parallel()

	var lock sync.Mutex
	callsPerAsset := make(map[string]int)

	facade := mock.Facade{
		RewardsAvailableToClaimHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
			lock.Lock()
			callsPerAsset[assetId]++
			lock.Unlock()

			return &models.AvailableClaimResponse{
				StakingRewards:    100,
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		},
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf("/address/%s/allowance/list?asset=%s", validAddress, "KLV,BTC,KLV,%20KLV%20,BTC,,KFI,KLV")

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response.Error)

	lock.Lock()
	defer lock.Unlock()
	assert.Equal(t, map[string]int{"KLV": 1, "BTC": 1, "KFI": 1}, callsPerAsset)

	assets := extractClaimListAssets(t, response)
	assert.Len(t, assets, 3)
	assert.Contains(t, assets, "KLV")
	assert.Contains(t, assets, "BTC")
	assert.Contains(t, assets, "KFI")
}

func TestGetAvailableClaimListRejectsExcessiveAssetCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		assetCount    int
		expectedCode  int
		expectedErr   string
		expectedCalls int64
	}{
		{
			name:          "at the maximum allowed asset count",
			assetCount:    address.MaxAssetsPerClaimList,
			expectedCode:  http.StatusOK,
			expectedCalls: int64(address.MaxAssetsPerClaimList),
		},
		{
			name:         "one asset above the maximum allowed asset count",
			assetCount:   address.MaxAssetsPerClaimList + 1,
			expectedCode: http.StatusBadRequest,
			expectedErr: fmt.Sprintf(
				"could not get rewards for requested account asset list: too many assets requested: maximum is %d",
				address.MaxAssetsPerClaimList,
			),
			expectedCalls: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64

			facade := mock.Facade{
				RewardsAvailableToClaimHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
					calls.Add(1)

					return &models.AvailableClaimResponse{
						StakingRewards:    100,
						AllStakingRewards: map[string]int64{assetId: 200},
						Allowance:         300,
					}, nil
				},
			}

			ws := startNodeServer(&facade)
			url := fmt.Sprintf(
				"/address/%s/allowance/list?asset=%s",
				validAddress,
				strings.Join(buildRequestedAssets(tc.assetCount), ","),
			)

			req, _ := http.NewRequest("GET", url, nil)
			resp := httptest.NewRecorder()
			ws.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)
			assert.Equal(t, tc.expectedCalls, calls.Load())

			var response shared.GenericAPIResponse
			err := json.Unmarshal(resp.Body.Bytes(), &response)
			assert.NoError(t, err)

			if tc.expectedErr != "" {
				assert.Equal(t, tc.expectedErr, response.Error)
				assert.Equal(t, shared.ReturnCodeRequestError, response.Code)
				assert.Nil(t, response.Data)
				return
			}

			assert.Empty(t, response.Error)
			assert.Len(t, extractClaimListAssets(t, response), tc.assetCount)
		})
	}
}

func TestGetAvailableClaimListReturnsAllRequestedAssets(t *testing.T) {
	t.Parallel()

	requestedAssets := buildRequestedAssets(25)
	rewardsPerAsset := make(map[string]int64, len(requestedAssets))
	for i, asset := range requestedAssets {
		rewardsPerAsset[asset] = int64(100 + i)
	}

	facade := mock.Facade{
		RewardsAvailableToClaimHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
			return &models.AvailableClaimResponse{
				StakingRewards:    rewardsPerAsset[assetId],
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		},
	}

	ws := startNodeServer(&facade)
	url := fmt.Sprintf(
		"/address/%s/allowance/list?asset=%s",
		validAddress,
		strings.Join(requestedAssets, ","),
	)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response.Error)
	assert.Equal(t, shared.ReturnCodeSuccess, response.Code)

	assets := extractClaimListAssets(t, response)
	assert.Len(t, assets, len(requestedAssets))

	for _, asset := range requestedAssets {
		entry, ok := assets[asset].(map[string]interface{})
		if !assert.True(t, ok, asset) {
			continue
		}

		assert.Equal(t, float64(rewardsPerAsset[asset]), entry["stakingRewards"])
		assert.Equal(t, map[string]interface{}{asset: float64(200)}, entry["allStakingRewards"])
		assert.Equal(t, float64(300), entry["allowance"])
	}
}

func TestGetAvailableClaimListBoundsConcurrentLookups(t *testing.T) {
	t.Parallel()

	const expectedMaxConcurrentLookups = int64(address.MaxConcurrentClaimLookups)

	var inFlight atomic.Int64
	var peakInFlight atomic.Int64

	facade := mock.Facade{
		RewardsAvailableToClaimHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
			current := inFlight.Add(1)
			for {
				peak := peakInFlight.Load()
				if current <= peak || peakInFlight.CompareAndSwap(peak, current) {
					break
				}
			}

			time.Sleep(2 * time.Millisecond)
			inFlight.Add(-1)

			return &models.AvailableClaimResponse{
				StakingRewards:    100,
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		},
	}

	requestedAssets := buildRequestedAssets(address.MaxAssetsPerClaimList)

	ws := startNodeServer(&facade)
	url := fmt.Sprintf(
		"/address/%s/allowance/list?asset=%s",
		validAddress,
		strings.Join(requestedAssets, ","),
	)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response.Error)
	assert.Len(t, extractClaimListAssets(t, response), len(requestedAssets))

	assert.Zero(t, inFlight.Load())
	assert.LessOrEqual(t, peakInFlight.Load(), expectedMaxConcurrentLookups)
	assert.Greater(t, peakInFlight.Load(), int64(1))
}

func TestGetAvailableClaimListReleasesSlotsOnLookupError(t *testing.T) {
	t.Parallel()

	const expectedMaxConcurrentLookups = int64(address.MaxConcurrentClaimLookups)

	var calls atomic.Int64
	var inFlight atomic.Int64
	var peakInFlight atomic.Int64

	facade := mock.Facade{
		RewardsAvailableToClaimHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
			current := inFlight.Add(1)
			for {
				peak := peakInFlight.Load()
				if current <= peak || peakInFlight.CompareAndSwap(peak, current) {
					break
				}
			}

			time.Sleep(time.Millisecond)
			inFlight.Add(-1)

			// Fail most lookups: if a failing lookup did not release its semaphore slot,
			// the dispatch loop would block once the bound is exhausted and never return.
			if calls.Add(1)%3 != 0 {
				return nil, fmt.Errorf("lookup failed for %s", assetId)
			}

			return &models.AvailableClaimResponse{
				StakingRewards:    100,
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		},
	}

	requestedAssets := buildRequestedAssets(address.MaxAssetsPerClaimList)

	ws := startNodeServer(&facade)
	url := fmt.Sprintf(
		"/address/%s/allowance/list?asset=%s",
		validAddress,
		strings.Join(requestedAssets, ","),
	)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusBadRequest, resp.Code)

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotEmpty(t, response.Error)

	// Every asset was dispatched, so no slot was leaked by a failing lookup.
	assert.Equal(t, int64(len(requestedAssets)), calls.Load())
	assert.Zero(t, inFlight.Load())
	assert.LessOrEqual(t, peakInFlight.Load(), expectedMaxConcurrentLookups)
}

func TestGetAvailableClaimListRecoversFromLookupPanic(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	facade := mock.Facade{
		RewardsAvailableToClaimHandler: func(address, assetId string) (*models.AvailableClaimResponse, error) {
			// Panic on a subset; gin.Recovery cannot reach these goroutines, so an
			// unrecovered panic here would take the whole process down.
			if calls.Add(1)%2 == 0 {
				panic("simulated lookup panic for " + assetId)
			}

			return &models.AvailableClaimResponse{
				StakingRewards:    100,
				AllStakingRewards: map[string]int64{assetId: 200},
				Allowance:         300,
			}, nil
		},
	}

	requestedAssets := buildRequestedAssets(20)

	ws := startNodeServer(&facade)
	url := fmt.Sprintf(
		"/address/%s/allowance/list?asset=%s",
		validAddress,
		strings.Join(requestedAssets, ","),
	)

	req, _ := http.NewRequest("GET", url, nil)
	resp := httptest.NewRecorder()

	require.NotPanics(t, func() { ws.ServeHTTP(resp, req) })

	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Equal(t, int64(len(requestedAssets)), calls.Load())

	var response shared.GenericAPIResponse
	err := json.Unmarshal(resp.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Contains(t, fmt.Sprint(response.Error), apiErrors.ErrClaimLookupFailed.Error())
}

func buildRequestedAssets(count int) []string {
	assets := make([]string, 0, count)
	for i := 0; i < count; i++ {
		assets = append(assets, fmt.Sprintf("KDA%d-A1B2", i))
	}

	return assets
}

func extractClaimListAssets(t *testing.T, response shared.GenericAPIResponse) map[string]interface{} {
	t.Helper()

	data, ok := response.Data.(map[string]interface{})
	if !assert.True(t, ok) {
		return nil
	}

	assets, ok := data["assets"].(map[string]interface{})
	if !assert.True(t, ok) {
		return nil
	}

	return assets
}

func TestInvalidFacade(t *testing.T) {
	t.Parallel()

	endpoints := []struct {
		name string
		path string
	}{
		{"GetAccount", "/address/test"},
		{"GetBalance", "/address/test/balance"},
		{"GetKDA", "/address/test/kda"},
		{"GetAccountNonce", "/address/test/nonce"},
		{"GetAvailableClaim", "/address/test/allowance?asset=KLV"},
		{"GetAvailableClaimList", "/address/test/allowance/list?asset=KLV"},
	}

	t.Run("nil facade", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint.name, func(t *testing.T) {
				ws := startNodeServer(nil)
				req, _ := http.NewRequest("GET", endpoint.path, nil)
				resp := httptest.NewRecorder()
				ws.ServeHTTP(resp, req)

				assert.Equal(t, http.StatusInternalServerError, resp.Code)

				var response shared.GenericAPIResponse
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Contains(t, response.Error, "nil app context")
			})
		}
	})

	t.Run("invalid facade type", func(t *testing.T) {
		for _, endpoint := range endpoints {
			t.Run(endpoint.name, func(t *testing.T) {
				ws := gin.New()
				invalidFacade := "invalid facade"
				addressGroup := ws.Group("/address")
				addressGroup.Use(func(c *gin.Context) {
					c.Set("facade", invalidFacade) // Set invalid facade type
					c.Next()
				})
				addressRoute, _ := wrapper.NewRouterWrapper("address", addressGroup, getRoutesConfig())
				address.Routes(addressRoute)

				req, _ := http.NewRequest("GET", endpoint.path, nil)
				resp := httptest.NewRecorder()
				ws.ServeHTTP(resp, req)

				assert.Equal(t, http.StatusInternalServerError, resp.Code)

				var response shared.GenericAPIResponse
				err := json.Unmarshal(resp.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Contains(t, response.Error, "invalid app context")
			})
		}
	})
}

func startNodeServer(handler address.FacadeHandler) *gin.Engine {
	ws := gin.New()
	ws.Use(cors.Default())
	addressRoutes := ws.Group("/address")
	if handler != nil {
		addressRoutes.Use(middleware.WithFacade(handler))
	}
	addressRoute, _ := wrapper.NewRouterWrapper("address", addressRoutes, getRoutesConfig())
	address.Routes(addressRoute)
	return ws
}

func getRoutesConfig() config.APIRoutesConfig {
	return config.APIRoutesConfig{
		APIPackages: map[string]config.APIPackageConfig{
			"address": {
				Routes: []config.RouteConfig{
					{Name: "/:address/kda", Open: true},
					{Name: "/:address/balance", Open: true},
					{Name: "/:address/nonce", Open: true},
					{Name: "/:address/allowance", Open: true},
					{Name: "/:address/allowance/list", Open: true},
					{Name: "/:address", Open: true},
				},
			},
		},
	}
}
