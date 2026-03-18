package validatortest

import (
	"net"
	"testing"
	"time"

	"emailvalidator/pkg/validator"
)

// MockDNSResolver
type MockDNSResolver struct {
	MXRecords []*net.MX
	Err       error
}

func (m *MockDNSResolver) LookupHost(domain string) ([]string, error) {
	return []string{"127.0.0.1"}, nil
}

func (m *MockDNSResolver) LookupMX(domain string) ([]*net.MX, error) {
	return m.MXRecords, m.Err
}

// MockConn
type MockConn struct {
	net.Conn
	ReadBuf  []byte
	WriteBuf []byte
}

func (m *MockConn) Read(b []byte) (n int, err error) {
	if len(m.ReadBuf) == 0 {
		return 0, nil
	}
	n = copy(b, m.ReadBuf)
	m.ReadBuf = m.ReadBuf[n:]
	return n, nil
}

func (m *MockConn) Write(b []byte) (n int, err error) {
	m.WriteBuf = append(m.WriteBuf, b...)
	return len(b), nil
}

func (m *MockConn) Close() error {
	return nil
}

func (m *MockConn) SetDeadline(t time.Time) error {
	return nil
}

func TestSMTPValidator_ValidateMailbox(t *testing.T) {
	tests := []struct {
		name          string
		email         string
		mxRecords     []*net.MX
		mxErr         error
		dialErr       error
		serverResponses []string // Responses to send for each command (Connect, HELO, MAIL, RCPT)
		wantIsValid   bool
		wantRetryable bool
		wantStatus    string
	}{
		{
			name:      "Valid mailbox",
			email:     "test@example.com",
			mxRecords: []*net.MX{{Host: "mx.example.com", Pref: 10}},
			serverResponses: []string{
				"220 mx.example.com ESMTP Service Ready\r\n",
				"250 Hello\r\n",
				"250 OK\r\n",
				"250 OK\r\n",
			},
			wantIsValid: true,
			wantStatus:  "valid",
		},
		{
			name:      "Invalid mailbox (550)",
			email:     "invalid@example.com",
			mxRecords: []*net.MX{{Host: "mx.example.com", Pref: 10}},
			serverResponses: []string{
				"220 mx.example.com ESMTP Service Ready\r\n",
				"250 Hello\r\n",
				"250 OK\r\n",
				"550 User unknown\r\n",
			},
			wantIsValid:   false,
			wantRetryable: false,
			wantStatus:    "mailbox_not_found",
		},
		{
			name:      "No MX records",
			email:     "test@nomx.com",
			mxRecords: []*net.MX{},
			mxErr:     nil, // Empty list
			wantIsValid: false,
			wantStatus:  "network_error", // Or whatever "no MX records found" maps to in error handling logic? 
			// Wait, the code returns `Error: fmt.Errorf("no MX records found")` but doesn't set status field in the first return.
			// Actually, ValidateMailbox returns result with Error set.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &MockDNSResolver{
				MXRecords: tt.mxRecords,
				Err:       tt.mxErr,
			}
			v := validator.NewSMTPValidator(resolver)

			// Mock Dialer
			v.SetDialer(func(network, address string, timeout time.Duration) (net.Conn, error) {
				if tt.dialErr != nil {
					return nil, tt.dialErr
				}
				// Construct a mock connection that replays serverResponses
				// We need a more sophisticated mock conn that acts like a server
				// But net/smtp.NewClient does a lot of reading.
				// Simplest way is to use net.Pipe and have a goroutine act as server.
				client, server := net.Pipe()
				
				go func() {
					defer server.Close()
					// Write initial greeting
					if len(tt.serverResponses) > 0 {
						server.Write([]byte(tt.serverResponses[0]))
					}
					
					// Read loop and respond
					buf := make([]byte, 1024)
					responseIdx := 1
					for {
						n, err := server.Read(buf)
						if err != nil {
							return
						}
						_ = n
						// cmd := string(buf[:n])
						// Determine response based on command or just sequence
						if responseIdx < len(tt.serverResponses) {
							server.Write([]byte(tt.serverResponses[responseIdx]))
							responseIdx++
						}
					}
				}()
				
				return client, nil
			})

			result := v.ValidateMailbox(tt.email)

			if tt.name == "No MX records" {
				if result.Error == nil || result.Error.Error() != "no MX records found" {
					t.Errorf("expected error 'no MX records found', got %v", result.Error)
				}
				return
			}

			if result.IsValid != tt.wantIsValid {
				t.Errorf("got IsValid %v, want %v", result.IsValid, tt.wantIsValid)
			}
			if result.IsRetryable != tt.wantRetryable {
				t.Errorf("got IsRetryable %v, want %v", result.IsRetryable, tt.wantRetryable)
			}
			if result.Status != tt.wantStatus {
				t.Errorf("got Status %q, want %q", result.Status, tt.wantStatus)
			}
		})
	}
}
