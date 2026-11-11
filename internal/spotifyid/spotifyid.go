// Package spotifyid は Spotify の ID / URI / URL を ID に正規化する。
package spotifyid

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Type は Spotify のリソース種別。
type Type string

const (
	TypeTrack    Type = "track"
	TypePlaylist Type = "playlist"
)

var idPattern = regexp.MustCompile(`^[0-9A-Za-z]{22}$`)

// IsID は s が Spotify の base62 ID 形式かどうかを返す。
func IsID(s string) bool {
	return idPattern.MatchString(s)
}

// URI は種別と ID から spotify:<type>:<id> 形式の URI を作る。
func URI(t Type, id string) string {
	return "spotify:" + string(t) + ":" + id
}

// ParseTrack はトラックの ID / URI / URL を受け取り、トラック ID を返す。
func ParseTrack(input string) (string, error) {
	return Parse(TypeTrack, input)
}

// ParsePlaylist はプレイリストの ID / URI / URL を受け取り、プレイリスト ID を返す。
func ParsePlaylist(input string) (string, error) {
	return Parse(TypePlaylist, input)
}

// Parse は want 種別の入力を ID に正規化する。
//
// 受け付ける形式:
//   - ID（22文字の base62）
//   - spotify:<type>:<ID>（プレイリストは spotify:user:<user>:playlist:<ID> も可）
//   - https://open.spotify.com/<type>/<ID>
//   - https://open.spotify.com/intl-ja/<type>/<ID>?si=... のようなロケール付き・クエリ付き URL
func Parse(want Type, input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", fmt.Errorf("empty %s", want)
	}

	if IsID(s) {
		return s, nil
	}

	if strings.HasPrefix(strings.ToLower(s), "spotify:") {
		return parseURI(want, s)
	}

	if strings.Contains(s, "://") || strings.HasPrefix(strings.ToLower(s), "open.spotify.com/") {
		return parseURL(want, s)
	}

	return "", fmt.Errorf("invalid %s %q: expected a 22-character ID, spotify:%s:<ID> URI, or https://open.spotify.com/%s/<ID> URL", want, input, want, want)
}

func parseURI(want Type, s string) (string, error) {
	parts := strings.Split(s, ":")
	// spotify:<type>:<id>
	if len(parts) == 3 {
		return checkTypeAndID(want, parts[1], parts[2], s)
	}
	// spotify:user:<user>:playlist:<id>（旧形式のプレイリスト URI）
	if len(parts) == 5 && strings.EqualFold(parts[1], "user") {
		return checkTypeAndID(want, parts[3], parts[4], s)
	}
	return "", fmt.Errorf("invalid %s URI %q", want, s)
}

func parseURL(want Type, s string) (string, error) {
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid %s URL %q: %w", want, s, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("invalid %s URL %q: unsupported scheme %q", want, s, u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host != "open.spotify.com" && host != "play.spotify.com" {
		return "", fmt.Errorf("invalid %s URL %q: host must be open.spotify.com (short links such as spotify.link are not supported)", want, s)
	}

	segs := make([]string, 0, 4)
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			segs = append(segs, p)
		}
	}
	// 先頭のロケール（intl-ja など）と埋め込み（embed）を取り除く。
	for len(segs) > 0 && (strings.HasPrefix(strings.ToLower(segs[0]), "intl-") || strings.EqualFold(segs[0], "embed")) {
		segs = segs[1:]
	}
	// /user/<user>/playlist/<id>（旧形式）
	if len(segs) == 4 && strings.EqualFold(segs[0], "user") {
		segs = segs[2:]
	}
	if len(segs) != 2 {
		return "", fmt.Errorf("invalid %s URL %q: expected path /%s/<ID>", want, s, want)
	}
	return checkTypeAndID(want, segs[0], segs[1], s)
}

func checkTypeAndID(want Type, gotType, id, original string) (string, error) {
	if !strings.EqualFold(gotType, string(want)) {
		return "", fmt.Errorf("invalid %s %q: this is a %s, not a %s", want, original, strings.ToLower(gotType), want)
	}
	if !IsID(id) {
		return "", fmt.Errorf("invalid %s %q: %q is not a valid Spotify ID", want, original, id)
	}
	return id, nil
}
