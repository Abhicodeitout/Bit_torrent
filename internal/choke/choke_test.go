package choke

import (
	"net"
	"testing"

	"torrent-client/internal/types"
)

func TestDecideUnchokes_RespectsMaxSlots(t *testing.T) {
	ur := NewUnchokeRound(4)
	ur.OptimisticInterval = 1

	peers := []types.Peer{
		{IP: net.ParseIP("10.0.0.1"), Port: 6881},
		{IP: net.ParseIP("10.0.0.2"), Port: 6882},
		{IP: net.ParseIP("10.0.0.3"), Port: 6883},
		{IP: net.ParseIP("10.0.0.4"), Port: 6884},
		{IP: net.ParseIP("10.0.0.5"), Port: 6885},
		{IP: net.ParseIP("10.0.0.6"), Port: 6886},
	}

	for i, p := range peers {
		ur.RegisterPeer(p)
		// Distinct contribution values for deterministic ordering.
		ur.RecordDownload(p, int64((len(peers)-i)*100))
	}

	_, _ = ur.DecideUnchokes()
	if got := countUnchoked(ur); got != 4 {
		t.Fatalf("first round unchoked=%d, want 4", got)
	}

	// Update contributions and run another optimistic round.
	for i, p := range peers {
		ur.RecordDownload(p, int64((i+1)*10))
	}
	_, _ = ur.DecideUnchokes()
	if got := countUnchoked(ur); got != 4 {
		t.Fatalf("second round unchoked=%d, want 4", got)
	}
}

func TestDecideUnchokes_ZeroMaxChokesEveryone(t *testing.T) {
	ur := NewUnchokeRound(1)
	p := types.Peer{IP: net.ParseIP("10.1.1.1"), Port: 7000}
	ur.RegisterPeer(p)
	ur.RecordDownload(p, 100)

	_, _ = ur.DecideUnchokes()
	if got := countUnchoked(ur); got != 1 {
		t.Fatalf("before zero limit unchoked=%d, want 1", got)
	}

	ur.MaxUnchoked = 0
	toUnchoke, toChoke := ur.DecideUnchokes()
	if len(toUnchoke) != 0 {
		t.Fatalf("toUnchoke=%d, want 0", len(toUnchoke))
	}
	if len(toChoke) != 1 {
		t.Fatalf("toChoke=%d, want 1", len(toChoke))
	}
	if got := countUnchoked(ur); got != 0 {
		t.Fatalf("after zero limit unchoked=%d, want 0", got)
	}
}

func countUnchoked(ur *UnchokeRound) int {
	total := 0
	for _, st := range ur.GetAllStats() {
		if !st.Choked {
			total++
		}
	}
	return total
}
