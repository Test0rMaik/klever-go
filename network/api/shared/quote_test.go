package shared_test

import (
	"strings"
	"testing"

	"github.com/klever-io/klever-go/network/api/shared"
	"github.com/stretchr/testify/assert"
)

// TestQuoteForLog covers the log-injection guard (CWE-117) applied to client-controlled values:
// a newline inside one would otherwise forge a whole log line in any file or SIEM that parses
// the node's output.
func TestQuoteForLog(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `"x\nINFO forged line"`, shared.QuoteForLog("x\nINFO forged line"))
	assert.Equal(t, `"x\r\nINFO forged line"`, shared.QuoteForLog("x\r\nINFO forged line"))
	assert.Len(t, shared.QuoteForLog(strings.Repeat("a", 4096)), 256+2,
		"a 4 KiB frame must not become a 4 KiB log line")
}
