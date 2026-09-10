package gatesentryproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// countingTripper records how many times the proxy fetched origin.
type countingTripper struct {
	hits int32
	body string
	cc   string
	ct   string
}

func (c *countingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&c.hits, 1)
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", c.ct)
	rec.Header().Set("Cache-Control", c.cc)
	rec.Header().Set("ETag", `"v1"`)
	_, _ = rec.WriteString(c.body)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func stubProxyForCacheTest() {
	p := NewGSProxy()
	p.IsAuthEnabled = func() bool { return false }
	p.TimeAccessHandler = func(*GSTimeAccessFilterData) {}
	p.UrlAccessHandler = func(*GSUrlFilterData) {}
	p.ContentHandler = func(*GSContentFilterData) {}
	p.ContentSizeHandler = func(GSContentSizeFilterData) {}
	p.LogHandler = func(GSLogData) {}
	p.ProxyErrorHandler = func(*GSProxyErrorData) {}
	p.UserAccessHandler = func(*GSUserAccessFilterData) {}
	p.DoMitm = func(string) bool { return false }
	p.IsExceptionUrl = func(string) bool { return false }
}

func TestProxyDoesNotHTTPCacheResponses(t *testing.T) {
	stubProxyForCacheTest()

	rt := &countingTripper{
		body: "<html><body>hello</body></html>",
		cc:   "public, max-age=3600",
		ct:   "text/html; charset=utf-8",
	}
	h := ProxyHandler{rt: rt, Iproxy: IProxy}

	req := httptest.NewRequest(http.MethodGet, "http://192.0.2.10/page", nil)
	req.RemoteAddr = "192.0.2.20:1234"
	req.Header.Set("Accept-Encoding", "gzip")

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req.Clone(req.Context()))
		res := w.Result()
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status %d", i+1, res.StatusCode)
		}
		gotCC := res.Header.Get("Cache-Control")
		if !strings.Contains(gotCC, "max-age=3600") {
			t.Fatalf("request %d: Cache-Control not forwarded, got %q", i+1, gotCC)
		}
		if res.Header.Get("Age") != "" {
			t.Fatalf("request %d: unexpected Age header %q (caching proxies set Age)", i+1, res.Header.Get("Age"))
		}
	}

	if atomic.LoadInt32(&rt.hits) != 2 {
		t.Fatalf("origin fetches = %d, want 2 (proxy has no HTTP content cache; Cache-Control is forwarded, not honoured)", rt.hits)
	}
}

func TestCopyResponseHeaderForwardsCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	upstream := httptest.NewRecorder()
	upstream.Header().Set("Cache-Control", "no-store")
	upstream.Header().Set("Content-Type", "text/html")
	resp := upstream.Result()
	resp.ProtoMajor, resp.ProtoMinor = 1, 1

	copyResponseHeader(rec, resp)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store forwarded", got)
	}
}
