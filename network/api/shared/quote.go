package shared

import "fmt"

// QuoteForLog makes a client-controlled string safe to put in a log line: %q escapes the newlines
// that would otherwise forge whole entries in a log file or SIEM, and the precision caps the
// length so a 4 KiB handshake frame cannot become a 4 KiB log line.
//
// Apply it to any value a remote peer can steer, including the text of an error built from one:
// a gorilla *CloseError carries the peer's close reason verbatim, and a logger profile error
// echoes the raw level segment it could not parse.
func QuoteForLog(s string) string {
	return fmt.Sprintf("%.256q", s)
}
