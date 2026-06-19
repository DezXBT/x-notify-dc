package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
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
	Chain   string // detection-time guess: "evm" or "sol"

	// Resolved (filled by ResolveDexScreener; zero until then):
	ResolvedChain string // exact DexScreener chainId, e.g. "ethereum", "base", "solana"
	PairURL       string // direct DexScreener pair URL, e.g. dexscreener.com/base/0x…
	Symbol        string // base token symbol from the top pair, e.g. "WPRL"
	LiquidityUSD  float64
}

// dexScreener is the shared HTTP client for token lookups (short timeout so a
// slow API never holds up a notification).
var dexScreener = &http.Client{Timeout: 6 * time.Second}

// dexTokenAPI is the DexScreener token endpoint; %s is the contract address.
// It returns every pair the address trades in, across all chains.
const dexTokenAPI = "https://api.dexscreener.com/latest/dex/tokens/%s"

// dexPair mirrors the fields we use from a DexScreener pair object.
type dexPair struct {
	ChainID   string `json:"chainId"`
	URL       string `json:"url"`
	BaseToken struct {
		Address string `json:"address"`
		Symbol  string `json:"symbol"`
	} `json:"baseToken"`
	Liquidity struct {
		USD float64 `json:"usd"`
	} `json:"liquidity"`
}

// ChartURL returns the best DexScreener link for this contract: the resolved
// direct pair URL when available (Rick-bot style, dexscreener.com/<chain>/<pair>),
// otherwise a generic search link as a safe fallback.
func (d DetectedContract) ChartURL() string {
	if d.PairURL != "" {
		return d.PairURL
	}
	if d.Address == "" {
		return ""
	}
	return "https://dexscreener.com/search?q=" + d.Address
}

// Resolved reports whether a direct DexScreener pair link was found.
func (d DetectedContract) Resolved() bool { return d.PairURL != "" }

// Short renders the address as 0x1234…abcd for compact display in an embed
// field, keeping the full value available via the button URL.
func (d DetectedContract) Short() string {
	a := d.Address
	if len(a) <= 12 {
		return a
	}
	return a[:6] + "…" + a[len(a)-4:]
}

// ResolveDexScreener looks the contract up on DexScreener and fills the resolved
// chain, direct pair URL, symbol, and liquidity from the highest-liquidity pair.
// It mutates the receiver in place. Network/parse failures are non-fatal: the
// contract simply stays unresolved and ChartURL falls back to a search link.
func (d *DetectedContract) ResolveDexScreener() {
	if d.Address == "" {
		return
	}
	url := strings.Replace(dexTokenAPI, "%s", d.Address, 1)
	resp, err := dexScreener.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return
	}
	var parsed struct {
		Pairs []dexPair `json:"pairs"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	pick := bestPair(parsed.Pairs)
	if pick == nil {
		return
	}
	d.ResolvedChain = pick.ChainID
	d.PairURL = pick.URL
	d.Symbol = pick.BaseToken.Symbol
	d.LiquidityUSD = pick.Liquidity.USD
}

// bestPair returns the highest-liquidity pair from a DexScreener response, or
// nil when there are none. Highest liquidity is the most useful chart to land
// on (matches what Rick-style bots surface).
func bestPair(pairs []dexPair) *dexPair {
	if len(pairs) == 0 {
		return nil
	}
	sorted := make([]dexPair, len(pairs))
	copy(sorted, pairs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Liquidity.USD > sorted[j].Liquidity.USD
	})
	return &sorted[0]
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

	// EVM: collect all 0x… matches. Register both the full address and the
	// bare hex body (lowercased) so the Solana matcher can't re-detect a
	// checksummed EVM address that lost its "0" prefix as a base58 candidate.
	for _, m := range evmAddrRe.FindAllStringSubmatch(text, -1) {
		addr := m[1]
		lower := strings.ToLower(addr)
		if !seen[lower] {
			seen[lower] = true
			seen[lower[2:]] = true // "76a43f…aba3" — blocks Solana false match
			out = append(out, DetectedContract{Address: addr, Chain: "evm"})
		}
	}

	// Solana: only when a cue is present, and only address-shaped candidates
	// that aren't hex-only (40 hex chars = EVM address, not Solana).
	if hasContractCue(text) {
		for _, m := range solAddrRe.FindAllStringSubmatch(text, -1) {
			cand := m[1]
			if isHexString(cand) {
				continue // 40-char hex → EVM address masquerading as base58
			}
			if looksLikeBase58Address(cand) && !seen[strings.ToLower(cand)] {
				seen[strings.ToLower(cand)] = true
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

// isHexString reports whether s is exactly 40 hex characters (the shape of an
// EVM address without the 0x prefix). Used to reject EVM addresses that the
// Solana base58 matcher would otherwise accept.
func isHexString(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
