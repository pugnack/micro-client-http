package http

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"go.unistack.org/micro/v4/client"
)

var (
	// DefaultPoolMaxStreams maximum streams on a connectioin
	// (20)
	DefaultPoolMaxStreams = 20

	// DefaultPoolMaxIdle maximum idle conns of a pool
	// (50)
	DefaultPoolMaxIdle = 50

	// DefaultMaxRecvMsgSize maximum message that client can receive
	// (4 MB).
	DefaultMaxRecvMsgSize = 1024 * 1024 * 4

	// DefaultMaxSendMsgSize maximum message that client can send
	// (4 MB).
	DefaultMaxSendMsgSize = 1024 * 1024 * 4
)

type poolMaxStreams struct{}

// PoolMaxStreams maximum streams on a connectioin
func PoolMaxStreams(n int) client.Option {
	return client.SetOption(poolMaxStreams{}, n)
}

type poolMaxIdle struct{}

// PoolMaxIdle maximum idle conns of a pool
func PoolMaxIdle(d int) client.Option {
	return client.SetOption(poolMaxIdle{}, d)
}

type maxRecvMsgSizeKey struct{}

// MaxRecvMsgSize set the maximum size of message that client can receive.
func MaxRecvMsgSize(s int) client.Option {
	return client.SetOption(maxRecvMsgSizeKey{}, s)
}

type maxSendMsgSizeKey struct{}

// MaxSendMsgSize set the maximum size of message that client can send.
func MaxSendMsgSize(s int) client.Option {
	return client.SetOption(maxSendMsgSizeKey{}, s)
}

type httpClientKey struct{}

// nolint: golint
// HTTPClient pass http.Client option to client Call
func HTTPClient(c *http.Client) client.Option {
	return client.SetOption(httpClientKey{}, c)
}

// httpClientFromOpts extracts http client from client options.
func httpClientFromOpts(opts client.Options) (*http.Client, bool) {
	httpClient, ok := opts.Context.Value(httpClientKey{}).(*http.Client)
	return httpClient, ok
}

func defaultHTTPClient(
	dialer func(ctx context.Context, addr string) (net.Conn, error),
	tlsConfig *tls.Config,
) *http.Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer(ctx, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxConnsPerHost:       100,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}
	return &http.Client{Transport: tr}
}

type httpDialerKey struct{}

// nolint: golint
// HTTPDialer pass net.Dialer option to client
func HTTPDialer(d *net.Dialer) client.Option {
	return client.SetOption(httpDialerKey{}, d)
}

// httpDialerFromOpts extracts dialer func from client options.
func httpDialerFromOpts(opts client.Options) (dialerFunc func(context.Context, string) (net.Conn, error), ok bool) {
	var d *net.Dialer

	if d, ok = opts.Context.Value(httpDialerKey{}).(*net.Dialer); ok {
		dialerFunc = func(ctx context.Context, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", addr)
		}
	}

	if opts.ContextDialer != nil {
		dialerFunc, ok = opts.ContextDialer, true
	}

	return dialerFunc, ok
}

func defaultHTTPDialer() func(ctx context.Context, addr string) (net.Conn, error) {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		d := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		return d.DialContext(ctx, "tcp", addr)
	}
}

type methodKey struct{}

// Method pass method option to client Call
func Method(m string) client.CallOption {
	return client.SetCallOption(methodKey{}, m)
}

func methodFromOpts(opts client.CallOptions) (string, bool) {
	m, ok := opts.Context.Value(methodKey{}).(string)
	return m, ok
}

type pathKey struct{}

// Path specifies path option to client Call
func Path(p string) client.CallOption {
	return client.SetCallOption(pathKey{}, p)
}

func pathFromOpts(opts client.CallOptions) (string, bool) {
	p, ok := opts.Context.Value(pathKey{}).(string)
	return p, ok
}

type bodyKey struct{}

// Body specifies body option to client Call
func Body(b string) client.CallOption {
	return client.SetCallOption(bodyKey{}, b)
}

func bodyFromOpts(opts client.CallOptions) (string, bool) {
	b, ok := opts.Context.Value(bodyKey{}).(string)
	return b, ok
}

type errorMapKey struct{}

func ErrorMap(m map[string]error) client.CallOption {
	return client.SetCallOption(errorMapKey{}, m)
}

// errorMapFromOpts extracts error map from client call options.
func errorMapFromOpts(opts client.CallOptions) (map[string]error, bool) {
	errMap, ok := opts.Context.Value(errorMapKey{}).(map[string]error)
	return errMap, ok
}

type cookieKey struct{}

// Cookie pass cookie to client Call
func Cookie(cookies ...string) client.CallOption {
	return client.SetCallOption(cookieKey{}, cookies)
}

func cookieFromOpts(opts client.CallOptions) ([]string, bool) {
	c, ok := opts.Context.Value(cookieKey{}).([]string)
	return c, ok
}

type headerKey struct{}

// Header pass cookie to client Call
func Header(headers ...string) client.CallOption {
	return client.SetCallOption(headerKey{}, headers)
}

func headerFromOpts(opts client.CallOptions) ([]string, bool) {
	h, ok := opts.Context.Value(headerKey{}).([]string)
	return h, ok
}
