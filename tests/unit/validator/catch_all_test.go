package validatortest

import (
	"net"
	"strings"
	"testing"
	"time"

	"emailvalidator/pkg/validator"
)

// Reusing MockDNSResolver from smtp_validator_test.go (same package)

func TestSMTPValidator_CheckCatchAll(t *testing.T) {
	tests := []struct {
		name          string
		domain        string
		mxRecords     []*net.MX
		isCatchAll    bool // What the server behavior should simulate
		wantCatchAll  bool
		wantError     bool
	}{
		{
			name:   "Catch-all domain",
			domain: "catchall.com",
			mxRecords: []*net.MX{{Host: "mx.catchall.com", Pref: 10}},
			isCatchAll: true, // Server accepts any RCPT TO
			wantCatchAll: true,
			wantError:    false,
		},
		{
			name:   "Non-catch-all domain",
			domain: "normal.com",
			mxRecords: []*net.MX{{Host: "mx.normal.com", Pref: 10}},
			isCatchAll: false, // Server rejects unknown RCPT TO
			wantCatchAll: false,
			wantError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &MockDNSResolver{
				MXRecords: tt.mxRecords,
				Err:       nil,
			}
			v := validator.NewSMTPValidator(resolver)

			// Mock Dialer
			v.SetDialer(func(network, address string, timeout time.Duration) (net.Conn, error) {
				client, server := net.Pipe()
				
				go func() {
					defer server.Close()
					
					// 1. Send Greeting
					server.Write([]byte("220 mx.example.com ESMTP Service Ready\r\n"))

					buf := make([]byte, 1024)
					for {
						n, err := server.Read(buf)
						if err != nil {
							return
						}
						cmd := string(buf[:n])
						
						// Simple state machine or just sequential responses based on command
						if strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO") {
							server.Write([]byte("250 Hello\r\n"))
						} else if strings.HasPrefix(cmd, "MAIL FROM:") {
							server.Write([]byte("250 OK\r\n"))
						} else if strings.HasPrefix(cmd, "RCPT TO:") {
							// Check if this is the random probe
							// Since we don't know the exact random string, we assume any RCPT TO in this test is the probe.
							if tt.isCatchAll {
								server.Write([]byte("250 OK\r\n"))
							} else {
								server.Write([]byte("550 User unknown\r\n"))
							}
						} else if strings.HasPrefix(cmd, "QUIT") {
							server.Write([]byte("221 Bye\r\n"))
							return
						}
					}
				}()
				
				return client, nil
			})

			got, err := v.CheckCatchAll(tt.domain)
			if (err != nil) != tt.wantError {
				t.Errorf("CheckCatchAll() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if got != tt.wantCatchAll {
				t.Errorf("CheckCatchAll() = %v, want %v", got, tt.wantCatchAll)
			}
		})
	}
}
