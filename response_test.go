package response

import (
	"bufio"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestResponse(t *testing.T) {
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\r\n"))
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
				reader := bufio.NewReader(conn)
				var err error
				var req *http.Request
				for err == nil {
					req, err = http.ReadRequest(reader)
					if err != nil {
						break
					}
					res := NewResponse(req, conn, nil)
					m.ServeHTTP(res, req)
					res.FinishRequest()
					FreeResponse(res)
				}
			}(conn)
		}
	}()
	time.Sleep(time.Millisecond * 10)
	testHTTP("GET", "http://"+addr+"/", http.StatusOK, "Hello World!\r\n", t)
	ln.Close()
	wg.Wait()
}

func testHTTP(method, url string, status int, result string, t *testing.T) {
	var req *http.Request
	req, _ = http.NewRequest(method, url, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Error(err)
	} else if resp.StatusCode != status {
		t.Error(resp.StatusCode)
	} else if body, err := ioutil.ReadAll(resp.Body); err != nil {
		t.Error(err)
	} else if string(body) != result {
		t.Error(string(body))
	}
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
