package script

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"nitrobutton.com/nitro-uber-pad/internal/config"
)

const maxHTTPBody = 64 << 20

// httpTimeout returns the timeout given as a Lua number of seconds, or the
// default timeout selected in the settings.
func (e *Engine) httpTimeout(v lua.LValue) time.Duration {
	if n, ok := v.(lua.LNumber); ok && n > 0 {
		return time.Duration(float64(n) * float64(time.Second))
	}
	if t := e.h.HTTPTimeout(); t > 0 {
		return t
	}
	return config.DefaultHTTPTimeout * time.Second
}

func httpFail(L *lua.LState, msg string) int {
	L.Push(lua.LNil)
	L.Push(lua.LNumber(0))
	L.Push(lua.LString(msg))
	L.Push(lua.LNil)
	return 4
}

// doHTTP performs a request and pushes body, status, err, headers.
func (e *Engine) doHTTP(L *lua.LState, method, rawURL string, body *string, contentType string, headers *lua.LTable, timeout time.Duration) int {
	start := time.Now()
	destination := "(invalid URL)"
	if u, err := url.Parse(rawURL); err == nil {
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		destination = u.String()
	}
	e.log("network", fmt.Sprintf("HTTP start %s %s; timeout=%s", method, destination, timeout))
	defer func() { e.log("network", fmt.Sprintf("HTTP finished %s %s in %s", method, destination, time.Since(start))) }()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(*body)
	}
	req, err := http.NewRequest(method, rawURL, rd)
	if err != nil {
		e.log("network", "Invalid HTTP request")
		return httpFail(L, err.Error())
	}
	if ctx := L.Context(); ctx != nil { req = req.WithContext(ctx) }
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		ConnectStart: func(network, addr string) { e.log("network", fmt.Sprintf("Connect %s %s", network, addr)) },
		ConnectDone: func(network, addr string, err error) { e.log("network", fmt.Sprintf("Connected %s %s; error=%v", network, addr, err)) },
		GotConn: func(info httptrace.GotConnInfo) { e.log("network", fmt.Sprintf("Connection acquired; reused=%t idle=%t", info.Reused, info.WasIdle)) },
	}))
	req.Header.Set("User-Agent", "NitroUberPad/"+config.Version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if headers != nil {
		headers.ForEach(func(k, v lua.LValue) { req.Header.Set(k.String(), v.String()) })
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		logged := err
		if ue, ok := err.(*url.Error); ok { logged = ue.Err }
		e.log("network", fmt.Sprintf("HTTP failed: %v", logged))
		return httpFail(L, err.Error())
	}
	defer resp.Body.Close()
	data, rerr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody+1))
	if len(data) > maxHTTPBody {
		data = data[:maxHTTPBody]
		rerr = fmt.Errorf("response exceeds %d-byte limit", maxHTTPBody)
	}
	e.log("network", fmt.Sprintf("HTTP response %s %s: status=%d bytes=%d read_error=%v", method, destination, resp.StatusCode, len(data), rerr))
	hdr := L.NewTable()
	for k, v := range resp.Header {
		hdr.RawSetString(strings.ToLower(k), lua.LString(strings.Join(v, ", ")))
	}
	L.Push(lua.LString(data))
	L.Push(lua.LNumber(resp.StatusCode))
	if rerr != nil {
		L.Push(lua.LString(rerr.Error()))
	} else {
		L.Push(lua.LNil)
	}
	L.Push(hdr)
	return 4
}

func (e *Engine) registerHTTP() {
	L := e.L
	L.SetGlobal("http", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"get": func(L *lua.LState) int {
			return e.doHTTP(L, http.MethodGet, L.CheckString(1), nil, "", L.OptTable(3, nil), e.httpTimeout(L.Get(2)))
		},
		"post": func(L *lua.LState) int {
			body := L.OptString(2, "")
			return e.doHTTP(L, http.MethodPost, L.CheckString(1), &body,
				L.OptString(3, "text/plain; charset=utf-8"), L.OptTable(5, nil), e.httpTimeout(L.Get(4)))
		},
		"request": func(L *lua.LState) int {
			t := L.CheckTable(1)
			method := strings.ToUpper(lua.LVAsString(t.RawGetString("method")))
			if method == "" {
				method = http.MethodGet
			}
			u := lua.LVAsString(t.RawGetString("url"))
			if u == "" {
				L.ArgError(1, "url is required")
				return 0
			}
			var body *string
			if b := t.RawGetString("body"); b != lua.LNil {
				s := lua.LVAsString(b)
				body = &s
			}
			var headers *lua.LTable
			if h, ok := t.RawGetString("headers").(*lua.LTable); ok {
				headers = h
			}
			return e.doHTTP(L, method, u, body, "", headers, e.httpTimeout(t.RawGetString("timeout")))
		},
		"timeout": func(L *lua.LState) int {
			L.Push(lua.LNumber(e.httpTimeout(lua.LNil).Seconds()))
			return 1
		},
		"urlencode": func(L *lua.LState) int {
			L.Push(lua.LString(url.QueryEscape(L.CheckString(1))))
			return 1
		},
	}))
}
