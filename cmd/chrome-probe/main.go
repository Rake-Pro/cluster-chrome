// chrome-probe connects to a running Chrome over the DevTools Protocol and
// reports browser fingerprint signals (webdriver flag, WebGL renderer, ...).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const probeJS = `(() => {
  const gl = document.createElement('canvas').getContext('webgl');
  const ext = gl && gl.getExtension('WEBGL_debug_renderer_info');
  return {
    webdriver: navigator.webdriver,
    userAgent: navigator.userAgent,
    platform: navigator.platform,
    languages: navigator.languages,
    hardwareConcurrency: navigator.hardwareConcurrency,
    deviceMemory: navigator.deviceMemory,
    screen: {width: screen.width, height: screen.height, colorDepth: screen.colorDepth},
    webgl: {
      vendor: gl ? gl.getParameter(gl.VENDOR) : null,
      renderer: gl ? gl.getParameter(gl.RENDERER) : null,
      unmaskedVendor: ext ? gl.getParameter(ext.UNMASKED_VENDOR_WEBGL) : null,
      unmaskedRenderer: ext ? gl.getParameter(ext.UNMASKED_RENDERER_WEBGL) : null,
    },
    plugins: navigator.plugins.length,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  };
})()`

type report struct {
	Webdriver           *bool    `json:"webdriver"`
	UserAgent           string   `json:"userAgent"`
	Platform            string   `json:"platform"`
	Languages           []string `json:"languages"`
	HardwareConcurrency int      `json:"hardwareConcurrency"`
	DeviceMemory        *float64 `json:"deviceMemory"`
	Screen              struct {
		Width      int `json:"width"`
		Height     int `json:"height"`
		ColorDepth int `json:"colorDepth"`
	} `json:"screen"`
	WebGL struct {
		Vendor           *string `json:"vendor"`
		Renderer         *string `json:"renderer"`
		UnmaskedVendor   *string `json:"unmaskedVendor"`
		UnmaskedRenderer *string `json:"unmaskedRenderer"`
	} `json:"webgl"`
	Plugins  int    `json:"plugins"`
	Timezone string `json:"timezone"`
}

// renderer prefers the unmasked renderer, which names the real GPU or software rasterizer.
func (r *report) renderer() string {
	if r.WebGL.UnmaskedRenderer != nil {
		return *r.WebGL.UnmaskedRenderer
	}
	if r.WebGL.Renderer != nil {
		return *r.WebGL.Renderer
	}
	return ""
}

func (r *report) webdriverStr() string {
	if r.Webdriver == nil {
		return "undefined"
	}
	return fmt.Sprint(*r.Webdriver)
}

func main() {

	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chrome-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	devtools := fs.String("devtools", "http://127.0.0.1:9222", "DevTools HTTP endpoint")
	pageURL := fs.String("url", "about:blank", "page to navigate to before evaluating")
	timeout := fs.Duration("timeout", 30*time.Second, "timeout for the whole run")
	wait := fs.Duration("wait", 0, "retry the initial connection for this long")
	expectWD := fs.String("expect-webdriver", "", `assert navigator.webdriver: "true" or "false"`)
	failSW := fs.Bool("fail-on-swiftshader", false, "exit 2 if the WebGL renderer is SwiftShader or llvmpipe")
	asJSON := fs.Bool("json", true, "print JSON (false: key: value lines)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *expectWD != "" && *expectWD != "true" && *expectWD != "false" {
		fmt.Fprintln(stderr, `error: -expect-webdriver must be "true" or "false"`)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	base := strings.TrimRight(*devtools, "/")

	raw, err := probe(ctx, base, *pageURL, *wait, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	var rep report
	if err := json.Unmarshal(raw, &rep); err != nil {
		fmt.Fprintln(stderr, "error: parse result:", err)
		return 1
	}

	if *asJSON {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		fmt.Fprintln(stdout, buf.String())
	} else {
		printLines(stdout, &rep)
	}
	fmt.Fprintf(stderr, "webdriver=%s renderer=%q\n", rep.webdriverStr(), rep.renderer())

	code := 0
	if *expectWD != "" && rep.webdriverStr() != *expectWD {
		fmt.Fprintf(stderr, "assertion failed: webdriver=%s, expected %s\n", rep.webdriverStr(), *expectWD)
		code = 2
	}
	if r := rep.renderer(); *failSW && (strings.Contains(r, "SwiftShader") || strings.Contains(r, "llvmpipe")) {
		fmt.Fprintf(stderr, "assertion failed: software renderer %q\n", r)
		code = 2
	}
	return code
}

func printLines(w io.Writer, r *report) {
	str := func(p *string) string {
		if p == nil {
			return "null"
		}
		return *p
	}
	mem := "null"
	if r.DeviceMemory != nil {
		mem = fmt.Sprint(*r.DeviceMemory)
	}
	fmt.Fprintf(w, "webdriver: %s\n", r.webdriverStr())
	fmt.Fprintf(w, "userAgent: %s\n", r.UserAgent)
	fmt.Fprintf(w, "platform: %s\n", r.Platform)
	fmt.Fprintf(w, "languages: %s\n", strings.Join(r.Languages, ","))
	fmt.Fprintf(w, "hardwareConcurrency: %d\n", r.HardwareConcurrency)
	fmt.Fprintf(w, "deviceMemory: %s\n", mem)
	fmt.Fprintf(w, "screen: %dx%d depth %d\n", r.Screen.Width, r.Screen.Height, r.Screen.ColorDepth)
	fmt.Fprintf(w, "webgl.vendor: %s\n", str(r.WebGL.Vendor))
	fmt.Fprintf(w, "webgl.renderer: %s\n", str(r.WebGL.Renderer))
	fmt.Fprintf(w, "webgl.unmaskedVendor: %s\n", str(r.WebGL.UnmaskedVendor))
	fmt.Fprintf(w, "webgl.unmaskedRenderer: %s\n", str(r.WebGL.UnmaskedRenderer))
	fmt.Fprintf(w, "plugins: %d\n", r.Plugins)
	fmt.Fprintf(w, "timezone: %s\n", r.Timezone)
}

// probe returns the raw JSON object produced by probeJS.
func probe(ctx context.Context, base, pageURL string, wait time.Duration, stderr io.Writer) (json.RawMessage, error) {
	var ver struct {
		Browser   string `json:"Browser"`
		UserAgent string `json:"User-Agent"`
	}
	if err := waitVersion(ctx, base, wait, &ver); err != nil {
		return nil, err
	}
	fmt.Fprintf(stderr, "Browser: %s\nUser-Agent: %s\n", ver.Browser, ver.UserAgent)

	wsURL, err := pageTarget(ctx, base)
	if err != nil {
		return nil, err
	}
	c, err := dial(ctx, wsURL, base)
	if err != nil {
		return nil, err
	}
	defer c.conn.Close()

	if pageURL != "about:blank" {
		if _, err := c.call("Page.enable", nil); err != nil {
			return nil, err
		}
		if _, err := c.call("Page.navigate", map[string]any{"url": pageURL}); err != nil {
			return nil, err
		}
		if err := c.waitEvent("Page.loadEventFired"); err != nil {
			return nil, err
		}
	}

	res, err := c.call("Runtime.evaluate", map[string]any{
		"expression": probeJS, "returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("parse Runtime.evaluate result: %w", err)
	}
	if out.ExceptionDetails != nil {
		return nil, fmt.Errorf("script exception: %s", out.ExceptionDetails.Text)
	}
	if len(out.Result.Value) == 0 {
		return nil, errors.New("Runtime.evaluate returned no value")
	}
	return out.Result.Value, nil
}

func httpJSON(ctx context.Context, method, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s", method, u, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func waitVersion(ctx context.Context, base string, wait time.Duration, out any) error {
	deadline := time.Now().Add(wait)
	for {
		err := httpJSON(ctx, http.MethodGet, base+"/json/version", out)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return err
		}
	}
}

type target struct {
	Type string `json:"type"`
	WS   string `json:"webSocketDebuggerUrl"`
}

func pageTarget(ctx context.Context, base string) (string, error) {
	var list []target
	if err := httpJSON(ctx, http.MethodGet, base+"/json/list", &list); err != nil {
		return "", err
	}
	for _, t := range list {
		if t.Type == "page" && t.WS != "" {
			return t.WS, nil
		}
	}
	// Newer Chrome requires PUT for /json/new and rejects GET/POST; older accepts POST.
	var t target
	err := httpJSON(ctx, http.MethodPut, base+"/json/new?about:blank", &t)
	if err != nil {
		if err2 := httpJSON(ctx, http.MethodPost, base+"/json/new?about:blank", &t); err2 != nil {
			return "", fmt.Errorf("create target: PUT: %v; POST: %v", err, err2)
		}
	}
	if t.WS == "" {
		return "", errors.New("new target has no webSocketDebuggerUrl")
	}
	return t.WS, nil
}

type cdp struct {
	conn *websocket.Conn
	id   int
	seen map[string]bool
}

func dial(ctx context.Context, wsURL, base string) (*cdp, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	cfg, err := websocket.NewConfig(wsURL, u.Scheme+"://"+u.Host)
	if err != nil {
		return nil, err
	}
	conn, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	return &cdp{conn: conn, seen: map[string]bool{}}, nil
}

type message struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// call sends a command and waits for the response with the same id.
// Events seen on the way are remembered for waitEvent.
func (c *cdp) call(method string, params any) (json.RawMessage, error) {
	c.id++
	if err := websocket.JSON.Send(c.conn, map[string]any{"id": c.id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("%s: send: %w", method, err)
	}
	for {
		var m message
		if err := websocket.JSON.Receive(c.conn, &m); err != nil {
			return nil, fmt.Errorf("%s: receive: %w", method, err)
		}
		if m.ID == 0 {
			c.seen[m.Method] = true
			continue
		}
		if m.ID != c.id {
			continue
		}
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	}
}

func (c *cdp) waitEvent(method string) error {
	for !c.seen[method] {
		var m message
		if err := websocket.JSON.Receive(c.conn, &m); err != nil {
			return fmt.Errorf("waiting for %s: %w", method, err)
		}
		if m.ID == 0 {
			c.seen[m.Method] = true
		}
	}
	return nil
}
