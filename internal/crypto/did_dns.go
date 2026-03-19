// Package crypto provides did:dns DID method support.
//
// did:dns uses DNS TXT records to store DID information.
//
//	did:dns:example.com
//
// Resolution queries DNS TXT records at _did.<domain> and parses did:key URIs
// from the record values. This provides a lightweight, DNS-based alternative to
// did:web for associating domain names with agent identities.
//
// DNS record format (TXT at _did.example.com):
//
//	"did=did:key:z6Mkf5rGMoatrSj1f4CyvuHBeXJELe9RPdzo2PKGNCKVtZxP"
//
// Multiple records may exist to associate multiple agent identities with a domain.
package crypto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Errors for did:dns operations.
var (
	ErrInvalidDIDDNS = errors.New("invalid did:dns format")
	ErrDIDDNSResolve = errors.New("did:dns resolution failed")
)

// DIDDNSEntry represents a single identity extracted from a did:dns record.
type DIDDNSEntry struct {
	DIDKey string // The did:key URI found in the DNS record.
	PeerID string // The libp2p PeerID derived from the did:key, if convertible.
}

// dnsResolver abstracts DNS lookups for testability.
type dnsResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// netResolver wraps net.Resolver to implement dnsResolver.
type netResolver struct {
	r *net.Resolver
}

func (n *netResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return n.r.LookupTXT(ctx, name)
}

// defaultDNSResolver is the production DNS resolver.
var defaultDNSResolver dnsResolver = &netResolver{r: net.DefaultResolver}

// ResolveDIDDNS resolves a did:dns identifier by querying DNS TXT records
// at _did.<domain> and extracting did:key URIs.
func ResolveDIDDNS(ctx context.Context, didDNS string) ([]DIDDNSEntry, error) {
	return resolveDIDDNSWithResolver(ctx, didDNS, defaultDNSResolver)
}

// resolveDIDDNSWithResolver allows injecting a custom resolver for testing.
func resolveDIDDNSWithResolver(ctx context.Context, didDNS string, resolver dnsResolver) ([]DIDDNSEntry, error) {
	domain, err := parseDIDDNSDomain(didDNS)
	if err != nil {
		return nil, err
	}

	queryName := "_did." + domain

	records, err := resolver.LookupTXT(ctx, queryName)
	if err != nil {
		return nil, fmt.Errorf("%w: lookup TXT %s: %v", ErrDIDDNSResolve, queryName, err)
	}

	var entries []DIDDNSEntry
	for _, record := range records {
		didKey := extractDIDKeyFromTXT(record)
		if didKey == "" {
			continue
		}

		entry := DIDDNSEntry{DIDKey: didKey}

		// Attempt to convert to PeerID (best-effort).
		pid, err := DIDKeyToPeerID(didKey)
		if err == nil {
			entry.PeerID = pid.String()
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// parseDIDDNSDomain extracts the domain from a did:dns identifier.
func parseDIDDNSDomain(didDNS string) (string, error) {
	if !strings.HasPrefix(didDNS, "did:dns:") {
		return "", fmt.Errorf("%w: missing did:dns: prefix", ErrInvalidDIDDNS)
	}

	domain := didDNS[len("did:dns:"):]
	if domain == "" {
		return "", fmt.Errorf("%w: empty domain", ErrInvalidDIDDNS)
	}

	// Basic validation: domain should not contain spaces or colons.
	if strings.ContainsAny(domain, " \t:") {
		return "", fmt.Errorf("%w: invalid domain %q", ErrInvalidDIDDNS, domain)
	}

	return domain, nil
}

// extractDIDKeyFromTXT extracts a did:key URI from a DNS TXT record value.
// Supports two formats:
//   - "did=did:key:z6Mk..." (key=value format)
//   - "did:key:z6Mk..."     (bare URI)
func extractDIDKeyFromTXT(record string) string {
	record = strings.TrimSpace(record)

	// Try key=value format: "did=did:key:..."
	if strings.HasPrefix(record, "did=") {
		val := strings.TrimPrefix(record, "did=")
		val = strings.TrimSpace(val)
		if strings.HasPrefix(val, "did:key:") {
			return val
		}
	}

	// Try bare did:key URI.
	if strings.HasPrefix(record, "did:key:") {
		return record
	}

	return ""
}
