package address

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/data/state"

	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/network/api/errors"
	"github.com/klever-io/klever-go/network/api/models"
	"github.com/klever-io/klever-go/network/api/shared"
	"github.com/klever-io/klever-go/network/api/wrapper"
)

var log = logger.GetOrCreate("api/address")

const (
	getAccountPath            = "/:address"
	getBalancePath            = "/:address/balance"
	getKDAPath                = "/:address/kda"
	getAccountNoncePath       = "/:address/nonce"
	getAvailableClaimPath     = "/:address/allowance"
	getAvailableClaimListPath = "/:address/allowance/list"

	defaultAsset = "KLV"

	// maxAssetsPerClaimList bounds the per-request fanout. Each asset costs three exclusive
	// AccountsDB.LoadAccount acquisitions (the user account plus the Staking and KDA kapp
	// accounts), on mutexes shared with block processing, so the cap is set from that cost
	// rather than from what a client might conceivably ask for. Raise it once the invariant
	// loads are hoisted out of the fanout (KLC-2652), which drops the per-request cost from
	// 3n acquisitions to 3.
	maxAssetsPerClaimList = 25
	// maxConcurrentClaimLookups bounds in-flight lookups per request. Useful parallelism here
	// is about two: LoadAccount holds an exclusive mutex for its whole call, and each asset
	// takes one acquisition on the user adapter and two on the kapps adapter, so extra
	// goroutines queue rather than overlap. Only the trie reads outside LoadAccount overlap,
	// which is what the remaining headroom is for. Node-wide this multiplies by
	// webServer.simultaneousRequests, so the constant is deliberately small.
	maxConcurrentClaimLookups = 4
)

// FacadeHandler interface defines methods that can be used by the gin webserver
type FacadeHandler interface {
	GetAccount(address string) (state.UserAccountHandler, error)
	GetNextNonce(address string) (*models.AccountNonceResponse, error)
	GetBalance(address string, kda string) (*models.BalanceResponse, error)
	GetUserKDA(address string, kda string) (*kapps.UserKDA, error)
	GetAvailableClaim(address string, assetId string) (*models.AvailableClaimResponse, error)
	IsInterfaceNil() bool
}

// Routes defines address related routes
func Routes(router *wrapper.RouterWrapper) {
	router.RegisterHandler(http.MethodGet, getAccountPath, GetAccount)
	router.RegisterHandler(http.MethodGet, getBalancePath, GetBalance)
	router.RegisterHandler(http.MethodGet, getKDAPath, GetKDA)
	router.RegisterHandler(http.MethodGet, getAccountNoncePath, GetAccountNonce)
	router.RegisterHandler(http.MethodGet, getAvailableClaimPath, GetAvailableClaim)
	router.RegisterHandler(http.MethodGet, getAvailableClaimListPath, GetAvailableClaimList)
}

func getFacade(c *gin.Context) (FacadeHandler, bool) {
	facadeObj, ok := c.Get("facade")
	if !ok {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrNilAppContext),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return nil, false
	}

	facade, ok := facadeObj.(FacadeHandler)
	if !ok {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrInvalidAppContext),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return nil, false
	}

	return facade, true
}

// @Summary returns an accountResponse
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Success 200 object shared.GenericAPIResponse{data=object{account=data.AccountInfo}} "ok"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address} [get]
// GetAccount returns an accountResponse containing information
// about the account correlated with provided address
func GetAccount(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	acc, err := facade.GetAccount(addr)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrCouldNotGetAccount, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  gin.H{"account": acc},
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the nonce and pending transaction info for a specific account
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Success 200 object shared.GenericAPIResponse{data=models.AccountNonceResponse} "ok"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/nonce [get]
// GetAccountNonce returns an account nonce info
func GetAccountNonce(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	nonceResponse, err := facade.GetNextNonce(addr)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrCouldNotGetAccount, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  nonceResponse,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the balance for the address parameter
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string false "asset" default(KLV)
// @Success 200 object shared.GenericAPIResponse{data=models.BalanceResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/balance [get]
// GetBalance returns the balance for the address parameter
func GetBalance(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	asset := c.Query("asset")
	if asset == "" {
		asset = defaultAsset
	}

	addr := c.Param("address")
	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  &models.BalanceResponse{Balance: 0},
				Error: errors.APIErrorString(errors.ErrGetBalance, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	balanceResponse, err := facade.GetBalance(addr, asset)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  &models.BalanceResponse{Balance: 0},
				Error: errors.APIErrorString(errors.ErrGetBalance, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}
	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  balanceResponse,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the user kda for the address parameter
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string false "asset" default(KLV)
// @Success 200 object shared.GenericAPIResponse{data=models.KDAResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/kda [get]
// GetKDA returns the balance for the address parameter
func GetKDA(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	asset := c.Query("asset")
	if asset == "" {
		asset = defaultAsset
	}

	addr := c.Param("address")
	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  &models.KDAResponse{UserKDA: nil, Address: addr, Asset: asset},
				Error: errors.APIErrorString(errors.ErrGetUserKDA, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	userKDA, err := facade.GetUserKDA(addr, asset)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  &models.KDAResponse{UserKDA: nil, Address: addr, Asset: asset},
				Error: errors.APIErrorString(errors.ErrGetUserKDA, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	response := &models.KDAResponse{
		UserKDA: userKDA,
		Address: addr,
		Asset:   asset,
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  response,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// @Summary returns the rewards available for a specific asset in an account
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string true "asset"
// @Success 200 object shared.GenericAPIResponse{data=models.AvailableClaimResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/allowance [get]
// GetAvailableClaim returns the rewards available for a specific asset in an account
func GetAvailableClaim(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	assetId := c.Query("asset")

	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaim, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}
	if assetId == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaim, errors.ErrEmptyAssetId),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	claimResponse, err := facade.GetAvailableClaim(addr, assetId)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaim, err),
				Code:  shared.ReturnCodeInternalError,
			},
		)
		return
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  claimResponse,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}

// parseRequestedAssets splits a comma-separated asset query into distinct, non-empty asset IDs.
//
// It deliberately returns up to maxAssetsPerClaimList+1 entries rather than truncating at the cap,
// so the caller can distinguish an at-cap request from an over-cap one and reject the latter with
// HTTP 400. Breaking one element earlier would silently serve partial results with HTTP 200.
func parseRequestedAssets(assets string) []string {
	raw := strings.Split(assets, ",")
	assetList := make([]string, 0, min(len(raw), maxAssetsPerClaimList+1))
	requested := make(map[string]struct{}, cap(assetList))

	for _, s := range raw {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		if _, alreadyRequested := requested[t]; alreadyRequested {
			continue
		}
		requested[t] = struct{}{}
		assetList = append(assetList, t)
		if len(assetList) > maxAssetsPerClaimList {
			break
		}
	}

	return assetList
}

// @Summary returns the rewards available for a specific list of asset in an account
// @Tags Address
// @Produce json
// @Param address path string true "address"
// @Param asset query string true "assets (comma-separated asset IDs, max 25), e.g. KLV,KFI"
// @Success 200 object shared.GenericAPIResponse{data=models.AvailableClaimListResponse} "ok"
// @Failure 400 object shared.GenericAPIResponse "some error"
// @Failure 500 object shared.GenericAPIResponse "internal error"
// @Router /address/{address}/allowance/list [get]
// GetAvailableClaimList returns the rewards available for a specific list of asset in an account
func GetAvailableClaimList(c *gin.Context) {
	facade, ok := getFacade(c)
	if !ok {
		return
	}

	addr := c.Param("address")
	assets := c.Query("asset") // need to be separated by comma

	// split assets
	assetList := parseRequestedAssets(assets)

	if addr == "" {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaimList, errors.ErrEmptyAddress),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	if len(assetList) == 0 {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaimList, errors.ErrEmptyAssetId),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	if len(assetList) > maxAssetsPerClaimList {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data: nil,
				Error: errors.APIErrorString(
					errors.ErrGetAvailableClaimList,
					fmt.Errorf("%w: maximum is %d", errors.ErrTooManyAssets, maxAssetsPerClaimList),
				),
				Code: shared.ReturnCodeRequestError,
			},
		)
		return
	}

	var wg sync.WaitGroup
	var lock sync.Mutex
	allowanceError := make(map[string]error)
	assetData := make(map[string]*models.AvailableClaimResponse, len(assetList))

	sem := make(chan struct{}, maxConcurrentClaimLookups)

	for _, asset := range assetList {
		wg.Add(1)
		sem <- struct{}{}
		go func(assetId string) {
			defer wg.Done()
			defer func() { <-sem }()
			// gin.Recovery only wraps the handler goroutine, so an unrecovered panic here
			// would terminate the node process rather than fail the request.
			defer func() {
				r := recover()
				if r == nil {
					return
				}

				log.Error("recovered from panic while computing available claim",
					"address", addr,
					"asset", assetId,
					"panic", r,
					"stack", string(debug.Stack()),
				)

				lock.Lock()
				defer lock.Unlock()
				allowanceError[assetId] = errors.ErrClaimLookupFailed
			}()

			claimResponse, err := facade.GetAvailableClaim(addr, assetId)

			lock.Lock()
			defer lock.Unlock()
			if err != nil {
				allowanceError[assetId] = err
				return
			}
			assetData[assetId] = claimResponse
		}(asset)
	}

	wg.Wait()

	if len(allowanceError) > 0 {
		c.JSON(
			http.StatusBadRequest,
			shared.GenericAPIResponse{
				Data:  nil,
				Error: errors.APIErrorString(errors.ErrGetAvailableClaimList, allowanceError),
				Code:  shared.ReturnCodeRequestError,
			},
		)
		return
	}

	response := &models.AvailableClaimListResponse{
		Assets: assetData,
	}

	c.JSON(
		http.StatusOK,
		shared.GenericAPIResponse{
			Data:  response,
			Error: "",
			Code:  shared.ReturnCodeSuccess,
		},
	)
}
