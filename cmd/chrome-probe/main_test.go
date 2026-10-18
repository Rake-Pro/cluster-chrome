package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

const canned = `{"webdriver":false,"userAgent":"UA","platform":"Linux x86_64","languages":["en-US"],
"hardwareConcurrency":8,"deviceMemory":8,"screen":{"width":1920,"height":1080,"colorDepth":24},
"webgl":{"vendor":"Google Inc.","renderer":"ANGLE (x)","unmaskedVendor":"Google Inc. (Intel)","unmaskedRenderer":"ANGLE (Intel, Mesa)"},
"plugins":5,"timezone":"UTC"}`

func fakeChrome(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Browser":"Chrome/test","User-Agent":"UA"}`))
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		ws := "ws" + strings.TrimPrefix(srv.URL, "http") + "/devtools/page/1"
		json.NewEncoder(w).Encode([]map[string]string{{"type": "page", "webSocketDebuggerUrl": ws}})
	})
	mux.Handle("/devtools/page/1", websocket.Handler(func(c *websocket.Conn) {
		for {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if websocket.JSON.Receive(c, &req) != nil {
				return
			}
			result := map[string]any{}
			if req.Method == "Runtime.evaluate" {
				result = map[string]any{"result": map[string]any{"type": "object", "value": json.RawMessage(canned)}}
			}
			websocket.JSON.Send(c, map[string]any{"id": req.ID, "result": result})
			if req.Method == "Page.navigate" {
				websocket.JSON.Send(c, map[string]any{"method": "Page.loadEventFired", "params": map[string]any{}})
			}
		}
	}))
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunParsesResult(t *testing.T) {
	srv := fakeChrome(t)
	var out, errb bytes.Buffer
	if code := run([]string{"-devtools", srv.URL}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	var rep report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if rep.Webdriver == nil || *rep.Webdriver || rep.HardwareConcurrency != 8 || rep.Screen.Width != 1920 {
		t.Errorf("unexpected report: %+v", rep)
	}
	if !strings.Contains(errb.String(), "webdriver=false") {
		t.Errorf("summary missing webdriver=false: %s", errb.String())
	}
}

func TestRunNavigate(t *testing.T) {
	srv := fakeChrome(t)
	var out, errb bytes.Buffer
	args := []string{"-devtools", srv.URL, "-url", "http://example.test/", "-json=false"}
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "webdriver: false") {
		t.Errorf("missing key: value line: %s", out.String())
	}
}

func TestExpectWebdriverMismatch(t *testing.T) {
	srv := fakeChrome(t)
	var out, errb bytes.Buffer
	if code := run([]string{"-devtools", srv.URL, "-expect-webdriver=true"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2; stderr: %s", code, errb.String())
	}
}

func TestConnectionError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-devtools", "http://127.0.0.1:1", "-timeout", "2s"}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}
