# response
Package response implements an HTTP response writer.

## Get started

### Install
```
go get github.com/hslam/response
```
### Import
```
import "github.com/hslam/response"
```
### Usage
#### Example
```
package main

import (
	"bufio"
	"github.com/hslam/mux"
	"github.com/hslam/response"
	"net"
	"net/http"
)

func main() {
	m := mux.New()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\r\n"))
	})
	ListenAndServe(":8080", m)
}

func ListenAndServe(addr string, handler http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(conn net.Conn) {
			reader := bufio.NewReader(conn)
			var err error
			var req *http.Request
			for err == nil {
				req, err = http.ReadRequest(reader)
				if err != nil {
					break
				}
				res := response.NewResponse(conn)
				handler.ServeHTTP(res, req)
				err = res.Flush()
				response.FreeResponse(res)
			}
		}(conn)
	}
}
```

#### Netpoll Example
```
package main

import (
	"bufio"
	"github.com/hslam/mux"
	"github.com/hslam/netpoll"
	"github.com/hslam/response"
	"net"
	"net/http"
	"sync"
)

func main() {
	m := mux.New()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\r\n"))
	})
	ListenAndServe(":8080", m)
}

func ListenAndServe(addr string, handler http.Handler) error {
	var h = &netpoll.ConnHandler{}
	type Context struct {
		reader  *bufio.Reader
		conn    net.Conn
		reading sync.Mutex
	}
	h.SetUpgrade(func(conn net.Conn) (netpoll.Context, error) {
		return &Context{reader: bufio.NewReader(conn), conn: conn}, nil
	})
	h.SetServe(func(context netpoll.Context) error {
		ctx := context.(*Context)
		ctx.reading.Lock()
		req, err := http.ReadRequest(ctx.reader)
		ctx.reading.Unlock()
		if err != nil {
			return err
		}
		res := response.NewResponse(ctx.conn)
		handler.ServeHTTP(res, req)
		err = res.Flush()
		response.FreeResponse(res)
		return err
	})
	return netpoll.ListenAndServe("tcp", addr, h)
}
```

curl -XGET http://localhost:8080
```
Hello World!
```

### License
This package is licensed under a MIT license (Copyright (c) 2020 Meng Huang)


### Author
response was written by Meng Huang.


