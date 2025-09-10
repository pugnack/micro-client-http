// Package http provides a http client
package http

import (
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

func (c *Client) Stream(ctx context.Context, req client.Request, opts ...client.CallOption) (client.Stream, error) {
	return c.fnStream(ctx, req, opts...)
}

func (c *Client) String() string {
	return "http"
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

func (c *Client) fnStream(context.Context, client.Request, ...client.CallOption) (client.Stream, error) {
	panic("not implemented")
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
