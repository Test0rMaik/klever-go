package mock

import (
	"sync"

	logger "github.com/klever-io/klever-go-logger"
)

// LogLineCounter is a log observer and formatter in one: it counts the lines whose message
// equals the one it was built with and formats nothing else, so a test can assert on what the
// logger actually emitted rather than on state the code under test happens to keep. Register
// it with logger.AddLogObserver(counter, counter).
type LogLineCounter struct {
	message string
	mut     sync.Mutex
	n       int
}

// NewLogLineCounter -
func NewLogLineCounter(message string) *LogLineCounter {
	return &LogLineCounter{message: message}
}

// Write counts one line per non-empty formatted output.
func (c *LogLineCounter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		c.mut.Lock()
		c.n++
		c.mut.Unlock()
	}

	return len(p), nil
}

// Count returns how many matching lines were emitted so far.
func (c *LogLineCounter) Count() int {
	c.mut.Lock()
	defer c.mut.Unlock()

	return c.n
}

// Output is the Formatter: a byte for a matching line, nothing for anything else.
func (c *LogLineCounter) Output(line logger.LogLineHandler) []byte {
	if line.GetMessage() != c.message {
		return nil
	}

	return []byte{1}
}

// IsInterfaceNil -
func (c *LogLineCounter) IsInterfaceNil() bool {
	return c == nil
}
