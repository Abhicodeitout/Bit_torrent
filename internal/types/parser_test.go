package types

import (
	"fmt"
	"testing"
)

func TestParseMagnetLinkHexInfoHash(t *testing.T) {
	t.Parallel()

	magnet := "magnet:?xt=urn:btih:8F7C6B1559607AFA3A4CEFB1836E9E8415E3355F&dn=Big+Buck+Bunny&tr=udp://tracker.opentrackr.org:1337/announce&tr=https://tracker.example/announce"
	link, err := ParseMagnetLink(magnet)
	if err != nil {
		t.Fatalf("ParseMagnetLink returned error: %v", err)
	}

	got := fmt.Sprintf("%x", link.InfoHash)
	if got != "8f7c6b1559607afa3a4cefb1836e9e8415e3355f" {
		t.Fatalf("unexpected info hash: %s", got)
	}
	if link.Name != "Big Buck Bunny" {
		t.Fatalf("unexpected name: %q", link.Name)
	}
	if len(link.Trackers) != 2 {
		t.Fatalf("unexpected tracker count: %d", len(link.Trackers))
	}
}

func TestParseMagnetLinkBase32LowercaseInfoHash(t *testing.T) {
	t.Parallel()

	magnet := "magnet:?xt=urn:btih:r56gwfkzmb5puosm56yyg3u6qqk6gnk7&tr=udp://tracker.opentrackr.org:1337/announce"
	link, err := ParseMagnetLink(magnet)
	if err != nil {
		t.Fatalf("ParseMagnetLink returned error: %v", err)
	}

	got := fmt.Sprintf("%x", link.InfoHash)
	if got != "8f7c6b1559607afa3a4cefb1836e9e8415e3355f" {
		t.Fatalf("unexpected info hash from base32 magnet: %s", got)
	}
}

func TestParseMagnetLinkRejectsMissingOrInvalidXT(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		magnet string
	}{
		{name: "missing xt", magnet: "magnet:?dn=NoHash&tr=udp://tracker.opentrackr.org:1337/announce"},
		{name: "invalid xt", magnet: "magnet:?xt=urn:btih:not-a-real-hash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseMagnetLink(tt.magnet); err == nil {
				t.Fatalf("ParseMagnetLink(%q) unexpectedly succeeded", tt.magnet)
			}
		})
	}
}
