package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
)

const (
	// DefaultAPIBaseURL は Web API のベース URL。
	DefaultAPIBaseURL = "https://api.spotify.com/v1"
	// MaxItemsPerPage は GET /playlists/{id}/items の limit 上限。
	MaxItemsPerPage = 50
	// MaxItemsPerAdd は POST /playlists/{id}/items で1回に追加できる件数の上限。
	MaxItemsPerAdd = 100
)

// TokenSource は有効なアクセストークンを返す。
type TokenSource func(ctx context.Context) (string, error)

// Client は Web API のクライアント。
type Client struct {
	BaseURL   string
	Token     TokenSource
	Transport *Transport
}

// User は GET /me の応答のうち必要な部分。
type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	URI         string `json:"uri"`
}

// Playlist は GET /playlists/{id} の応答のうち必要な部分。
type Playlist struct {
	ID            string            `json:"id"`
	URI           string            `json:"uri"`
	Name          string            `json:"name"`
	Description   *string           `json:"description"`
	Public        *bool             `json:"public"`
	Collaborative bool              `json:"collaborative"`
	SnapshotID    string            `json:"snapshot_id"`
	ExternalURLs  map[string]string `json:"external_urls"`
	Owner         struct {
		ID          string  `json:"id"`
		DisplayName *string `json:"display_name"`
	} `json:"owner"`
	// Items は所有・共同編集しているプレイリストでだけ返る。
	Items *struct {
		Total int `json:"total"`
	} `json:"items"`
}

// PlaylistItemsPage は GET /playlists/{id}/items の1ページ。
type PlaylistItemsPage struct {
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Next   *string        `json:"next"`
	Items  []PlaylistItem `json:"items"`
}

// PlaylistItem はプレイリスト内の1要素。
type PlaylistItem struct {
	AddedAt string `json:"added_at"`
	IsLocal bool   `json:"is_local"`
	// Item はトラックまたはエピソード。削除済みなどで null のことがある。
	Item *PlayableItem `json:"item"`
	// Track は廃止予定の旧フィールド。Item が無い応答のときだけ使う。
	Track *PlayableItem `json:"track"`
}

// Playable は Item（なければ旧フィールドの Track）を返す。
func (p PlaylistItem) Playable() *PlayableItem {
	if p.Item != nil {
		return p.Item
	}
	return p.Track
}

// PlayableItem はトラックまたはエピソード。
type PlayableItem struct {
	Type         string            `json:"type"`
	ID           *string           `json:"id"`
	URI          string            `json:"uri"`
	Name         string            `json:"name"`
	DurationMS   int               `json:"duration_ms"`
	ExternalURLs map[string]string `json:"external_urls"`
	Artists      []struct {
		ID   *string `json:"id"`
		Name string  `json:"name"`
	} `json:"artists"`
	Album *struct {
		ID   *string `json:"id"`
		Name string  `json:"name"`
	} `json:"album"`
	Show *struct {
		ID   *string `json:"id"`
		Name string  `json:"name"`
	} `json:"show"`
}

// Me は現在のユーザーを返す。
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	if err := c.getJSON(ctx, "/me", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetPlaylist はプレイリストのメタデータを返す。
func (c *Client) GetPlaylist(ctx context.Context, playlistID string) (*Playlist, error) {
	q := url.Values{}
	q.Set("additional_types", "track,episode")
	var p Playlist
	if err := c.getJSON(ctx, "/playlists/"+url.PathEscape(playlistID), q, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPlaylistItems はプレイリストの要素を1ページ取得する（limit は 1〜50）。
func (c *Client) GetPlaylistItems(ctx context.Context, playlistID string, limit, offset int) (*PlaylistItemsPage, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	q.Set("additional_types", "track,episode")
	var page PlaylistItemsPage
	if err := c.getJSON(ctx, "/playlists/"+url.PathEscape(playlistID)+"/items", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ListPlaylistItems は offset から最大 limit 件（limit < 0 なら最後まで）をページングして取得する。
// 戻り値の total はプレイリスト全体の件数。
func (c *Client) ListPlaylistItems(ctx context.Context, playlistID string, limit, offset int) (items []PlaylistItem, total int, err error) {
	items = []PlaylistItem{}
	pos := offset
	for {
		pageSize := MaxItemsPerPage
		if limit >= 0 {
			remaining := limit - len(items)
			if remaining <= 0 {
				return items, total, nil
			}
			if remaining < pageSize {
				pageSize = remaining
			}
		}
		page, err := c.GetPlaylistItems(ctx, playlistID, pageSize, pos)
		if err != nil {
			return nil, 0, err
		}
		total = page.Total
		items = append(items, page.Items...)
		pos += len(page.Items)
		if len(page.Items) == 0 || page.Next == nil || pos >= page.Total {
			return items, total, nil
		}
	}
}

// AddResult は1回の追加リクエストの結果。
type AddResult struct {
	URIs       []string `json:"uris"`
	SnapshotID string   `json:"snapshot_id,omitempty"`
}

// AddItems は uris をプレイリスト末尾に追加する。100件を超える場合は分割して順に送る。
// 途中で失敗した場合は、それまでに成功した結果とエラーを返す。
func (c *Client) AddItems(ctx context.Context, playlistID string, uris []string) ([]AddResult, error) {
	results := []AddResult{}
	for _, chunk := range Chunk(uris, MaxItemsPerAdd) {
		payload, err := json.Marshal(map[string]any{"uris": chunk})
		if err != nil {
			return results, apperr.Wrap(apperr.KindInternal, err, "failed to encode request")
		}
		resp, err := c.send(ctx, http.MethodPost, "/playlists/"+url.PathEscape(playlistID)+"/items", nil, payload)
		if err != nil {
			return results, err
		}
		var body struct {
			SnapshotID string `json:"snapshot_id"`
		}
		if err := decodeJSON(resp.Body, &body); err != nil {
			return results, err
		}
		results = append(results, AddResult{URIs: chunk, SnapshotID: body.SnapshotID})
	}
	return results, nil
}

// Chunk は s を size 件ずつに分割する。
func Chunk(s []string, size int) [][]string {
	var out [][]string
	for len(s) > 0 {
		n := min(size, len(s))
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, v any) error {
	resp, err := c.send(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	return decodeJSON(resp.Body, v)
}

func (c *Client) send(ctx context.Context, method, path string, q url.Values, payload []byte) (*response, error) {
	if c.Token == nil {
		return nil, apperr.New(apperr.KindInternal, "no token source configured")
	}
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := c.baseURL() + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	transport := c.Transport
	if transport == nil {
		transport = NewTransport()
	}
	return transport.do(ctx, func(ctx context.Context) (*http.Request, error) {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, nil
	}, classifyAPIError)
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultAPIBaseURL
}
