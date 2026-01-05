package validator

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// DisposableValidator handles disposable email validation
type DisposableValidator struct {
	disposableDomains  map[string]struct{}
	registrableDomains map[string]struct{}
}

// NewDisposableValidator creates a new instance of DisposableValidator using the config file
func NewDisposableValidator() (*DisposableValidator, error) {
	// Get the project root directory
	projectRoot, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Keep going up until we find the config directory or hit the root
	for {
		if _, err := os.Stat(filepath.Join(projectRoot, "config")); err == nil {
			break
		}
		parent := filepath.Dir(projectRoot)
		if parent == projectRoot {
			return nil, err
		}
		projectRoot = parent
	}

	reader := NewFileDomainReader(filepath.Join(projectRoot, "config", "disposable_domains.txt"))
	return NewDisposableValidatorWithReader(reader)
}

// NewDisposableValidatorWithDomains creates a new instance of DisposableValidator with a custom list of domains
func NewDisposableValidatorWithDomains(domains []string) *DisposableValidator {
	disposableDomains := make(map[string]struct{}, len(domains))
	registrableDomains := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		normalized := normalizeDomain(domain)
		if normalized == "" {
			continue
		}
		disposableDomains[normalized] = struct{}{}
		if registrable, err := publicsuffix.EffectiveTLDPlusOne(normalized); err == nil && registrable == normalized {
			registrableDomains[registrable] = struct{}{}
		}
	}
	return &DisposableValidator{
		disposableDomains:  disposableDomains,
		registrableDomains: registrableDomains,
	}
}

// NewDisposableValidatorWithReader creates a new instance of DisposableValidator using a DomainReader
func NewDisposableValidatorWithReader(reader DomainReader) (*DisposableValidator, error) {
	domains, err := reader.ReadDomains()
	if err != nil {
		return nil, err
	}
	return NewDisposableValidatorWithDomains(domains), nil
}

// Validate checks if the email domain is from a disposable email provider
func (v *DisposableValidator) Validate(domain string) bool {
	normalized := normalizeDomain(domain)
	if normalized == "" {
		return false
	}
	if _, exists := v.disposableDomains[normalized]; exists {
		return true
	}
	registrable, err := publicsuffix.EffectiveTLDPlusOne(normalized)
	if err != nil {
		return false
	}
	_, exists := v.registrableDomains[registrable]
	return exists
}

func normalizeDomain(domain string) string {
	trimmed := strings.TrimSpace(domain)
	trimmed = strings.TrimRight(trimmed, ".")
	if trimmed == "" {
		return ""
	}
	ascii, err := idna.Lookup.ToASCII(trimmed)
	if err != nil {
		return strings.ToLower(trimmed)
	}
	return strings.ToLower(ascii)
}
