// Package crypto provides did:dns DID method support.
//
// This is a thin wrapper around the agentanycast-identity package, adapting
// the identity package's DIDDNSEntry (which uses ed25519.PublicKey) to this
// package's DIDDNSEntry (which uses a libp2p PeerID string).
package crypto

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/AgentAnycast/agentanycast-identity"
)

// Errors for did:dns operations. Re-exported from the identity package.
var (
	ErrInvalidDIDDNS = identity.ErrInvalidDIDDNS
	ErrDIDDNSResolve = identity.ErrDIDDNSResolve
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
// Delegates validation logic to the identity package's rules.
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
