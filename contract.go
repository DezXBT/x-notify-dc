package main

import (
	"regexp"
	"strings"
)

// ──────────────────────────────────────────────────────────────────────────────
// Contract-address auto-detection
//
// Scans a tweet's text (and any expanded URLs) for an EVM or Solana contract
// address and produces a clickable DexScreener link. Used by the tweet
// notification embed so a CA posted by a watched account becomes one tap away
// from the chart instead of a copy-paste.
// ──────────────────────────────────────────────────────────────────────────────

// evmAddrRe matches a 40-hex EVM contract/wallet address. Boundaries on both
// sides stop it pulling a 40-hex slice out of a longer hex blob (tx hashes etc).
var evmAddrRe = regexp.MustCompile(`(?:^|[^0-9a-fA-Fx])(0x[0-9a-fA-F]{40})(?:$|[^0-9a-fA-F])`)

// solAddrRe matches a base58 string 32-44 chars long (the Solana address shape).
// Base58 excludes 0, O, I, l to avoid visual ambiguity.
var solAddrRe = regexp.MustCompile(`(?:^|[^1-9A-HJ-NP-Za-km-z])([1-9A-HJ-NP-Za-km-z]{32,44})(?:$|[^1-9A-HJ-NP-Za-km-z])`)

// DetectedContract is a single contract found in a tweet plus its chain hint.
type DetectedContract struct {
	Address string // the raw contract address
	Chain   string // "evm" or "sol"
}

// DexScreenerURL returns the DexScreener link for this contract. The
// /search?q= endpoint resolves across every chain DexScreener indexes, so an
// exact chain isn't required.
func (d DetectedContract) DexScreenerURL() string {
	if d.Address == "" {
		return ""
	}
	return "https://dexscreener.com/search?q=" + d.Address
}

// Short renders the address as 0x1234…abcd for compact display in an embed
// field, keeping the full value available via the button URL.
func (d DetectedContract) Short() string {
	a := d.Address
	if len(a) <= 12 {
		return a
	}
	return a[:6] + "…" + a[len(a)-4:]
}

// detectContracts scans text for contract addresses and returns every distinct
// one found (EVM first, then cue-gated Solana), preserving order of appearance
// and de-duplicating. Returns nil when nothing matches.
//
// EVM is unambiguous (0x + 40 hex). Solana base58 is noisier, so a candidate is
// only accepted when the tweet carries an explicit CA cue ("ca", "contract",
// "pump", "$TICKER", …) AND the candidate has the mixed-case+digit shape of a
// real address.
func detectContracts(text string) []DetectedContract {
	if text == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []DetectedContract

	// EVM: collect all 0x… matches.
	for _, m := range evmAddrRe.FindAllStringSubmatch(text, -1) {
		addr := m[1]
		if !seen[strings.ToLower(addr)] {
			seen[strings.ToLower(addr)] = true
			out = append(out, DetectedContract{Address: addr, Chain: "evm"})
		}
	}

	// Solana: only when a cue is present, and only address-shaped candidates.
	if hasContractCue(text) {
		for _, m := range solAddrRe.FindAllStringSubmatch(text, -1) {
			cand := m[1]
			if looksLikeBase58Address(cand) && !seen[cand] {
				seen[cand] = true
				out = append(out, DetectedContract{Address: cand, Chain: "sol"})
			}
		}
	}
	return out
}

// hasContractCue reports whether the text hints that a contract address is
// present, used to gate the noisier Solana matcher.
func hasContractCue(text string) bool {
	l := strings.ToLower(text)
	cues := []string{"ca:", "ca ", "contract", "address", "pump", "bonk", "$"}
	for _, c := range cues {
		if strings.Contains(l, c) {
			return true
		}
	}
	return false
}

// looksLikeBase58Address applies cheap heuristics to weed out base58 matches
// that are unlikely to be real addresses: it requires at least one digit and a
// mix of upper and lower case (real Solana addresses always have all three).
func looksLikeBase58Address(s string) bool {
	var hasDigit, hasUpper, hasLower bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		}
	}
	return hasDigit && hasUpper && hasLower
}
