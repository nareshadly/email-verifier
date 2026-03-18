package validator

import (
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"
)

// SMTPValidationResult represents the result of an SMTP validation check
type SMTPValidationResult struct {
	IsValid        bool
	IsRetryable    bool
	IsCatchAll     bool // Not fully implemented yet, but placeholder
	Status         string
	Error          error
}

// SMTPValidator handles SMTP connection and mailbox verification
type SMTPValidator struct {
	resolver DNSResolver
	timeout  time.Duration
	sender   string // The email address to use in MAIL FROM
	dialer   func(network, address string, timeout time.Duration) (net.Conn, error)
}

// NewSMTPValidator creates a new instance of SMTPValidator
func NewSMTPValidator(resolver DNSResolver) *SMTPValidator {
	return &SMTPValidator{
		resolver: resolver,
		timeout:  10 * time.Second,
		sender:   "validator@example.com", // Default sender, should be configurable
		dialer:   net.DialTimeout,
	}
}

// SetSender sets the sender email address for MAIL FROM command
func (v *SMTPValidator) SetSender(sender string) {
	v.sender = sender
}

// SetTimeout sets the connection timeout
func (v *SMTPValidator) SetTimeout(timeout time.Duration) {
	v.timeout = timeout
}

// SetDialer sets a custom dialer for testing
func (v *SMTPValidator) SetDialer(dialer func(network, address string, timeout time.Duration) (net.Conn, error)) {
	v.dialer = dialer
}

// ValidateMailbox checks if the mailbox exists on the remote server
func (v *SMTPValidator) ValidateMailbox(email string) SMTPValidationResult {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return SMTPValidationResult{
			IsValid: false,
			Error:   fmt.Errorf("invalid email format"),
		}
	}
	domain := parts[1]

	// 1. Resolve MX records
	mxRecords, err := v.resolver.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		return SMTPValidationResult{
			IsValid: false,
			Error:   fmt.Errorf("no MX records found"),
		}
	}

	// Sort by preference
	sort.Slice(mxRecords, func(i, j int) bool {
		return mxRecords[i].Pref < mxRecords[j].Pref
	})

	// Try each MX record until one works or we exhaust the list
	var lastErr error
	for _, mx := range mxRecords {
		result := v.checkMX(mx.Host, email)
		
		// If valid, return success
		if result.IsValid {
			return result
		}

		// If invalid (550), return failure immediately
		if !result.IsValid && !result.IsRetryable && result.Error == nil {
			// Explicit rejection (550)
			return result
		}

		// If retryable (4xx) or connection error, save error and try next MX
		lastErr = result.Error
		if result.IsRetryable {
			// If it's a 4xx error, we might want to return it if all MXs fail with 4xx
			// For now, continue to next MX
		}
	}

	// If all failed with errors or timeouts
	return SMTPValidationResult{
		IsValid:     false,
		IsRetryable: true, // Treat connectivity issues as retryable
		Error:       lastErr,
		Status:      "network_error",
	}
}

func (v *SMTPValidator) checkMX(host string, email string) SMTPValidationResult {
	// Add port 25 if not present (usually MX records are just hostnames)
	address := host + ":25"
	if strings.Contains(host, ":") {
		address = host
	}

	// Connect with timeout
	conn, err := v.dialer("tcp", address, v.timeout)
	if err != nil {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: true,
			Error:       err,
			Status:      "connection_failed",
		}
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: true,
			Error:       err,
			Status:      "smtp_client_failed",
		}
	}
	defer client.Quit()

	// HELO/EHLO
	// We should use a valid hostname here. For now "localhost" or verify with user.
	// Using "validator.local" or similar.
	if err := client.Hello("validator.local"); err != nil {
		return v.handleSMTPError(err)
	}

	// MAIL FROM
	if err := client.Mail(v.sender); err != nil {
		return v.handleSMTPError(err)
	}

	// RCPT TO
	if err := client.Rcpt(email); err != nil {
		return v.handleSMTPError(err)
	}

	// If we got here, it's a 250 OK
	return SMTPValidationResult{
		IsValid: true,
		Status:  "valid",
	}
}

func (v *SMTPValidator) handleSMTPError(err error) SMTPValidationResult {
	if err == nil {
		return SMTPValidationResult{IsValid: true}
	}
	
	// Check for textproto.Error (SMTP error)
	// Currently net/smtp wraps errors, but we can check the error string
	// or unwrap if it was exposed. `net/smtp` returns `textproto.Error` which has Code.
	// But `net/smtp` doesn't export `textproto`.
	// Actually `net/smtp` functions return `error` interface.
	// We can check the error message for codes.

	// Ideally we would type assert to *textproto.Error, but textproto is in net/textproto.
	// Let's assume we can string match for now or import net/textproto.
	
	errMsg := err.Error()
	
	// 550 User unknown
	if strings.Contains(errMsg, "550") {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: false,
			Status:      "mailbox_not_found",
			Error:       nil, // Not an error in the sense of failure, but a definitive result
		}
	}

	// 551 User not local
	if strings.Contains(errMsg, "551") {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: false, // Or maybe risky?
			Status:      "user_not_local",
		}
	}

	// 452 Mailbox full
	if strings.Contains(errMsg, "452") {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: true,
			Status:      "mailbox_full",
			Error:       err,
		}
	}

	// 421 Service not available
	if strings.Contains(errMsg, "421") {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: true,
			Status:      "service_unavailable",
			Error:       err,
		}
	}

	// 450, 451 - other temp errors
	if strings.HasPrefix(errMsg, "4") {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: true,
			Status:      "temporary_error",
			Error:       err,
		}
	}

	// 5xx - other permanent errors
	if strings.HasPrefix(errMsg, "5") {
		return SMTPValidationResult{
			IsValid:     false,
			IsRetryable: false,
			Status:      "permanent_error",
			Error:       err,
		}
	}

	// Other errors (network, etc)
	return SMTPValidationResult{
		IsValid:     false,
		IsRetryable: true,
		Status:      "unknown_error",
		Error:       err,
	}
}
