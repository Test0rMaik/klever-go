package address

// MaxAssetsPerClaimList exposes the available-claim fanout cap so the boundary tests
// track the constant instead of restating it.
const MaxAssetsPerClaimList = maxAssetsPerClaimList

// MaxConcurrentClaimLookups exposes the per-request lookup concurrency bound so the
// concurrency tests assert the real limit rather than a restated copy of it.
const MaxConcurrentClaimLookups = maxConcurrentClaimLookups
