// Copyright (c) 2020 Meng Huang (mhboy@outlook.com)
// This package is licensed under a MIT license that can be found in the LICENSE file.

package response

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func testHTTP(method, url string, status int, result string, t *testing.T) {
	var req *http.Request
	var err error
	req, err = http.NewRequest(method, url, nil)
	if err != nil {
		t.Error(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:   1,
			DisableKeepAlives: true,
		},
	}
	if resp, err := client.Do(req); err != nil {
		t.Error(err)
	} else if resp.StatusCode != status {
		t.Error(resp.StatusCode)
	} else if body, err := ioutil.ReadAll(resp.Body); err != nil {
		t.Error(err)
	} else if string(body) != result {
		t.Error(string(body))
	}
}

func testHeader(method, url string, status int, result string, header map[string]string, t *testing.T) {
	var req *http.Request
	var err error
	req, err = http.NewRequest(method, url, nil)
	if err != nil {
		t.Error(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:   1,
			DisableKeepAlives: true,
		},
	}
	if resp, err := client.Do(req); err != nil {
		t.Error(err)
	} else if resp.StatusCode != status {
		t.Error(resp.StatusCode)
	} else if body, err := ioutil.ReadAll(resp.Body); err != nil {
		t.Error(err)
	} else if string(body) != result {
		t.Error(string(body))
	} else {
		for k, v := range header {
			value := resp.Header.Get(k)
			if value != v {
				t.Error(k, v, value)
			}
		}
	}
}

func testMultipart(url string, status int, result string, values map[string]io.Reader, t *testing.T) {
	var b bytes.Buffer
	var err error
	w := multipart.NewWriter(&b)
	for key, r := range values {
		var fw io.Writer
		if x, ok := r.(io.Closer); ok {
			defer x.Close()
		}
		if x, ok := r.(*os.File); ok {
			if fw, err = w.CreateFormFile(key, x.Name()); err != nil {
				t.Error(err)
			}
		} else {
			if fw, err = w.CreateFormField(key); err != nil {
				t.Error(err)
			}
		}
		if _, err = io.Copy(fw, r); err != nil {
			t.Error(err)
		}
	}
	w.Close()
	var req *http.Request
	req, err = http.NewRequest("POST", url, &b)
	if err != nil {
		t.Error(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:   1,
			DisableKeepAlives: true,
		},
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if resp, err := client.Do(req); err != nil {
		t.Error(err)
	} else if resp.StatusCode != status {
		t.Error(resp.StatusCode)
	} else if body, err := ioutil.ReadAll(resp.Body); err != nil {
		t.Error(err)
	} else if string(body) != result {
		t.Error(string(body))
	}
}

func TestResponse(t *testing.T) {
	m := http.NewServeMux()
	length := 1024 * 64
	var msg = make([]byte, length)
	for i := 0; i < length; i++ {
		msg[i] = 'a'
	}
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\r\n"))
	})
	m.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Write([]byte("Hello"))
		w.Write([]byte(" World!\r\n"))
	})
	m.HandleFunc("/msg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(contentLength, strconv.FormatInt(int64(len(msg)), 10))
		w.Write(msg)
	})
	m.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	m.HandleFunc("/header", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	})
	m.HandleFunc("/multipart", func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1024)
		mf := r.MultipartForm
		if mf != nil {
			w.Write([]byte(mf.Value["value"][0]))
		}
	})
	addr := ":8080"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Error(err)
	}
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			go func(conn net.Conn) {
				reader := NewBufioReader(conn)
				writer := NewBufioWriter(conn)
				var err error
				var req *http.Request
				for err == nil {
					req, err = http.ReadRequest(reader)
					if err != nil {
						break
					}
					res := NewResponse(req, conn, bufio.NewReadWriter(reader, writer))
					m.ServeHTTP(res, req)
					res.FinishRequest()
					FreeResponse(res)
				}
				FreeBufioReader(reader)
				FreeBufioWriter(writer)
			}(conn)
		}
	}()
	time.Sleep(time.Millisecond * 10)
	testHTTP("GET", "http://"+addr+"/", http.StatusOK, "Hello World!\r\n", t)
	testHTTP("GET", "http://"+addr+"/chunked", http.StatusOK, "Hello World!\r\n", t)
	testHTTP("GET", "http://"+addr+"/msg", http.StatusOK, string(msg), t)
	testHTTP("GET", "http://"+addr+"/error", http.StatusBadRequest, "", t)
	header := make(map[string]string)
	header["Access-Control-Allow-Origin"] = "*"
	testHeader("GET", "http://"+addr+"/header", http.StatusOK, "", header, t)
	values := make(map[string]io.Reader)
	values["value"] = bytes.NewReader(msg)
	testMultipart("http://"+addr+"/multipart", http.StatusOK, string(msg), values, t)
	ln.Close()
	wg.Wait()
}

func TestNewBufioReader(t *testing.T) {
	reader := NewBufioReader(nil)
	FreeBufioReader(reader)
}

func TestFreeResponse(t *testing.T) {
	FreeResponse(nil)
}

func TestFreeHeader(t *testing.T) {
	freeHeader(nil)
	h := make(http.Header)
	h.Add("Content-Type", defaultContentType)
	freeHeader(h)
}

func TestBodyAllowed(t *testing.T) {
	defer func() {
		e := recover()
		if e == nil {
			t.Error("should panic")
		}
	}()
	res := &Response{}
	res.bodyAllowed()
}

func TestBodyAllowedForStatus(t *testing.T) {
	if bodyAllowedForStatus(100) {
		t.Error(100)
	}
	if bodyAllowedForStatus(204) {
		t.Error(204)
	}
	if bodyAllowedForStatus(304) {
		t.Error(304)
	}
}

func TestCheckWriteHeaderCode(t *testing.T) {
	defer func() {
		e := recover()
		if e == nil {
			t.Error("should panic")
		}
	}()
	checkWriteHeaderCode(0)
}
