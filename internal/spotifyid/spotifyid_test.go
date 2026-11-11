package spotifyid

import "testing"

func TestParseTrackAcceptsAllFormatsAsSameTrack(t *testing.T) {
	const want = "0CmpgRM57Uq8KOQAzKOxrq"
	inputs := []string{
		"0CmpgRM57Uq8KOQAzKOxrq",
		"spotify:track:0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/intl-ja/track/0CmpgRM57Uq8KOQAzKOxrq?si=bd303f89bf084bee",
		// 周辺の揺れも同じトラックとして扱う。
		"  0CmpgRM57Uq8KOQAzKOxrq\n",
		"https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq?si=abc&context=spotify%3Aplaylist%3Ax",
		"https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq/",
		"https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq#frag",
		"http://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq",
		"open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/intl-pt/track/0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/embed/track/0CmpgRM57Uq8KOQAzKOxrq",
		"https://OPEN.SPOTIFY.COM/track/0CmpgRM57Uq8KOQAzKOxrq",
		"Spotify:Track:0CmpgRM57Uq8KOQAzKOxrq",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			got, err := ParseTrack(in)
			if err != nil {
				t.Fatalf("ParseTrack(%q) error: %v", in, err)
			}
			if got != want {
				t.Fatalf("ParseTrack(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestParseTrackRejectsInvalid(t *testing.T) {
	inputs := []string{
		"",
		"   ",
		"abc",
		"0CmpgRM57Uq8KOQAzKOxr",   // 21文字
		"0CmpgRM57Uq8KOQAzKOxrqq", // 23文字
		"0CmpgRM57Uq8KOQAzKOx-q",
		"spotify:track:",
		"spotify:track:short",
		"spotify:album:0CmpgRM57Uq8KOQAzKOxrq",
		"spotify:playlist:7xUQAlcjqXAnPin5jyeOHZ",
		"spotify:track:0CmpgRM57Uq8KOQAzKOxrq:extra",
		"https://open.spotify.com/album/0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/playlist/7xUQAlcjqXAnPin5jyeOHZ",
		"https://open.spotify.com/track/",
		"https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq/extra",
		"https://example.com/track/0CmpgRM57Uq8KOQAzKOxrq",
		"https://spotify.link/abcdef",
		"ftp://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			if got, err := ParseTrack(in); err == nil {
				t.Fatalf("ParseTrack(%q) = %q, want error", in, got)
			}
		})
	}
}

func TestParsePlaylistAcceptsAllFormats(t *testing.T) {
	const want = "7xUQAlcjqXAnPin5jyeOHZ"
	inputs := []string{
		"7xUQAlcjqXAnPin5jyeOHZ",
		"spotify:playlist:7xUQAlcjqXAnPin5jyeOHZ",
		"spotify:user:someone:playlist:7xUQAlcjqXAnPin5jyeOHZ",
		"https://open.spotify.com/playlist/7xUQAlcjqXAnPin5jyeOHZ",
		"https://open.spotify.com/playlist/7xUQAlcjqXAnPin5jyeOHZ?si=0123456789abcdef",
		"https://open.spotify.com/intl-ja/playlist/7xUQAlcjqXAnPin5jyeOHZ?si=0123456789abcdef&pi=x",
		"https://open.spotify.com/user/someone/playlist/7xUQAlcjqXAnPin5jyeOHZ",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			got, err := ParsePlaylist(in)
			if err != nil {
				t.Fatalf("ParsePlaylist(%q) error: %v", in, err)
			}
			if got != want {
				t.Fatalf("ParsePlaylist(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestParsePlaylistRejectsTrack(t *testing.T) {
	inputs := []string{
		"spotify:track:0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/track/0CmpgRM57Uq8KOQAzKOxrq",
		"https://open.spotify.com/intl-ja/track/0CmpgRM57Uq8KOQAzKOxrq?si=bd303f89bf084bee",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			if got, err := ParsePlaylist(in); err == nil {
				t.Fatalf("ParsePlaylist(%q) = %q, want error", in, got)
			}
		})
	}
}

func TestURI(t *testing.T) {
	if got := URI(TypeTrack, "0CmpgRM57Uq8KOQAzKOxrq"); got != "spotify:track:0CmpgRM57Uq8KOQAzKOxrq" {
		t.Fatalf("URI = %q", got)
	}
}
