package core

import (
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// Limits shared by the HTTP listeners lark-agent-bot owns (webhook, bridge,
// management API, local API socket, provider proxy).
const (
	// serverReadHeaderTimeout bounds how long a client may take to send the
	// request headers, so idle half-open connections cannot pile up.
	serverReadHeaderTimeout = 10 * time.Second

	// jsonBodyLimit caps the body of JSON API requests that carry no
	// attachments. Endpoints that accept base64 attachments size their own
	// limits (see APIServer.sendBodyLimit).
	jsonBodyLimit int64 = 1 << 20 // 1 MiB
)

// capRequestBody wraps r.Body in http.MaxBytesReader and reports whether the
// declared Content-Length already exceeds limit, so the caller can answer 413
// before reading anything. A body sent without Content-Length (chunked) that
// runs past the limit makes the next read fail with *http.MaxBytesError; see
// isBodyTooLarge.
func capRequestBody(w http.ResponseWriter, r *http.Request, limit int64) (tooLarge bool) {
	if r.ContentLength > limit {
		return true
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	return false
}

// isBodyTooLarge reports whether err comes from reading past a
// http.MaxBytesReader limit.
func isBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// isJSONContentType reports whether the request declares a JSON body
// (application/json, parameters such as charset allowed). Requiring it keeps a
// browser from posting to the endpoint with a cross-site "simple" request
// (text/plain, form encodings), which skips the CORS preflight.
func isJSONContentType(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil && !errors.Is(err, mime.ErrInvalidMediaParameter) {
		return false
	}
	return mediaType == "application/json"
}

// loopbackListenAddr returns the address for a listener that must only be
// reachable from this machine.
func loopbackListenAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// allInterfacesListenAddr returns the address for a listener on every
// interface.
func allInterfacesListenAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}

// isLoopbackRemote reports whether the request came from this machine.
func isLoopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}

// isLoopbackHost reports whether the request's Host header names this machine
// (localhost, 127.0.0.0/8 or ::1). A listener that runs without a token relies
// on only local clients reaching it; checking Host as well keeps a web page
// that re-points its own domain at 127.0.0.1 (DNS rebinding) from talking to
// it as a same-origin page.
func isLoopbackHost(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}

// isLocalRequest reports whether a request both came from and was addressed to
// this machine; tokenless listeners accept nothing else.
func isLocalRequest(r *http.Request) bool {
	return isLoopbackRemote(r) && isLoopbackHost(r)
}
