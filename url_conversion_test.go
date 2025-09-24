package schematicdatastreamws

import (
	"context"
	"testing"
)

func TestConvertAPIURLToWebSocketURL(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "API subdomain with HTTPS",
			input:    "https://api.schematichq.com",
			expected: "wss://datastream.schematichq.com/datastream",
			wantErr:  false,
		},
		{
			name:     "API subdomain with staging",
			input:    "https://api.staging.schematichq.com",
			expected: "wss://datastream.staging.schematichq.com/datastream",
			wantErr:  false,
		},
		{
			name:     "API subdomain with HTTP",
			input:    "http://api.localhost:8080",
			expected: "ws://datastream.localhost:8080/datastream",
			wantErr:  false,
		},
		{
			name:     "Non-API subdomain",
			input:    "https://custom.schematichq.com",
			expected: "wss://custom.schematichq.com/datastream",
			wantErr:  false,
		},
		{
			name:     "No subdomain",
			input:    "https://schematichq.com",
			expected: "wss://schematichq.com/datastream",
			wantErr:  false,
		},
		{
			name:     "Localhost without subdomain",
			input:    "http://localhost:8080",
			expected: "ws://localhost:8080/datastream",
			wantErr:  false,
		},
		{
			name:     "Invalid scheme",
			input:    "ftp://api.example.com",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "Invalid URL",
			input:    "not-a-url",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := convertAPIURLToWebSocketURL(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error for input %q, but got none", tc.input)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error for input %q: %v", tc.input, err)
				return
			}

			if result.String() != tc.expected {
				t.Errorf("For input %q, expected %q, got %q", tc.input, tc.expected, result.String())
			}
		})
	}
}

func TestNewClientWithAPIURL(t *testing.T) {
	testHandler := func(ctx context.Context, message *DataStreamResp) error { return nil }

	testCases := []struct {
		name      string
		options   ClientOptions
		expectURL string
		wantErr   bool
	}{
		{
			name: "HTTP API URL conversion",
			options: ClientOptions{
				URL:            "https://api.schematichq.com",
				ApiKey:         "test-api-key",
				MessageHandler: testHandler,
			},
			expectURL: "wss://datastream.schematichq.com/datastream",
			wantErr:   false,
		},
		{
			name: "WebSocket URL usage",
			options: ClientOptions{
				URL:            "wss://custom.example.com/ws",
				ApiKey:         "test-api-key",
				MessageHandler: testHandler,
			},
			expectURL: "wss://custom.example.com/ws",
			wantErr:   false,
		},
		{
			name: "HTTP URL conversion",
			options: ClientOptions{
				URL:            "http://api.localhost:8080",
				ApiKey:         "test-api-key",
				MessageHandler: testHandler,
			},
			expectURL: "ws://datastream.localhost:8080/datastream",
			wantErr:   false,
		},
		{
			name: "No URL",
			options: ClientOptions{
				ApiKey:         "test-api-key",
				MessageHandler: testHandler,
			},
			wantErr: true,
		},
		{
			name: "No ApiKey",
			options: ClientOptions{
				URL:            "https://api.example.com",
				MessageHandler: testHandler,
			},
			wantErr: true,
		},
		{
			name: "No MessageHandler",
			options: ClientOptions{
				URL:    "https://api.example.com",
				ApiKey: "test-api-key",
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(tc.options)

			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if client.url.String() != tc.expectURL {
				t.Errorf("Expected URL %q, got %q", tc.expectURL, client.url.String())
			}

			// Verify API key is properly set in headers
			if tc.options.ApiKey != "" {
				expectedApiKey := tc.options.ApiKey
				actualApiKey := client.headers.Get("X-Schematic-Api-Key")
				if actualApiKey != expectedApiKey {
					t.Errorf("Expected API key %q in headers, got %q", expectedApiKey, actualApiKey)
				}
			}
		})
	}
}
