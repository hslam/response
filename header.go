package response

import (
	"bufio"
)

type header struct {
	date             []byte
	contentLength    string
	contentType      string
	connection       string
	transferEncoding string
}

// Sorted the same as Header.Write's loop.
var headerKeys = [][]byte{
	[]byte("Content-Length"),
	[]byte("Content-Type"),
	[]byte("Connection"),
	[]byte("Transfer-Encoding"),
}
var (
	headerDate = []byte("Date: ")
)
var (
	crlf       = []byte("\r\n")
	colonSpace = []byte(": ")
)

// Write writes the headers described in h to w.
//
// This method has a value receiver, despite the somewhat large size
// of h, because it prevents an allocation. The escape analysis isn't
// smart enough to realize this function doesn't mutate h.
func (h header) Write(w *bufio.Writer) {
	if h.date != nil {
		w.Write(headerDate)
		w.Write(h.date)
		w.Write(crlf)
	}
	for i, v := range []string{h.contentLength, h.contentType, h.connection, h.transferEncoding} {
		if len(v) > 0 {
			w.Write(headerKeys[i])
			w.Write(colonSpace)
			w.WriteString(v)
			w.Write(crlf)
		}
	}
}
