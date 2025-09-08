package http_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	jsoncodec "go.unistack.org/micro-codec-json/v4"
	"go.unistack.org/micro/v4/client"
	"go.unistack.org/micro/v4/metadata"
	"google.golang.org/protobuf/proto"

	httpcli "go.unistack.org/micro-client-http/v4"
	pb "go.unistack.org/micro-client-http/v4/builder/proto"
)

func TestClient_Call(t *testing.T) {
	type (
		request      = pb.Test_Client_Call_Request
		response     = pb.Test_Client_Call_Response
		defaultError = pb.Test_Client_Call_DefaultError
		specialError = pb.Test_Client_Call_SpecialError
	)

	httpClient := httpcli.NewClient(
		client.Name("http"),
		client.ContentType("application/json"),
		client.Codec("application/json", jsoncodec.NewCodec()),
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
					printHTTPRequest(r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/user/products", r.URL.RequestURI())

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
					printHTTPRequest(r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/user/products", r.URL.RequestURI())

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
			expectedErr: &defaultError{Code: "default-error-code", Msg: "default-error-msg"},
		},
		{
			name: "special error",
			serverMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					printHTTPRequest(r)

					// Validate request
					require.Equal(t, "POST", r.Method)
					require.Equal(t, "/user/products", r.URL.RequestURI())

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
			expectedErr: &specialError{Code: "special-error-code", Msg: "special-error-msg", Warning: "special-error-warning"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.serverMock()
			defer server.Close()

			var (
				ctx = metadata.NewOutgoingContext(
					context.Background(),
					metadata.Pairs("Authorization", "Bearer token", "My-Header", "My-Header-Value"),
				)
				req = &request{UserId: "user-id-1", OrderId: 123}
				rsp = &response{}
			)

			err := httpClient.Call(
				ctx,
				httpClient.NewRequest("test.service", "Test.Call", req),
				rsp,
				client.WithAddress(server.URL),
				httpcli.Method(http.MethodPost),
				httpcli.Path("/user/products"),
				httpcli.Body("*"),
				httpcli.ErrorMap(map[string]any{
					"default": &pb.Test_Client_Call_DefaultError{},
					"403":     &pb.Test_Client_Call_SpecialError{},
				}),
			)

			if tt.expectedErr != nil {
				fmt.Println(err)
			} else {
				require.NoError(t, err)
				require.True(t, proto.Equal(tt.expectedRsp, rsp))
			}
		})
	}
}
