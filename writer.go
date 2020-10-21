package response

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const commaSpaceChunked = ", chunked"

// This should be >= 512 bytes for DetectContentType,
// but otherwise it's somewhat arbitrary.
const bufferBeforeChunkingSize = 2048

// chunkWriter writes to a response's conn buffer, and is the writer
// wrapped by the response.bufw buffered writer.
//
// chunkWriter also is responsible for finalizing the Header, including
// conditionally setting the Content-Type and setting a Content-Length
// in cases where the handler's final output is smaller than the buffer
// size. It also conditionally adds chunk headers, when in chunking mode.
//
// See the comment above (*response).Write for the entire write flow.
type chunkWriter struct {
	res *Response

	// wroteHeader tells whether the header's been written to "the
	// wire" (or rather: w.conn.buf). this is unlike
	// (*response).wroteHeader, which tells only whether it was
	// logically written.
	wroteHeader bool

	// set by the writeHeader method:
	chunking bool // using chunked transfer encoding for reply body
}

func (cw *chunkWriter) Write(p []byte) (n int, err error) {
	if !cw.wroteHeader {
		cw.writeHeader(p)
	}
	if cw.res.req.Method == head {
		// Eat writes.
		return len(p), nil
	}
	if cw.chunking {
		_, err = fmt.Fprintf(cw.res.rw, chunk, len(p))
		if err != nil {
			cw.res.conn.Close()
			return
		}
	}
	n, err = cw.res.rw.Write(p)
	if cw.chunking && err == nil {
		_, err = cw.res.rw.Write(crlf)
	}
	if err != nil {
		cw.res.conn.Close()
	}
	return
}

func (cw *chunkWriter) flush() {
	if !cw.wroteHeader {
		cw.writeHeader(nil)
	}
	cw.res.rw.Flush()
}

func (cw *chunkWriter) close() {
	if !cw.wroteHeader {
		cw.writeHeader(nil)
	}
	if cw.chunking {
		bw := cw.res.rw // conn's bufio writer
		// zero chunk to mark EOF
		bw.WriteString("0\r\n")
		//if trailers := cw.res.finalTrailers(); trailers != nil {
		//	trailers.Write(bw) // the writer handles noting errors
		//}
		// final blank line after the trailers (whether
		// present or not)
		bw.WriteString("\r\n")
	}
}

func (cw *chunkWriter) writeHeader(p []byte) {
	if cw.wroteHeader {
		return
	}
	cw.wroteHeader = true
	var w = cw.res
	w.setHeader.date = appendTime(cw.res.dateBuf[:0], time.Now())
	if len(w.setHeader.contentLength) > 0 {
		cw.chunking = false
	} else if cw.chunking {
	} else if w.noCache {
		cw.chunking = true
		if len(w.setHeader.transferEncoding) > 0 {
			if !strings.Contains(w.setHeader.transferEncoding, chunked) {
				w.setHeader.transferEncoding += commaSpaceChunked
			}
		} else {
			w.setHeader.transferEncoding = chunked
		}
	} else if w.handlerDone.isSet() && len(p) > 0 {
		w.contentLength = int64(len(p))
		w.setHeader.contentLength = strconv.FormatInt(w.contentLength, 10)
	}
	if ct := w.handlerHeader.Get(contentType); ct != emptyString {
		w.setHeader.contentType = ct
	} else {
		if !cw.chunking && len(p) > 0 {
			w.setHeader.contentType = http.DetectContentType(p)
		}
	}
	if co := w.handlerHeader.Get(connection); co != emptyString {
		w.setHeader.connection = co
	}
	w.rw.WriteString(fmt.Sprintf(statusLine, w.status, http.StatusText(w.status)))
	w.setHeader.Write(w.rw.Writer)
	for key := range w.handlerHeader {
		value := w.handlerHeader.Get(key)
		if key == date || key == contentLength || key == transferEncoding || key == contentType || key == connection {
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
}
