package http_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	jsoncodec "go.unistack.org/micro-codec-json/v4"
	"go.unistack.org/micro/v4/client"
	"go.unistack.org/micro/v4/metadata"
	"google.golang.org/protobuf/proto"

	httpcli "go.unistack.org/micro-client-http/v4"
	pb "go.unistack.org/micro-client-http/v4/builder/proto"
)

func printHTTPRequest(t *testing.T, r *http.Request) {
	t.Helper()
	output, err := httputil.DumpRequest(r, true)
	require.NoError(t, err)
	log.Printf("\n%s\n", output)
}

func TestClient_Call_Get(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	tests := []struct {
		name        string
		method      string
		path        string
		req         *request
		options     []client.CallOption
		wantPath    string
		wantReqBody []byte
		wantRsp     *response
		wantErr     bool
	}{
		{
			name:        "GET request (query)",
			method:      http.MethodGet,
			path:        "/user/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/products?order_id=456&user_id=123",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "GET request (path)",
			method:      http.MethodGet,
			path:        "/user/{user_id}/order/{order_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/123/order/456/products",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "GET request (path + query)",
			method:      http.MethodGet,
			path:        "/user/{user_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/123/products?order_id=456",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "GET request (zero-value query)",
			method:      http.MethodGet,
			path:        "/user/products",
			req:         &request{},
			wantPath:    "/test/call/user/products",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:    "GET request (zero-value path)",
			method:  http.MethodGet,
			path:    "/user/{user_id}/products",
			req:     &request{OrderId: 456},
			wantRsp: nil,
			wantErr: true,
		},
		{
			name:    "GET request (with body)",
			method:  http.MethodGet,
			path:    "/user/products",
			req:     &request{UserId: "123", OrderId: 456},
			options: []client.CallOption{httpcli.Body("*")},
			wantRsp: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tt.method, r.Method)
				require.Equal(t, tt.wantPath, r.URL.RequestURI())

				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
				require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

				buf, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				defer r.Body.Close()
				require.Equal(t, tt.wantReqBody, buf)

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("My-Header", "My-Header-Value")
				w.WriteHeader(http.StatusOK)

				if tt.wantRsp != nil {
					c := jsoncodec.NewCodec()
					buf, err = c.Marshal(tt.wantRsp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}
			}))
			defer server.Close()

			httpClient := httpcli.NewClient(
				client.Codec("application/json", jsoncodec.NewCodec()),
			)

			var (
				ctx = metadata.NewOutgoingContext(
					context.Background(),
					metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value"),
				)
				rsp          = &response{}
				respMetadata = metadata.Metadata{}
			)

			opts := []client.CallOption{
				client.WithAddress(server.URL),
				client.WithResponseMetadata(&respMetadata),
				httpcli.Method(tt.method),
				httpcli.Path(tt.path),
			}

			if len(tt.options) > 0 {
				opts = append(opts, tt.options...)
			}

			err := httpClient.Call(
				ctx,
				httpClient.NewRequest("test.service", "/test/call", tt.req),
				rsp,
				opts...,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, rsp)
			} else {
				require.NoError(t, err)
				require.True(t, proto.Equal(tt.wantRsp, rsp))
				require.Equal(t, "application/json", respMetadata.GetJoined("Content-Type"))
				require.Equal(t, "My-Header-Value", respMetadata.GetJoined("My-Header"))
			}
		})
	}
}

func TestClient_Call_Head(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	tests := []struct {
		name        string
		method      string
		path        string
		req         *request
		options     []client.CallOption
		wantPath    string
		wantReqBody []byte
		wantRsp     *response
		wantErr     bool
	}{
		{
			name:        "HEAD request (query)",
			method:      http.MethodHead,
			path:        "/user/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/products?order_id=456&user_id=123",
			wantReqBody: []byte{},
			wantRsp:     &response{},
			wantErr:     false,
		},
		{
			name:        "HEAD request (path)",
			method:      http.MethodHead,
			path:        "/user/{user_id}/order/{order_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/123/order/456/products",
			wantReqBody: []byte{},
			wantRsp:     &response{},
			wantErr:     false,
		},
		{
			name:        "HEAD request (path + query)",
			method:      http.MethodHead,
			path:        "/user/{user_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/123/products?order_id=456",
			wantReqBody: []byte{},
			wantRsp:     &response{},
			wantErr:     false,
		},
		{
			name:        "HEAD request (zero-value query)",
			method:      http.MethodHead,
			path:        "/user/products",
			req:         &request{},
			wantPath:    "/test/call/user/products",
			wantReqBody: []byte{},
			wantRsp:     &response{},
			wantErr:     false,
		},
		{
			name:    "HEAD request (zero-value path)",
			method:  http.MethodHead,
			path:    "/user/{user_id}/products",
			req:     &request{OrderId: 456},
			wantRsp: nil,
			wantErr: true,
		},
		{
			name:    "HEAD request (with body)",
			method:  http.MethodHead,
			path:    "/user/products",
			req:     &request{UserId: "123", OrderId: 456},
			options: []client.CallOption{httpcli.Body("*")},
			wantRsp: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tt.method, r.Method)
				require.Equal(t, tt.wantPath, r.URL.RequestURI())

				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
				require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

				buf, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				defer r.Body.Close()
				require.Equal(t, tt.wantReqBody, buf)

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("My-Header", "My-Header-Value")
				w.WriteHeader(http.StatusOK)

				// used to verify that the HTTP client skips the response body for HEAD method
				c := jsoncodec.NewCodec()
				buf, err = c.Marshal(map[string]any{"id": "product-id", "name": "product-name"})
				require.NoError(t, err)
				_, err = w.Write(buf)
				require.NoError(t, err)
			}))
			defer server.Close()

			httpClient := httpcli.NewClient(
				client.Codec("application/json", jsoncodec.NewCodec()),
			)

			var (
				ctx = metadata.NewOutgoingContext(
					context.Background(),
					metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value"),
				)
				rsp          = &response{}
				respMetadata = metadata.Metadata{}
			)

			opts := []client.CallOption{
				client.WithAddress(server.URL),
				client.WithResponseMetadata(&respMetadata),
				httpcli.Method(tt.method),
				httpcli.Path(tt.path),
			}

			if len(tt.options) > 0 {
				opts = append(opts, tt.options...)
			}

			err := httpClient.Call(
				ctx,
				httpClient.NewRequest("test.service", "/test/call", tt.req),
				rsp,
				opts...,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, rsp)
			} else {
				require.NoError(t, err)
				require.True(t, proto.Equal(tt.wantRsp, rsp))
				require.Equal(t, "application/json", respMetadata.GetJoined("Content-Type"))
				require.Equal(t, "My-Header-Value", respMetadata.GetJoined("My-Header"))
			}
		})
	}
}

func TestClient_Call_Post(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	tests := []struct {
		name        string
		method      string
		path        string
		req         *request
		options     []client.CallOption
		wantPath    string
		wantReqBody []byte
		wantRsp     *response
		wantErr     bool
	}{
		{
			name:        "POST request (query)",
			method:      http.MethodPost,
			path:        "/user/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/products?order_id=456&user_id=123",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (path)",
			method:      http.MethodPost,
			path:        "/user/{user_id}/order/{order_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/123/order/456/products",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (body)",
			method:      http.MethodPost,
			path:        "/user/products",
			req:         &request{UserId: "123", OrderId: 456},
			options:     []client.CallOption{httpcli.Body("*")},
			wantPath:    "/test/call/user/products",
			wantReqBody: []byte(`{"userId":"123","orderId":456}`),
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (path + query)",
			method:      http.MethodPost,
			path:        "/user/{user_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			wantPath:    "/test/call/user/123/products?order_id=456",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (path + body)",
			method:      http.MethodPost,
			path:        "/user/{user_id}/products",
			req:         &request{UserId: "123", OrderId: 456},
			options:     []client.CallOption{httpcli.Body("*")},
			wantPath:    "/test/call/user/123/products",
			wantReqBody: []byte(`{"orderId":456}`),
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (query + body)",
			method:      http.MethodPost,
			path:        "/user/products",
			req:         &request{UserId: "123", OrderId: 456},
			options:     []client.CallOption{httpcli.Body("order_id")},
			wantPath:    "/test/call/user/products?user_id=123",
			wantReqBody: []byte(`{"orderId":456}`),
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (zero-value query)",
			method:      http.MethodPost,
			path:        "/user/products",
			req:         &request{},
			wantPath:    "/test/call/user/products",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:        "POST request (zero-value body)",
			method:      http.MethodPost,
			path:        "/user/products",
			req:         &request{},
			options:     []client.CallOption{httpcli.Body("*")},
			wantPath:    "/test/call/user/products",
			wantReqBody: []byte{},
			wantRsp:     &response{Id: "product-id", Name: "product-name"},
			wantErr:     false,
		},
		{
			name:    "POST request (zero-value path)",
			method:  http.MethodPost,
			path:    "/user/{user_id}/products",
			req:     &request{OrderId: 456},
			wantRsp: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tt.method, r.Method)
				require.Equal(t, tt.wantPath, r.URL.RequestURI())

				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
				require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

				buf, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				defer r.Body.Close()
				require.Equal(t, tt.wantReqBody, buf)

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("My-Header", "My-Header-Value")
				w.WriteHeader(http.StatusOK)

				if tt.wantRsp != nil {
					c := jsoncodec.NewCodec()
					buf, err = c.Marshal(tt.wantRsp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}
			}))
			defer server.Close()

			httpClient := httpcli.NewClient(
				client.Codec("application/json", jsoncodec.NewCodec()),
			)

			var (
				ctx = metadata.NewOutgoingContext(
					context.Background(),
					metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value"),
				)
				rsp          = &response{}
				respMetadata = metadata.Metadata{}
			)

			opts := []client.CallOption{
				client.WithAddress(server.URL),
				client.WithResponseMetadata(&respMetadata),
				httpcli.Method(tt.method),
				httpcli.Path(tt.path),
			}

			if len(tt.options) > 0 {
				opts = append(opts, tt.options...)
			}

			err := httpClient.Call(
				ctx,
				httpClient.NewRequest("test.service", "/test/call", tt.req),
				rsp,
				opts...,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, rsp)
			} else {
				require.NoError(t, err)
				require.True(t, proto.Equal(tt.wantRsp, rsp))
				require.Equal(t, "application/json", respMetadata.GetJoined("Content-Type"))
				require.Equal(t, "My-Header-Value", respMetadata.GetJoined("My-Header"))
			}
		})
	}
}

func TestClient_Call_SuccessAndErrorsMap(t *testing.T) {
	type (
		request      = pb.Test_Client_Call_Request
		response     = pb.Test_Client_Call_Response
		defaultError = pb.Test_Client_Call_DefaultError
		specialError = pb.Test_Client_Call_SpecialError
	)

	tests := []struct {
		name        string
		serverMock  func() *httptest.Server
		expectedRsp *response
		expectedErr error
	}{
		{
			name: "success",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
					require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusOK)

					resp := map[string]interface{}{
						"id":   "product-id-1",
						"name": "product-name-1",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			expectedRsp: &response{Id: "product-id-1", Name: "product-name-1"},
		},
		{
			name: "default error",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
					require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusBadRequest)

					resp := map[string]interface{}{
						"code": "default-error-code",
						"msg":  "default-error-message",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			expectedErr: &defaultError{Code: "default-error-code", Msg: "default-error-message"},
		},
		{
			name: "special error",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
					require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusForbidden)

					resp := map[string]interface{}{
						"code":    "special-error-code",
						"msg":     "special-error-message",
						"warning": "special-error-warning",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			expectedErr: &specialError{Code: "special-error-code", Msg: "special-error-message", Warning: "special-error-warning"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.serverMock()
			defer server.Close()

			httpClient := httpcli.NewClient(
				client.Codec("application/json", jsoncodec.NewCodec()),
			)

			var (
				ctx = metadata.NewOutgoingContext(
					context.Background(),
					metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value"),
				)
				req = &request{UserId: "user-id-1", OrderId: 123}
				rsp = &response{}

				respMetadata = metadata.Metadata{}
			)

			opts := []client.CallOption{
				client.WithAddress(server.URL),
				client.WithResponseMetadata(&respMetadata),
				httpcli.Method(http.MethodPost),
				httpcli.Path("/user/products"),
				httpcli.Body("*"),
				httpcli.ErrorMap(map[string]any{
					"default": &defaultError{},
					"403":     &specialError{},
				}),
			}

			err := httpClient.Call(
				ctx,
				httpClient.NewRequest("test.service", "/test/call", req),
				rsp,
				opts...,
			)

			if tt.expectedErr != nil {
				require.Equal(t, tt.expectedErr.Error(), err.Error())
				require.Empty(t, rsp)
			} else {
				require.NoError(t, err)
				require.True(t, proto.Equal(tt.expectedRsp, rsp))
			}

			require.Equal(t, "application/json", respMetadata.GetJoined("Content-Type"))
			require.Equal(t, "My-Header-Value", respMetadata.GetJoined("My-Header"))
		})
	}
}

func TestClient_Call_HeadersAndCookies(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	tests := []struct {
		name            string
		serverMock      func() *httptest.Server
		prepareMetadata func() metadata.Metadata
		headersOption   []string
		cookiesOption   []string
		expectedRsp     *response
		wantErr         bool
	}{
		{
			name: "with required headers",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
					require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusOK)

					resp := map[string]interface{}{
						"id":   "product-id-1",
						"name": "product-name-1",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			prepareMetadata: func() metadata.Metadata {
				return metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value")
			},
			headersOption: []string{"Authorization", "true", "My-Header", "true"},
			expectedRsp:   &response{Id: "product-id-1", Name: "product-name-1"},
		},
		{
			name: "without required headers",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
					require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusOK)

					resp := map[string]interface{}{
						"id":   "product-id-1",
						"name": "product-name-1",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			prepareMetadata: func() metadata.Metadata {
				return metadata.Pairs("Authorization", "Bearer token")
			},
			headersOption: []string{"Authorization", "true", "My-Header", "true"},
			wantErr:       true,
		},
		{
			name: "with required cookies",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "session_id=abc123; theme=dark", r.Header.Get("Cookie"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusOK)

					resp := map[string]interface{}{
						"id":   "product-id-1",
						"name": "product-name-1",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			prepareMetadata: func() metadata.Metadata {
				return metadata.Pairs("Cookie", "session_id=abc123; theme=dark")
			},
			cookiesOption: []string{"session_id", "true", "theme", "true"},
			expectedRsp:   &response{Id: "product-id-1", Name: "product-name-1"},
		},
		{
			name: "without required cookies",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "session_id=abc123; theme=dark", r.Header.Get("Cookie"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusOK)

					resp := map[string]interface{}{
						"id":   "product-id-1",
						"name": "product-name-1",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			prepareMetadata: func() metadata.Metadata {
				return metadata.Pairs("Cookie", "session_id=abc123")
			},
			cookiesOption: []string{"session_id", "true", "theme", "true"},
			wantErr:       true,
		},
		{
			name: "with headers and cookies",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(t, r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

					require.Equal(t, "application/json", r.Header.Get("Content-Type"))
					require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
					require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))
					require.Equal(t, "session_id=abc123; theme=dark", r.Header.Get("Cookie"))

					buf, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					defer r.Body.Close()

					c := jsoncodec.NewCodec()

					req := &request{}
					err = c.Unmarshal(buf, req)
					require.NoError(t, err)
					require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

					// Return response
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("My-Header", "My-Header-Value")
					w.WriteHeader(http.StatusOK)

					resp := map[string]interface{}{
						"id":   "product-id-1",
						"name": "product-name-1",
					}
					buf, err = c.Marshal(resp)
					require.NoError(t, err)
					_, err = w.Write(buf)
					require.NoError(t, err)
				}))
			},
			prepareMetadata: func() metadata.Metadata {
				return metadata.Pairs(
					"Authorization", "Bearer token",
					"My-Header", "My-Header-Value",
					"Cookie", "session_id=abc123; theme=dark",
				)
			},
			headersOption: []string{"Authorization", "true", "My-Header", "true"},
			cookiesOption: []string{"session_id", "true", "theme", "true"},
			expectedRsp:   &response{Id: "product-id-1", Name: "product-name-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.serverMock()
			defer server.Close()

			httpClient := httpcli.NewClient(
				client.Codec("application/json", jsoncodec.NewCodec()),
			)

			var (
				ctx = metadata.NewOutgoingContext(context.Background(), tt.prepareMetadata())
				req = &request{UserId: "user-id-1", OrderId: 123}
				rsp = &response{}

				respMetadata = metadata.Metadata{}
			)

			opts := []client.CallOption{
				client.WithAddress(server.URL),
				client.WithResponseMetadata(&respMetadata),
				httpcli.Method(http.MethodPost),
				httpcli.Path("/user/products"),
				httpcli.Body("*"),
			}
			if len(tt.headersOption) != 0 {
				opts = append(opts, httpcli.Header(tt.headersOption...))
			}
			if len(tt.cookiesOption) != 0 {
				opts = append(opts, httpcli.Cookie(tt.cookiesOption...))
			}

			err := httpClient.Call(
				ctx,
				httpClient.NewRequest("test.service", "/test/call", req),
				rsp,
				opts...,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, rsp)
			} else {
				require.NoError(t, err)
				require.True(t, proto.Equal(tt.expectedRsp, rsp))
				require.Equal(t, "application/json", respMetadata.GetJoined("Content-Type"))
				require.Equal(t, "My-Header-Value", respMetadata.GetJoined("My-Header"))
			}
		})
	}
}

func TestClient_Call_SuccessNoContent(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	serverMock := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			printHTTPRequest(t, r)

			// Validate request
			require.Equal(t, "POST", r.Method)
			require.Equal(t, "/test/call/user/products", r.URL.RequestURI())

			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
			require.Equal(t, "My-Header-Value", r.Header.Get("My-Header"))

			buf, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			defer r.Body.Close()

			c := jsoncodec.NewCodec()

			req := &request{}
			err = c.Unmarshal(buf, req)
			require.NoError(t, err)
			require.True(t, proto.Equal(&request{UserId: "user-id-1", OrderId: 123}, req))

			// Return response
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("My-Header", "My-Header-Value")
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	server := serverMock()
	defer server.Close()

	httpClient := httpcli.NewClient(
		client.Codec("application/json", jsoncodec.NewCodec()),
	)

	var (
		ctx = metadata.NewOutgoingContext(
			context.Background(),
			metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value"),
		)
		req = &request{UserId: "user-id-1", OrderId: 123}
		rsp = &response{}

		respMetadata = metadata.Metadata{}
	)

	opts := []client.CallOption{
		client.WithAddress(server.URL),
		client.WithResponseMetadata(&respMetadata),
		httpcli.Method(http.MethodPost),
		httpcli.Path("/user/products"),
		httpcli.Body("*"),
	}

	err := httpClient.Call(
		ctx,
		httpClient.NewRequest("test.service", "/test/call", req),
		rsp,
		opts...,
	)
	require.NoError(t, err)
	require.Empty(t, rsp)

	require.Equal(t, "application/json", respMetadata.GetJoined("Content-Type"))
	require.Equal(t, "My-Header-Value", respMetadata.GetJoined("My-Header"))
}

func TestClient_Call_RequestTimeoutError(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	serverMock := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Millisecond)
		}))
	}

	server := serverMock()
	defer server.Close()

	httpClient := httpcli.NewClient(
		client.Codec("application/json", jsoncodec.NewCodec()),
	)

	var (
		ctx = context.Background()
		req = &request{UserId: "user-id-1", OrderId: 123}
		rsp = &response{}
	)

	opts := []client.CallOption{
		client.WithAddress(server.URL),
		client.WithRequestTimeout(time.Millisecond),
		httpcli.Method(http.MethodPost),
		httpcli.Path("/user/products"),
		httpcli.Body("*"),
	}

	err := httpClient.Call(
		ctx,
		httpClient.NewRequest("test.service", "/test/call", req),
		rsp,
		opts...,
	)
	require.Error(t, err)
}

func TestClient_Call_ContextDeadlineError(t *testing.T) {
	type (
		request  = pb.Test_Client_Call_Request
		response = pb.Test_Client_Call_Response
	)

	serverMock := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Millisecond)
		}))
	}

	server := serverMock()
	defer server.Close()

	httpClient := httpcli.NewClient(
		client.Codec("application/json", jsoncodec.NewCodec()),
	)

	var (
		ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(time.Millisecond))
		req         = &request{UserId: "user-id-1", OrderId: 123}
		rsp         = &response{}
	)
	defer cancel()

	opts := []client.CallOption{
		client.WithAddress(server.URL),
		httpcli.Method(http.MethodPost),
		httpcli.Path("/user/products"),
		httpcli.Body("*"),
	}

	err := httpClient.Call(
		ctx,
		httpClient.NewRequest("test.service", "/test/call", req),
		rsp,
		opts...,
	)
	require.Error(t, err)
}
