// Package http provides a http client
package http

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.unistack.org/micro/v4/client"
	"go.unistack.org/micro/v4/codec"
	"go.unistack.org/micro/v4/errors"
	"go.unistack.org/micro/v4/logger"
	"go.unistack.org/micro/v4/metadata"
	"go.unistack.org/micro/v4/options"
	"go.unistack.org/micro/v4/selector"
	"go.unistack.org/micro/v4/semconv"
	"go.unistack.org/micro/v4/tracer"
	"google.golang.org/protobuf/proto"

	"go.unistack.org/micro-client-http/v4/builder"
)

var DefaultContentType = "application/json"

type Client struct {
	funcCall   client.FuncCall
	funcStream client.FuncStream
	httpClient *http.Client
	opts       client.Options
	mu         sync.RWMutex
}

func NewClient(opts ...client.Option) *Client {
	clientOpts := client.NewOptions(opts...)

	if len(clientOpts.ContentType) == 0 {
		clientOpts.ContentType = DefaultContentType
	}

	c := &Client{opts: clientOpts}

	dialer, ok := httpDialerFromOpts(clientOpts)
	if !ok {
		dialer = defaultHTTPDialer()
	}

	c.httpClient, ok = httpClientFromOpts(clientOpts)
	if !ok {
		c.httpClient = defaultHTTPClient(dialer, clientOpts.TLSConfig)
	}

	c.funcCall = c.fnCall
	c.funcStream = c.fnStream

	return c
}

func (c *Client) Name() string {
	return c.opts.Name
}

func (c *Client) Init(opts ...client.Option) error {
	for _, o := range opts {
		o(&c.opts)
	}

	c.opts.Hooks.EachPrev(func(hook options.Hook) {
		switch h := hook.(type) {
		case client.HookCall:
			c.funcCall = h(c.funcCall)
		case client.HookStream:
			c.funcStream = h(c.funcStream)
		}
	})

	return nil
}

func (c *Client) Options() client.Options {
	return c.opts
}

func (c *Client) NewRequest(service, method string, req any, opts ...client.RequestOption) client.Request {
	reqOpts := client.NewRequestOptions(opts...)
	if reqOpts.ContentType == "" {
		reqOpts.ContentType = c.opts.ContentType
	}

	return &httpRequest{
		service: service,
		method:  method,
		request: req,
		opts:    reqOpts,
	}
}

func (c *Client) Call(ctx context.Context, req client.Request, rsp any, opts ...client.CallOption) error {
	ts := time.Now()
	c.opts.Meter.Counter(semconv.ClientRequestInflight, "endpoint", req.Endpoint()).Inc()
	var sp tracer.Span
	ctx, sp = c.opts.Tracer.Start(ctx, req.Endpoint()+" rpc-client",
		tracer.WithSpanKind(tracer.SpanKindClient),
		tracer.WithSpanLabels("endpoint", req.Endpoint()),
	)
	err := c.funcCall(ctx, req, rsp, opts...)
	c.opts.Meter.Counter(semconv.ClientRequestInflight, "endpoint", req.Endpoint()).Dec()
	te := time.Since(ts)
	c.opts.Meter.Summary(semconv.ClientRequestLatencyMicroseconds, "endpoint", req.Endpoint()).Update(te.Seconds())
	c.opts.Meter.Histogram(semconv.ClientRequestDurationSeconds, "endpoint", req.Endpoint()).Update(te.Seconds())

	if me := errors.FromError(err); me == nil {
		sp.Finish()
		c.opts.Meter.Counter(semconv.ClientRequestTotal, "endpoint", req.Endpoint(), "status", "success", "code", strconv.Itoa(int(200))).Inc()
	} else {
		sp.SetStatus(tracer.SpanStatusError, err.Error())
		c.opts.Meter.Counter(semconv.ClientRequestTotal, "endpoint", req.Endpoint(), "status", "failure", "code", strconv.Itoa(int(me.Code))).Inc()
	}

	return err
}

func (c *Client) fnCall(ctx context.Context, req client.Request, rsp any, opts ...client.CallOption) error {
	// make a copy of call opts
	callOpts := c.opts.CallOptions
	for _, opt := range opts {
		opt(&callOpts)
	}

	// check if we already have a deadline
	d, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		// no deadline so we create a new one
		ctx, cancel = context.WithTimeout(ctx, callOpts.RequestTimeout)
		defer cancel()
	} else {
		// got a deadline so no need to setup context
		// but we need to set the timeout we pass along
		opt := client.WithRequestTimeout(time.Until(d))
		opt(&callOpts)
	}

	// should we noop right here?
	select {
	case <-ctx.Done():
		return errors.New("go.micro.client", fmt.Sprintf("%v", ctx.Err()), 408)
	default:
	}

	// make copy of call method
	hcall := c.call

	// use the router passed as a call option, or fallback to the rpc clients router
	if callOpts.Router == nil {
		callOpts.Router = c.opts.Router
	}

	if callOpts.Selector == nil {
		callOpts.Selector = c.opts.Selector
	}

	// inject proxy address
	// TODO: don't even bother using Lookup/Select in this case
	if len(c.opts.Proxy) > 0 {
		callOpts.Address = []string{c.opts.Proxy}
	}

	var next selector.Next

	call := func(i int) error {
		// call backoff first. Someone may want an initial start delay
		t, err := callOpts.Backoff(ctx, req, i)
		if err != nil {
			return errors.InternalServerError("go.micro.client", "%+v", err)
		}

		// only sleep if greater than 0
		if t.Seconds() > 0 {
			time.Sleep(t)
		}

		if next == nil {
			var routes []string
			// lookup the route to send the reques to
			// TODO apply any filtering here
			routes, err = c.opts.Lookup(ctx, req, callOpts)
			if err != nil {
				return errors.InternalServerError("go.micro.client", "%+v", err)
			}

			// balance the list of nodes
			next, err = callOpts.Selector.Select(routes)
			if err != nil {
				return err
			}
		}

		node := next()

		// make the call
		err = hcall(ctx, node, req, rsp, callOpts)

		// record the result of the call to inform future routing decisions
		if verr := c.opts.Selector.Record(node, err); verr != nil {
			return verr
		}

		// try and transform the error to a go-micro error
		if verr, ok := err.(*errors.Error); ok {
			return verr
		}

		return err
	}

	ch := make(chan error, callOpts.Retries)
	var gerr error

	for i := 0; i <= callOpts.Retries; i++ {
		go func() {
			ch <- call(i)
		}()

		select {
		case <-ctx.Done():
			return errors.New("go.micro.client", fmt.Sprintf("%v", ctx.Err()), 408)
		case err := <-ch:
			// if the call succeeded lets bail early
			if err == nil {
				return nil
			}

			retry, rerr := callOpts.Retry(ctx, req, i, err)
			if rerr != nil {
				return rerr
			}

			if !retry {
				return err
			}

			gerr = err
		}
	}

	return gerr
}

func (c *Client) call(ctx context.Context, addr string, req client.Request, rsp any, opts client.CallOptions) error {
	ct := req.ContentType()
	if len(opts.ContentType) > 0 {
		ct = opts.ContentType
	}

	cf, err := c.newCodec(ct)
	if err != nil {
		return errors.BadRequest("go.micro.client", "%+v", err)
	}

	hreq, err := buildHTTPRequest(ctx, addr, req.Endpoint(), ct, cf, req.Body(), opts, c.opts.Logger)
	if err != nil {
		return err
	}

	hrsp, err := c.httpClient.Do(hreq)
	if err != nil {
		switch err := err.(type) {
		case *url.Error:
			if err, ok := err.Err.(net.Error); ok && err.Timeout() {
				return errors.Timeout("go.micro.client", "%+v", err)
			}
		case net.Error:
			if err.Timeout() {
				return errors.Timeout("go.micro.client", "%+v", err)
			}
		}
		return errors.InternalServerError("go.micro.client", "%+v", err)
	}

	defer hrsp.Body.Close()

	return c.parseRsp(ctx, hrsp, rsp, opts)
}

func (c *Client) Stream(ctx context.Context, req client.Request, opts ...client.CallOption) (client.Stream, error) {
	ts := time.Now()
	c.opts.Meter.Counter(semconv.ClientRequestInflight, "endpoint", req.Endpoint()).Inc()
	var sp tracer.Span
	ctx, sp = c.opts.Tracer.Start(ctx, req.Endpoint()+" rpc-client",
		tracer.WithSpanKind(tracer.SpanKindClient),
		tracer.WithSpanLabels("endpoint", req.Endpoint()),
	)
	stream, err := c.funcStream(ctx, req, opts...)
	c.opts.Meter.Counter(semconv.ClientRequestInflight, "endpoint", req.Endpoint()).Dec()
	te := time.Since(ts)
	c.opts.Meter.Summary(semconv.ClientRequestLatencyMicroseconds, "endpoint", req.Endpoint()).Update(te.Seconds())
	c.opts.Meter.Histogram(semconv.ClientRequestDurationSeconds, "endpoint", req.Endpoint()).Update(te.Seconds())

	if me := errors.FromError(err); me == nil {
		sp.Finish()
		c.opts.Meter.Counter(semconv.ClientRequestTotal, "endpoint", req.Endpoint(), "status", "success", "code", strconv.Itoa(int(200))).Inc()
	} else {
		sp.SetStatus(tracer.SpanStatusError, err.Error())
		c.opts.Meter.Counter(semconv.ClientRequestTotal, "endpoint", req.Endpoint(), "status", "failure", "code", strconv.Itoa(int(me.Code))).Inc()
	}

	return stream, err
}

func (c *Client) fnStream(ctx context.Context, req client.Request, opts ...client.CallOption) (client.Stream, error) {
	var err error

	// make a copy of call opts
	callOpts := c.opts.CallOptions
	for _, opt := range opts {
		opt(&callOpts)
	}

	// check if we already have a deadline
	d, ok := ctx.Deadline()
	if !ok && callOpts.StreamTimeout > time.Duration(0) {
		var cancel context.CancelFunc
		// no deadline so we create a new one
		ctx, cancel = context.WithTimeout(ctx, callOpts.StreamTimeout)
		defer cancel()
	} else {
		// got a deadline so no need to setup context
		// but we need to set the timeout we pass along
		o := client.WithStreamTimeout(time.Until(d))
		o(&callOpts)
	}

	// should we noop right here?
	select {
	case <-ctx.Done():
		return nil, errors.New("go.micro.client", fmt.Sprintf("%v", ctx.Err()), 408)
	default:
	}

	// use the router passed as a call option, or fallback to the rpc clients router
	if callOpts.Router == nil {
		callOpts.Router = c.opts.Router
	}

	if callOpts.Selector == nil {
		callOpts.Selector = c.opts.Selector
	}

	// inject proxy address
	// TODO: don't even bother using Lookup/Select in this case
	if len(c.opts.Proxy) > 0 {
		callOpts.Address = []string{c.opts.Proxy}
	}

	var next selector.Next

	call := func(i int) (client.Stream, error) {
		// call backoff first. Someone may want an initial start delay
		t, cerr := callOpts.Backoff(ctx, req, i)
		if cerr != nil {
			return nil, errors.InternalServerError("go.micro.client", "%+v", cerr)
		}

		// only sleep if greater than 0
		if t.Seconds() > 0 {
			time.Sleep(t)
		}

		if next == nil {
			var routes []string
			// lookup the route to send the reques to
			// TODO apply any filtering here
			routes, err = c.opts.Lookup(ctx, req, callOpts)
			if err != nil {
				return nil, errors.InternalServerError("go.micro.client", "%+v", err)
			}

			// balance the list of nodes
			next, err = callOpts.Selector.Select(routes)
			if err != nil {
				return nil, err
			}
		}

		node := next()

		// init stream
		stream, cerr := c.stream(ctx, node, req, callOpts)

		// record the result of the call to inform future routing decisions
		if verr := c.opts.Selector.Record(node, cerr); verr != nil {
			return nil, verr
		}

		// try and transform the error to a go-micro error
		if verr, ok := cerr.(*errors.Error); ok {
			return nil, verr
		}

		return stream, cerr
	}

	type response struct {
		stream client.Stream
		err    error
	}

	ch := make(chan response, callOpts.Retries)
	var grr error

	for i := 0; i <= callOpts.Retries; i++ {
		go func() {
			s, cerr := call(i)
			ch <- response{s, cerr}
		}()

		select {
		case <-ctx.Done():
			return nil, errors.New("go.micro.client", fmt.Sprintf("%v", ctx.Err()), 408)
		case rsp := <-ch:
			// if the call succeeded lets bail early
			if rsp.err == nil {
				return rsp.stream, nil
			}

			retry, rerr := callOpts.Retry(ctx, req, i, err)
			if rerr != nil {
				return nil, rerr
			}

			if !retry {
				return nil, rsp.err
			}

			grr = rsp.err
		}
	}

	return nil, grr
}

func (c *Client) stream(ctx context.Context, addr string, req client.Request, opts client.CallOptions) (client.Stream, error) {
	ct := req.ContentType()
	if len(opts.ContentType) > 0 {
		ct = opts.ContentType
	}

	cf, err := c.newCodec(ct)
	if err != nil {
		return nil, errors.BadRequest("go.micro.client", "%+v", err)
	}

	cc, err := (c.httpClient.Transport).(*http.Transport).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, errors.InternalServerError("go.micro.client", "Error dialing: %v", err)
	}

	return &httpStream{
		address: addr,
		logger:  c.opts.Logger,
		context: ctx,
		closed:  make(chan bool),
		opts:    opts,
		conn:    cc,
		ct:      ct,
		cf:      cf,
		reader:  bufio.NewReader(cc),
		request: req,
	}, nil
}

func (c *Client) String() string {
	return "http"
}

func (c *Client) newCodec(ct string) (codec.Codec, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if idx := strings.IndexRune(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}

	if cf, ok := c.opts.Codecs[ct]; ok {
		return cf, nil
	}

	return nil, codec.ErrUnknownContentType
}

func (c *Client) parseRsp(ctx context.Context, hrsp *http.Response, rsp any, opts client.CallOptions) error {
	log := c.opts.Logger

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var buf []byte

	if opts.ResponseMetadata != nil {
		for k, v := range hrsp.Header {
			opts.ResponseMetadata.Set(k, strings.Join(v, ","))
		}
	}

	if hrsp.StatusCode == http.StatusNoContent {
		return nil
	}

	ct := DefaultContentType
	if htype := hrsp.Header.Get(metadata.HeaderContentType); htype != "" {
		ct = htype
	}

	if hrsp.Body != nil {
		var err error
		buf, err = io.ReadAll(hrsp.Body)
		if err != nil {
			return errors.InternalServerError("go.micro.client", "failed to read body: %v", err)
		}
	}

	cf, err := c.newCodec(ct)
	if err != nil {
		return errors.InternalServerError("go.micro.client", "unknown content-type %s: %v", ct, err)
	}

	if log.V(logger.DebugLevel) {
		log.Debug(ctx, fmt.Sprintf("response with headers: %v and body: %s", hrsp.Header, buf))
	}

	if hrsp.StatusCode < http.StatusBadRequest {
		if err = cf.Unmarshal(buf, rsp); err != nil {
			return errors.InternalServerError("go.micro.client", "failed to unmarshal response: %v", err)
		}
		return nil
	}

	var mappedErr any

	errMap, ok := errorMapFromOpts(opts)
	if ok && errMap != nil {
		mappedErr, ok = errMap[fmt.Sprintf("%d", hrsp.StatusCode)]
		if !ok {
			mappedErr, ok = errMap["default"]
		}
	}

	if !ok || mappedErr == nil {
		return errors.New("go.micro.client", string(buf), int32(hrsp.StatusCode))
	}

	if err = cf.Unmarshal(buf, mappedErr); err != nil {
		return errors.InternalServerError("go.micro.client", "failed to unmarshal error: %v", err)
	}

	if v, ok := mappedErr.(error); ok {
		return v
	}

	// if the error map item does not implement the error interface, wrap it
	return &Error{err: mappedErr}
}

func buildHTTPRequest(
	ctx context.Context,
	addr string,
	path string,
	ct string,
	cf codec.Codec,
	msg any,
	opts client.CallOptions,
	log logger.Logger,
) (
	*http.Request,
	error,
) {
	protoMsg, ok := msg.(proto.Message)
	if !ok {
		return nil, errors.BadRequest("go.micro.client", "msg must be a proto message type")
	}

	var (
		method  = http.MethodPost
		bodyOpt = "*"

		parameters = map[string]map[string]string{}
	)

	if opts.Context != nil {
		if v, ok := methodFromOpts(opts); ok {
			method = v
		}
		if v, ok := pathFromOpts(opts); ok {
			path = v
		}
		if v, ok := bodyFromOpts(opts); ok {
			bodyOpt = v
		}
		if h, ok := headerFromOpts(opts); ok && len(h) > 0 {
			m, ok := parameters["header"]
			if !ok {
				m = make(map[string]string)
				parameters["header"] = m
			}
			for idx := 0; idx+1 < len(h); idx += 2 {
				m[h[idx]] = h[idx+1]
			}
		}
		if c, ok := cookieFromOpts(opts); ok && len(c) > 0 {
			m, ok := parameters["cookie"]
			if !ok {
				m = make(map[string]string)
				parameters["cookie"] = m
			}
			for idx := 0; idx+1 < len(c); idx += 2 {
				m[c[idx]] = c[idx+1]
			}
		}
	}

	reqBuilder, err := builder.NewRequestBuilder(path, method, bodyOpt, protoMsg)
	if err != nil {
		return nil, errors.BadRequest("go.micro.client", "new request builder: %+v", err)
	}

	resolvedPath, newMsg, err := reqBuilder.Build()
	if err != nil {
		return nil, errors.BadRequest("go.micro.client", "request build: %+v", err)
	}

	u, err := url.Parse(fmt.Sprintf("%s%s", addr, resolvedPath))
	if err != nil {
		return nil, errors.BadRequest("go.micro.client", "%+v", err)
	}

	reqBody, err := cf.Marshal(newMsg)
	if err != nil {
		return nil, errors.BadRequest("go.micro.client", "%+v", err)
	}

	var hreq *http.Request

	if len(reqBody) > 0 {
		hreq, err = http.NewRequestWithContext(ctx, method, u.String(), io.NopCloser(bytes.NewBuffer(reqBody)))
		hreq.ContentLength = int64(len(reqBody))
	} else {
		hreq, err = http.NewRequestWithContext(ctx, method, u.String(), nil)
	}

	if err != nil {
		return nil, errors.BadRequest("go.micro.client", "%+v", err)
	}

	setHeadersAndCookies(ctx, hreq, ct, opts)
	if err = validateHeadersAndCookies(hreq, parameters); err != nil {
		return nil, errors.BadRequest("go.micro.client", "%+v", err)
	}

	if log.V(logger.DebugLevel) {
		log.Debug(
			ctx,
			fmt.Sprintf("request %s to %s with headers %v body %s", method, u.String(), hreq.Header, reqBody),
		)
	}

	return hreq, nil
}

func setHeadersAndCookies(ctx context.Context, r *http.Request, ct string, opts client.CallOptions) {
	r.Header = make(http.Header)

	r.Header.Set(metadata.HeaderContentType, ct)
	r.Header.Set("Content-Length", fmt.Sprintf("%d", r.ContentLength))

	if opts.AuthToken != "" {
		r.Header.Set(metadata.HeaderAuthorization, opts.AuthToken)
	}

	if opts.StreamTimeout > time.Duration(0) {
		r.Header.Set(metadata.HeaderTimeout, fmt.Sprintf("%d", opts.StreamTimeout))
	}
	if opts.RequestTimeout > time.Duration(0) {
		r.Header.Set(metadata.HeaderTimeout, fmt.Sprintf("%d", opts.RequestTimeout))
	}

	if opts.RequestMetadata != nil {
		for k, v := range opts.RequestMetadata {
			if k == "Cookie" {
				applyCookies(r, v)
				continue
			}
			r.Header[k] = append(r.Header[k], v...)
		}
	}

	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		for k, v := range md {
			if k == "Cookie" {
				applyCookies(r, v)
				continue
			}
			r.Header[k] = append(r.Header[k], v...)
		}
	}
}

func applyCookies(r *http.Request, rawCookies []string) {
	if len(rawCookies) == 0 {
		return
	}

	raw := strings.Join(rawCookies, "; ")

	tmp := http.Request{Header: http.Header{}}
	tmp.Header.Set("Cookie", raw)

	for _, c := range tmp.Cookies() {
		r.AddCookie(c)
	}
}

func validateHeadersAndCookies(r *http.Request, parameters map[string]map[string]string) error {
	if headers, ok := parameters["header"]; ok {
		for name, required := range headers {
			if required == "true" && r.Header.Get(name) == "" {
				return fmt.Errorf("missing required header: %s", name)
			}
		}
	}

	if cookies, ok := parameters["cookie"]; ok {
		cookieMap := map[string]string{}
		for _, c := range r.Cookies() {
			cookieMap[c.Name] = c.Value
		}

		for name, required := range cookies {
			if required == "true" {
				if _, ok := cookieMap[name]; !ok {
					return fmt.Errorf("missing required cookie: %s", name)
				}
			}
		}
	}

	return nil
}
