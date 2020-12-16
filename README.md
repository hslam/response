# response
[![PkgGoDev](https://pkg.go.dev/badge/github.com/hslam/response)](https://pkg.go.dev/github.com/hslam/response)
[![Build Status](https://api.travis-ci.com/hslam/response.svg?branch=master)](https://travis-ci.com/hslam/response)
[![Go Report Card](https://goreportcard.com/badge/github.com/hslam/response)](https://goreportcard.com/report/github.com/hslam/response)
[![LICENSE](https://img.shields.io/github/license/hslam/response.svg?style=flat-square)](https://github.com/hslam/response/blob/master/LICENSE)

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
```go
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
		w.Write([]byte("Hello World"))
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
			rw := bufio.NewReadWriter(reader, bufio.NewWriter(conn))
			var err error
			var req *http.Request
			for {
				req, err = http.ReadRequest(reader)
				if err != nil {
					break
				}
				res := response.NewResponse(req, conn, rw)
				handler.ServeHTTP(res, req)
				res.FinishRequest()
				response.FreeResponse(res)
			}
		}(conn)
	}
}
```

#### Netpoll Example
```go
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
		w.Write([]byte("Hello World"))
	})
	ListenAndServe(":8080", m)
}

func ListenAndServe(addr string, handler http.Handler) error {
	var h = &netpoll.ConnHandler{}
	type Context struct {
		reader  *bufio.Reader
		rw      *bufio.ReadWriter
		conn    net.Conn
		serving sync.Mutex
	}
	h.SetUpgrade(func(conn net.Conn) (netpoll.Context, error) {
		reader := bufio.NewReader(conn)
		rw := bufio.NewReadWriter(reader, bufio.NewWriter(conn))
		return &Context{reader: reader, conn: conn, rw: rw}, nil
	})
	h.SetServe(func(context netpoll.Context) error {
		ctx := context.(*Context)
		ctx.serving.Lock()
		req, err := http.ReadRequest(ctx.reader)
		if err != nil {
			ctx.serving.Unlock()
			return err
		}
		res := response.NewResponse(req, ctx.conn, ctx.rw)
		handler.ServeHTTP(res, req)
		res.FinishRequest()
		ctx.serving.Unlock()
		response.FreeResponse(res)
		return nil
	})
	return netpoll.ListenAndServe("tcp", addr, h)
}
```

curl -XGET http://localhost:8080
```
Hello World
```

### License
This package is licensed under a MIT license (Copyright (c) 2020 Meng Huang)


### Author
response was written by Meng Huang.


