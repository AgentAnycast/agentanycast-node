package core

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/agentanycast/agentanycast-node/internal/config"
	"github.com/agentanycast/agentanycast-node/internal/envelope"
)

// ACLManager enforces skill-level access control based on source identity.
// Rules are evaluated in order; the first matching rule wins. If no rules
// are configured, all access is allowed. If rules are configured but none
// match, access is denied (default-deny).
type ACLManager struct {
	rules  []config.ACLRule
	logger *slog.Logger
}

// NewACLManager creates a new ACL manager with the given rules.
func NewACLManager(rules []config.ACLRule, logger *slog.Logger) *ACLManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ACLManager{
		rules:  rules,
		logger: logger.With("component", "acl"),
	}
}

// CheckAccess returns nil if the source identity is allowed to invoke the
// skill. Rules are evaluated in order; the first match wins. If no rule
// matches, access is denied when rules exist, or allowed when no rules
// are configured.
func (m *ACLManager) CheckAccess(source envelope.AgentIdentity, skillID string) error {
	sourceID := source.DIDKey
	if sourceID == "" {
		sourceID = source.PeerID
	}

	for _, rule := range m.rules {
		sourceMatch := matchGlob(rule.Source, sourceID)
		skillMatch := matchGlob(rule.Skill, skillID)

		if sourceMatch && skillMatch {
			if rule.Allow {
				m.logger.Debug("acl: access granted",
					"source", sourceID,
					"skill", skillID,
					"rule_source", rule.Source,
					"rule_skill", rule.Skill,
				)
				return nil
			}
			m.logger.Info("acl: access denied",
				"source", sourceID,
				"skill", skillID,
				"rule_source", rule.Source,
				"rule_skill", rule.Skill,
			)
			return fmt.Errorf("acl: access denied for %s to skill %s", sourceID, skillID)
		}
	}

	// Default: allow if no rules configured, deny otherwise.
	if len(m.rules) == 0 {
		return nil
	}
	m.logger.Info("acl: no matching rule (default deny)",
		"source", sourceID,
		"skill", skillID,
	)
	return fmt.Errorf("acl: no matching rule for %s to skill %s (default deny)", sourceID, skillID)
}

// matchGlob matches a pattern against a value. Supports:
//   - "*" matches everything
//   - "prefix:*" matches values starting with "prefix:"
//   - filepath.Match glob patterns (e.g., "did:web:*.example.com")
func matchGlob(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	// Support "did:web:example.com:*" style prefix matching.
	if strings.HasSuffix(pattern, ":*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)
	}
	matched, err := filepath.Match(pattern, value)
	if err != nil {
		return false // Invalid pattern never matches.
	}
	return matched
}
