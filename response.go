// Copyright (c) 2020 Meng Huang (mhboy@outlook.com)
// This package is licensed under a MIT license that can be found in the LICENSE file.

// Package response implements an HTTP response writer.
package response

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var writerPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(nil)
	},
}

var responsePool = sync.Pool{
	New: func() interface{} {
		return &Response{}
	},
}

var headerPool = sync.Pool{
	New: func() interface{} {
		return make(http.Header)
	},
}

// Response implements the http.ResponseWriter interface.
type Response struct {
	conn          net.Conn
	wroteHeader   bool
	rw            *bufio.ReadWriter
	w             *bytes.Buffer // buffers output
	handlerHeader http.Header
	written       int64 // number of bytes written in body
	contentLength int64 // explicitly-declared Content-Length; or -1
	status        int
	hijacked      bool
	flushed       bool
}

// NewResponse returns a new response.
func NewResponse(conn net.Conn, rw *bufio.ReadWriter) *Response {
	if rw == nil {
		rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	}
	w := writerPool.Get().(*bytes.Buffer)
	res := responsePool.Get().(*Response)
	res.handlerHeader = headerPool.Get().(http.Header)
	res.contentLength = -1
	res.conn = conn
	res.rw = rw
	res.w = w
	return res
}

// FreeResponse frees the response.
func FreeResponse(w http.ResponseWriter) {
	if w == nil {
		return
	}
	if res, ok := w.(*Response); ok {
		res.w.Reset()
		writerPool.Put(res.w)
		freeHeader(res.handlerHeader)
		*res = Response{}
		responsePool.Put(res)
	}
}

func freeHeader(h http.Header) {
	if h == nil {
		return
	}
	for key := range h {
		h.Del(key)
	}
	headerPool.Put(h)
}

// Hijack lets the caller take over the connection.
// After a call to Hijack the HTTP server library
// will not do anything else with the connection.
func (w *Response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.conn, w.rw, nil
}

// Header returns the header map that will be sent by
// WriteHeader.
func (w *Response) Header() http.Header {
	return w.handlerHeader
}

// Write writes the data to the connection as part of an HTTP reply.
func (w *Response) Write(data []byte) (n int, err error) {
	if w.hijacked {
		return 0, http.ErrHijacked
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	lenData := len(data)
	if lenData == 0 {
		return 0, nil
	}
	if !w.bodyAllowed() {
		return 0, http.ErrBodyNotAllowed
	}
	w.written += int64(lenData) // ignoring errors, for errorKludge
	if w.contentLength != -1 && w.written > w.contentLength {
		return 0, http.ErrContentLength
	}
	return w.w.Write(data)
}

// WriteHeader sends an HTTP response header with the provided
// status code.
func (w *Response) WriteHeader(code int) {
	if w.hijacked {
		return
	}
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	checkWriteHeaderCode(code)
	w.status = code
}

// Flush writes any buffered data to the underlying connection.
func (w *Response) Flush() {
	if w.hijacked {
		return
	}
	if w.flushed {
		return
	}
	w.flushed = true
	var setHeader header
	setHeader.date = time.Now().UTC().Format(http.TimeFormat)
	if cl := w.handlerHeader.Get("Content-Length"); cl != "" {
		v, err := strconv.ParseInt(cl, 10, 64)
		if err == nil && v >= 0 {
			w.contentLength = v
			setHeader.contentLength = cl
		}
	}

	var body = w.w.Bytes()
	if len(setHeader.contentLength) == 0 {
		w.contentLength = int64(len(body))
		setHeader.contentLength = strconv.FormatInt(w.contentLength, 10)
	}
	if ct := w.handlerHeader.Get("Content-Type"); ct != "" {
		setHeader.contentType = ct
	} else {
		setHeader.contentType = defaultContentType
	}
	w.rw.WriteString(fmt.Sprintf("HTTP/1.1 %03d %s\r\n", w.status, http.StatusText(w.status)))
	setHeader.Write(w.rw)
	for key := range w.handlerHeader {
		value := w.handlerHeader.Get(key)
		if key == "Date" || key == "Content-Length" || key == "Content-Type" {
			continue
		}
		if len(key) > 0 && len(value) > 0 {
			w.rw.WriteString(key)
			w.rw.Write(colonSpace)
			w.rw.WriteString(value)
			w.rw.Write(crlf)
		}
	}
	w.rw.Write(crlf)
	w.rw.Write(body)
	w.rw.Flush()
}

var defaultContentType = "text/plain; charset=utf-8"

// bodyAllowed reports whether a Write is allowed for this response type.
// It's illegal to call this before the header has been flushed.
func (w *Response) bodyAllowed() bool {
	if !w.wroteHeader {
		panic("")
	}
	return bodyAllowedForStatus(w.status)
}

// bodyAllowedForStatus reports whether a given response status code
// permits a body. See RFC 7230, section 3.3.
func bodyAllowedForStatus(status int) bool {
	switch {
	case status >= 100 && status <= 199:
		return false
	case status == 204:
		return false
	case status == 304:
		return false
	}
	return true
}

func checkWriteHeaderCode(code int) {
	// Issue 22880: require valid WriteHeader status codes.
	// For now we only enforce that it's three digits.
	// In the future we might block things over 599 (600 and above aren't defined
	// at https://httpwg.org/specs/rfc7231.html#status.codes)
	// and we might block under 200 (once we have more mature 1xx support).
	// But for now any three digits.
	//
	// We used to send "HTTP/1.1 000 0" on the wire in responses but there's
	// no equivalent bogus thing we can realistically send in HTTP/2,
	// so we'll consistently panic instead and help people find their bugs
	// early. (We can't return an error from WriteHeader even if we wanted to.)
	if code < 100 || code > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", code))
	}
}

type header struct {
	date          string // written if not nil
	contentLength string // written if not nil
	contentType   string // written if not nil
}

// Sorted the same as Header.Write's loop.
var headerKeys = [][]byte{
	[]byte("Date"),
	[]byte("Content-Length"),
	[]byte("Content-Type"),
}

var (
	crlf       = []byte("\r\n")
	colonSpace = []byte(": ")
)

// Write writes the headers described in h to w.
//
// This method has a value receiver, despite the somewhat large size
// of h, because it prevents an allocation. The escape analysis isn't
// smart enough to realize this function doesn't mutate h.
func (h header) Write(rw *bufio.ReadWriter) {
	for i, v := range []string{h.date, h.contentLength, h.contentType} {
		if len(v) > 0 {
			rw.Write(headerKeys[i])
			rw.Write(colonSpace)
			rw.WriteString(v)
			rw.Write(crlf)
		}
	}
}
