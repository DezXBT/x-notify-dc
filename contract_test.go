package main

import "testing"

func TestDetectContractsEVM(t *testing.T) {
	addr := "0x1234567890abcdef1234567890abcdef12345678"
	cases := []string{
		"new gem " + addr + " ape now",
		addr,
		"CA: " + addr,
		"contract " + addr + " 🚀",
	}
	for _, text := range cases {
		got := detectContracts(text)
		if len(got) != 1 || got[0].Address != addr || got[0].Chain != "evm" {
			t.Errorf("detectContracts(%q) = %+v, want one evm %s", text, got, addr)
		}
	}

	// 40-hex embedded in a longer hex blob must NOT match.
	long := "0x1234567890abcdef1234567890abcdef1234567890ab"
	if got := detectContracts(long); len(got) != 0 {
		t.Errorf("expected no match inside longer hex blob, got %+v", got)
	}
}

func TestDetectContractsMultipleEVMDedup(t *testing.T) {
	a := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	text := a + " and again " + a + " plus " + b
	got := detectContracts(text)
	if len(got) != 2 {
		t.Fatalf("expected 2 distinct contracts (deduped), got %d: %+v", len(got), got)
	}
	if got[0].Address != a || got[1].Address != b {
		t.Errorf("expected order-preserving [a,b], got %+v", got)
	}
}

func TestDetectContractsSolana(t *testing.T) {
	sol := "3yYuW2UjLNYki79HSGj37XUMAyLkr6kxFwkLBYypHEgq"

	// No cue -> conservative matcher stays silent.
	if got := detectContracts(sol); len(got) != 0 {
		t.Errorf("expected no Solana match without cue, got %+v", got)
	}

	// With a cue -> detected as sol.
	got := detectContracts("CA: " + sol + " pump.fun launch")
	if len(got) != 1 || got[0].Address != sol || got[0].Chain != "sol" {
		t.Errorf("detectContracts with cue = %+v, want one sol %s", got, sol)
	}

	// All-lowercase base58-ish run rejected even with a cue.
	if got := detectContracts("contract abcdefghijkmnpqrstuvwxyzabcdefghijkmnpq pump"); len(got) != 0 {
		t.Errorf("expected all-lowercase run rejected, got %+v", got)
	}
}

func TestDetectContractsEmpty(t *testing.T) {
	if got := detectContracts(""); got != nil {
		t.Errorf("expected nil for empty text, got %+v", got)
	}
	if got := detectContracts("just a normal tweet, gm everyone"); len(got) != 0 {
		t.Errorf("expected no contracts in a plain tweet, got %+v", got)
	}
}

func TestDexScreenerURL(t *testing.T) {
	c := DetectedContract{Address: "0xabc", Chain: "evm"}
	if got := c.DexScreenerURL(); got != "https://dexscreener.com/search?q=0xabc" {
		t.Errorf("unexpected url: %q", got)
	}
	if got := (DetectedContract{}).DexScreenerURL(); got != "" {
		t.Errorf("expected empty url for empty address, got %q", got)
	}
}

func TestContractShort(t *testing.T) {
	c := DetectedContract{Address: "0x1234567890abcdef1234567890abcdef12345678"}
	if got := c.Short(); got != "0x1234…5678" {
		t.Errorf("Short() = %q, want 0x1234…5678", got)
	}
	// Short addresses are returned as-is.
	if got := (DetectedContract{Address: "0xabc"}).Short(); got != "0xabc" {
		t.Errorf("Short() short-circuit = %q, want 0xabc", got)
	}
}

func TestLooksLikeBase58Address(t *testing.T) {
	if !looksLikeBase58Address("3yYuW2UjLN9HSGj37") {
		t.Errorf("expected mixed-case-with-digit to look like an address")
	}
	for _, bad := range []string{"abcdefghij", "1234567890", "ABCDEFGHIJ"} {
		if looksLikeBase58Address(bad) {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestHasContractCue(t *testing.T) {
	for _, yes := range []string{"CA: foo", "contract here", "buy $PEPE", "pump.fun", "ca xyz"} {
		if !hasContractCue(yes) {
			t.Errorf("expected cue in %q", yes)
		}
	}
	if hasContractCue("just a normal gm tweet") {
		t.Errorf("did not expect a cue in a plain tweet")
	}
}
