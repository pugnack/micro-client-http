package http

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "host with port",
			input:   "localhost:8080",
			want:    "http://localhost:8080",
			wantErr: false,
		},
		{
			name:    "http with host",
			input:   "http://example.com",
			want:    "http://example.com",
			wantErr: false,
		},
		{
			name:    "http with no host",
			input:   "http://",
			want:    "",
			wantErr: true,
		},
		{
			name:    "https with host",
			input:   "https://example.com",
			want:    "https://example.com",
			wantErr: false,
		},
		{
			name:    "https with no host",
			input:   "https://",
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid scheme",
			input:   "ftp://example.com",
			want:    "",
			wantErr: true,
		},
		{
			name:    "IPv4 without scheme",
			input:   "127.0.0.1:9000",
			want:    "http://127.0.0.1:9000",
			wantErr: false,
		},
		{
			name:    "IPv4 with scheme",
			input:   "http://127.0.0.1:8080",
			want:    "http://127.0.0.1:8080",
			wantErr: false,
		},
		{
			name:    "IPv6 without scheme",
			input:   "[::1]:8080",
			want:    "http://[::1]:8080",
			wantErr: false,
		},
		{
			name:    "IPv6 with scheme",
			input:   "https://[::1]:443",
			want:    "https://[::1]:443",
			wantErr: false,
		},
		{
			name:    "hostname only",
			input:   "my-service",
			want:    "http://my-service",
			wantErr: false,
		},
		{
			name:    "hostname with path",
			input:   "service.local/api/v1",
			want:    "http://service.local/api/v1",
			wantErr: false,
		},
		{
			name:    "hostname with dash and port",
			input:   "api-service.local:8080",
			want:    "http://api-service.local:8080",
			wantErr: false,
		},
		{
			name:    "just path",
			input:   "/api/v1",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "http with query params",
			input:   "http://example.com?x=1&y=2",
			want:    "http://example.com?x=1&y=2",
			wantErr: false,
		},
		{
			name:    "http with fragment",
			input:   "http://example.com/path#section1",
			want:    "http://example.com/path#section1",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalizeURL(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, result.String())
			}
		})
	}
}
