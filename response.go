// Copyright (c) 2020 Meng Huang (mhboy@outlook.com)
// This package is licensed under a MIT license that can be found in the LICENSE file.

// Package response implements an HTTP response writer.
package response

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	statusLine         = "HTTP/1.1 %03d %s\r\n"
	chunk              = "%x\r\n"
	contentLength      = "Content-Length"
	transferEncoding   = "Transfer-Encoding"
	contentType        = "Content-Type"
	date               = "Date"
	connection         = "Connection"
	chunked            = "chunked"
	defaultContentType = "text/plain; charset=utf-8"
	head               = "HEAD"
	emptyString        = ""
)

var (
	buffers = sync.Map{}
	assign  int32
)

func assignPool(size int) *sync.Pool {
	for {
		if p, ok := buffers.Load(size); ok {
			return p.(*sync.Pool)
		}
		if atomic.CompareAndSwapInt32(&assign, 0, 1) {
			var pool = &sync.Pool{New: func() interface{} {
				return make([]byte, size)
			}}
			buffers.Store(size, pool)
			atomic.StoreInt32(&assign, 0)
			return pool
		}
	}
}

var (
	bufioReaderPool   sync.Pool
	bufioWriter2kPool sync.Pool
	bufioWriter4kPool sync.Pool
)

// NewBufioReader returns a new bufio.Reader with r.
func NewBufioReader(r io.Reader) *bufio.Reader {
	if v := bufioReaderPool.Get(); v != nil {
		br := v.(*bufio.Reader)
		br.Reset(r)
		return br
	}
	// Note: if this reader size is ever changed, update
	// TestHandlerBodyClose's assumptions.
	return bufio.NewReader(r)
}

// FreeBufioReader frees the bufio.Reader.
func FreeBufioReader(br *bufio.Reader) {
	br.Reset(nil)
	bufioReaderPool.Put(br)
}

func bufioWriterPool(size int) *sync.Pool {
	switch size {
	case 2 << 10:
		return &bufioWriter2kPool
	case 4 << 10:
		return &bufioWriter4kPool
	}
	return nil
}

// NewBufioWriterSize returns a new bufio.Writer with w and size.
func NewBufioWriterSize(w io.Writer, size int) *bufio.Writer {
	pool := bufioWriterPool(size)
	if pool != nil {
		if v := pool.Get(); v != nil {
			bw := v.(*bufio.Writer)
			bw.Reset(w)
			return bw
		}
	}
	return bufio.NewWriterSize(w, size)
}

// FreeBufioWriter frees the bufio.Writer.
func FreeBufioWriter(bw *bufio.Writer) {
	bw.Reset(nil)
	if pool := bufioWriterPool(bw.Available()); pool != nil {
		pool.Put(bw)
	}
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

// FreeResponse frees the response.
func FreeResponse(w http.ResponseWriter) {
	if w == nil {
		return
	}
	if res, ok := w.(*Response); ok {
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

// Response implements the http.ResponseWriter interface.
type Response struct {
	req           *http.Request
	conn          net.Conn
	wroteHeader   bool
	rw            *bufio.ReadWriter
	buffer        []byte
	w             *bufio.Writer // buffers output
	cw            chunkWriter
	handlerHeader http.Header
	setHeader     header
	written       int64 // number of bytes written in body
	noCache       bool
	contentLength int64 // explicitly-declared Content-Length; or -1
	status        int
	hijacked      bool
	dateBuf       [len(TimeFormat)]byte
	bufferPool    *sync.Pool
	handlerDone   atomicBool // set true when the handler exits
}

type atomicBool int32

func (b *atomicBool) isSet() bool { return atomic.LoadInt32((*int32)(b)) != 0 }
func (b *atomicBool) setTrue()    { atomic.StoreInt32((*int32)(b), 1) }

// NewResponse returns a new response.
func NewResponse(req *http.Request, conn net.Conn, rw *bufio.ReadWriter) *Response {
	return NewResponseSize(req, conn, rw, bufferBeforeChunkingSize)
}

// NewResponseSize returns a new response whose buffer has at least the specified
// size.
func NewResponseSize(req *http.Request, conn net.Conn, rw *bufio.ReadWriter, size int) *Response {
	if rw == nil {
		rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	}
	bufferPool := assignPool(size)
	res := responsePool.Get().(*Response)
	res.handlerHeader = headerPool.Get().(http.Header)
	res.contentLength = -1
	res.req = req
	res.conn = conn
	res.rw = rw
	res.cw.res = res
	res.w = NewBufioWriterSize(&res.cw, size)
	res.bufferPool = bufferPool
	res.buffer = bufferPool.Get().([]byte)
	return res
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
	if !w.cw.chunking {
		offset := w.written
		w.written += int64(lenData) // ignoring errors, for errorKludge
		if w.contentLength != -1 && w.written > w.contentLength {
			return 0, http.ErrContentLength
		}
		if !w.noCache && w.written <= int64(len(w.buffer)) {
			copy(w.buffer[offset:w.written], data)
			return
		}
		if !w.noCache {
			w.noCache = true
			if offset > 0 {
				w.w.Write(w.buffer[:offset])
			}
		}
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
	if cl := w.handlerHeader.Get(contentLength); cl != emptyString {
		v, err := strconv.ParseInt(cl, 10, 64)
		if err == nil && v >= 0 {
			w.contentLength = v
			w.setHeader.contentLength = cl
		} else {
			w.handlerHeader.Del(contentLength)
		}
	} else if te := w.handlerHeader.Get(transferEncoding); te == chunked {
		w.cw.chunking = true
		w.setHeader.transferEncoding = chunked
	}
}

// Hijack implements the http.Hijacker interface.
//
// Hijack lets the caller take over the connection.
// After a call to Hijack the HTTP server library
// will not do anything else with the connection.
func (w *Response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	if w.wroteHeader {
		w.cw.flush()
	}
	return w.conn, w.rw, nil
}

// Flush implements the http.Flusher interface.
//
// Flush writes any buffered data to the underlying connection.
func (w *Response) Flush() {
	if w.hijacked {
		return
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.noCache {
		if w.written > 0 {
			w.w.Write(w.buffer[:w.written])
			w.written = 0
		}
	}
	w.w.Flush()
	w.cw.flush()
}

// FinishRequest finishes a request.
func (w *Response) FinishRequest() {
	w.handlerDone.setTrue()
	w.Flush()
	w.w.Flush()
	FreeBufioWriter(w.w)
	w.cw.close()
	w.rw.Flush()
	// Close the body (regardless of w.closeAfterReply) so we can
	// re-use its bufio.Reader later safely.
	w.req.Body.Close()

	if w.req.MultipartForm != nil {
		w.req.MultipartForm.RemoveAll()
	}
	freeHeader(w.handlerHeader)
	w.buffer = w.buffer[:cap(w.buffer)]
	w.bufferPool.Put(w.buffer)
}

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
