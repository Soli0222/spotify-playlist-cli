package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
)

type sleepRecorder struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, d)
	return nil
}

func newTestClient(t *testing.T, h http.Handler) (*Client, *sleepRecorder) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rec := &sleepRecorder{}
	tr := &Transport{HTTPClient: srv.Client(), MaxRetries: DefaultMaxRetries, MaxRetryWait: DefaultMaxRetryWait, Sleep: rec.sleep}
	return &Client{
		BaseURL:   srv.URL,
		Transport: tr,
		Token:     func(context.Context) (string, error) { return "test-token", nil },
	}, rec
}

func kindOf(t *testing.T, err error) apperr.Kind {
	t.Helper()
	e := apperr.As(err)
	if e == nil {
		t.Fatalf("error %v is not *apperr.Error", err)
	}
	return e.Kind
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":{"status":%d,"message":%q}}`, status, msg)
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	var calls int
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.Header().Set("Retry-After", "3")
			writeAPIError(w, http.StatusTooManyRequests, "API rate limit exceeded")
			return
		}
		_, _ = io.WriteString(w, `{"id":"user1","display_name":"User One"}`)
	}))
	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.ID != "user1" || calls != 3 {
		t.Fatalf("me=%+v calls=%d", me, calls)
	}
	if len(rec.waits) != 2 || rec.waits[0] != 3*time.Second {
		t.Fatalf("waits = %v, want [3s 3s]", rec.waits)
	}
}

func TestRetryOn429GivesUpAfterMaxRetries(t *testing.T) {
	var calls int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "API rate limit exceeded")
	}))
	_, err := c.Me(context.Background())
	if kindOf(t, err) != apperr.KindRateLimited {
		t.Fatalf("kind = %v", kindOf(t, err))
	}
	if calls != DefaultMaxRetries+1 {
		t.Fatalf("calls = %d, want %d", calls, DefaultMaxRetries+1)
	}
}

func TestRetryAfterTooLongFailsImmediately(t *testing.T) {
	var calls int
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "3600")
		writeAPIError(w, http.StatusTooManyRequests, "API rate limit exceeded")
	}))
	_, err := c.Me(context.Background())
	e := apperr.As(err)
	if e == nil || e.Kind != apperr.KindRateLimited || e.Details["retry_after_seconds"] != 3600 {
		t.Fatalf("err = %#v", err)
	}
	if calls != 1 || len(rec.waits) != 0 {
		t.Fatalf("calls=%d waits=%v", calls, rec.waits)
	}
}

func TestNoRetryOnOtherErrors(t *testing.T) {
	cases := []struct {
		status int
		msg    string
		want   apperr.Kind
	}{
		{http.StatusUnauthorized, "The access token expired", apperr.KindUnauthorized},
		{http.StatusForbidden, "Insufficient client scope", apperr.KindInsufficientScope},
		{http.StatusForbidden, "Forbidden", apperr.KindForbidden},
		{http.StatusForbidden, "Check settings on developer.spotify.com/dashboard, the user may not be registered.", apperr.KindForbidden},
		{http.StatusNotFound, "Resource not found", apperr.KindNotFound},
		{http.StatusInternalServerError, "Server error", apperr.KindAPIError},
		{http.StatusBadRequest, "Invalid base62 id", apperr.KindAPIError},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.msg), func(t *testing.T) {
			var calls int
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				writeAPIError(w, tc.status, tc.msg)
			}))
			_, err := c.Me(context.Background())
			e := apperr.As(err)
			if e == nil || e.Kind != tc.want || e.Status != tc.status || e.Message != tc.msg {
				t.Fatalf("err = %#v, want kind %s", err, tc.want)
			}
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
		})
	}
}

// fakePlaylist は n 件のトラックを持つプレイリストを返す。
func fakePlaylist(t *testing.T, n int, maxLimit int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/playlists/PL/items" {
			t.Errorf("unexpected path %s", r.URL.Path)
			writeAPIError(w, 404, "not found")
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit < 1 || limit > maxLimit {
			writeAPIError(w, 400, "Invalid limit")
			return
		}
		items := []map[string]any{}
		for i := offset; i < n && i < offset+limit; i++ {
			id := fmt.Sprintf("%022d", i)
			items = append(items, map[string]any{
				"added_at": "2026-11-11T00:00:00Z",
				"is_local": false,
				"item": map[string]any{
					"type": "track", "id": id, "uri": "spotify:track:" + id, "name": fmt.Sprintf("Song %d", i),
					"artists": []map[string]any{{"id": "a", "name": "Artist"}},
					"album":   map[string]any{"id": "b", "name": "Album"},
				},
			})
		}
		var next any
		if offset+limit < n {
			next = "https://api.spotify.com/v1/playlists/PL/items?offset=" + strconv.Itoa(offset+limit)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": n, "limit": limit, "offset": offset, "next": next, "items": items})
	})
}

func TestListPlaylistItemsPaginatesAll(t *testing.T) {
	c, _ := newTestClient(t, fakePlaylist(t, 123, MaxItemsPerPage))
	items, total, err := c.ListPlaylistItems(context.Background(), "PL", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 123 || len(items) != 123 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	for i, it := range items {
		if *it.Playable().ID != fmt.Sprintf("%022d", i) {
			t.Fatalf("item %d has id %s", i, *it.Playable().ID)
		}
	}
}

func TestListPlaylistItemsLimitOffset(t *testing.T) {
	c, _ := newTestClient(t, fakePlaylist(t, 123, MaxItemsPerPage))
	items, total, err := c.ListPlaylistItems(context.Background(), "PL", 70, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 123 || len(items) != 70 {
		t.Fatalf("total=%d len=%d", total, len(items))
	}
	if *items[0].Playable().ID != fmt.Sprintf("%022d", 10) || *items[69].Playable().ID != fmt.Sprintf("%022d", 79) {
		t.Fatal("unexpected range")
	}

	// 末尾を超える範囲は存在する分だけ返す。
	items, _, err = c.ListPlaylistItems(context.Background(), "PL", 50, 120)
	if err != nil || len(items) != 3 {
		t.Fatalf("len=%d err=%v", len(items), err)
	}
}

func TestListPlaylistItemsEmpty(t *testing.T) {
	c, _ := newTestClient(t, fakePlaylist(t, 0, MaxItemsPerPage))
	items, total, err := c.ListPlaylistItems(context.Background(), "PL", -1, 0)
	if err != nil || total != 0 || items == nil || len(items) != 0 {
		t.Fatalf("items=%v total=%d err=%v", items, total, err)
	}
}

func TestPlaylistItemFallsBackToDeprecatedTrackField(t *testing.T) {
	var it PlaylistItem
	if err := json.Unmarshal([]byte(`{"track":{"type":"track","id":"x","uri":"spotify:track:x","name":"Old"}}`), &it); err != nil {
		t.Fatal(err)
	}
	if it.Playable() == nil || it.Playable().Name != "Old" {
		t.Fatalf("Playable = %+v", it.Playable())
	}
}

func TestAddItemsChunksBy100AndUsesItemsEndpoint(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/playlists/PL/items" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var body struct {
			URIs     []string `json:"uris"`
			Position *int     `json:"position"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Position != nil {
			t.Error("position must be omitted to append")
		}
		mu.Lock()
		bodies = append(bodies, body.URIs)
		n := len(bodies)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"snapshot_id":"snap%d"}`, n)
	}))
	uris := make([]string, 250)
	for i := range uris {
		uris[i] = fmt.Sprintf("spotify:track:%022d", i)
	}
	results, err := c.AddItems(context.Background(), "PL", uris)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 || len(bodies[0]) != 100 || len(bodies[1]) != 100 || len(bodies[2]) != 50 {
		t.Fatalf("chunk sizes wrong: %d", len(bodies))
	}
	if bodies[2][49] != uris[249] || bodies[1][0] != uris[100] {
		t.Fatal("order not preserved")
	}
	if len(results) != 3 || results[2].SnapshotID != "snap3" {
		t.Fatalf("results = %+v", results)
	}
}

func TestAddItemsPartialFailure(t *testing.T) {
	var calls int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			writeAPIError(w, http.StatusForbidden, "Insufficient client scope")
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"snapshot_id":"s"}`)
	}))
	uris := make([]string, 150)
	for i := range uris {
		uris[i] = "spotify:track:" + strings.Repeat("a", 22)
	}
	results, err := c.AddItems(context.Background(), "PL", uris)
	if kindOf(t, err) != apperr.KindInsufficientScope {
		t.Fatalf("err = %v", err)
	}
	if len(results) != 1 || len(results[0].URIs) != 100 {
		t.Fatalf("results = %d", len(results))
	}
}

func TestChunk(t *testing.T) {
	if got := Chunk(nil, 100); len(got) != 0 {
		t.Fatalf("Chunk(nil) = %v", got)
	}
	if got := Chunk([]string{"a", "b", "c"}, 2); len(got) != 2 || len(got[1]) != 1 {
		t.Fatalf("Chunk = %v", got)
	}
}

func TestOAuthAuthorizeURL(t *testing.T) {
	o := &OAuth{ClientID: "cid", ClientSecret: "secret"}
	u := o.AuthorizeURL("st")
	for _, want := range []string{
		"https://accounts.spotify.com/authorize?",
		"client_id=cid",
		"response_type=code",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A8765%2Fcallback",
		"scope=playlist-read-private+playlist-modify-public+playlist-modify-private",
		"state=st",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("AuthorizeURL %q does not contain %q", u, want)
		}
	}
	if strings.Contains(u, "secret") {
		t.Error("AuthorizeURL must not contain the client secret")
	}
}

func TestOAuthExchangeAndRefresh(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token" {
			t.Errorf("path = %s", r.URL.Path)
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != "cid" || secret != "csecret" {
			t.Errorf("basic auth = %v %q %q", ok, id, secret)
		}
		_ = r.ParseForm()
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			if r.PostForm.Get("code") != "the-code" || r.PostForm.Get("redirect_uri") != RedirectURI {
				t.Errorf("form = %v", r.PostForm)
			}
			_, _ = io.WriteString(w, `{"access_token":"at","token_type":"Bearer","scope":"playlist-read-private","expires_in":3600,"refresh_token":"rt"}`)
		case "refresh_token":
			if r.PostForm.Get("refresh_token") == "revoked" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Refresh token revoked"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at2","token_type":"Bearer","scope":"playlist-read-private","expires_in":3600}`)
		}
	}))
	defer srv.Close()
	o := &OAuth{ClientID: "cid", ClientSecret: "csecret", AccountsBaseURL: srv.URL, Transport: &Transport{HTTPClient: srv.Client()}, Now: func() time.Time { return now }}

	tok, err := o.Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" || !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("tok = %+v", tok)
	}

	tok, err = o.Refresh(context.Background(), "rt")
	if err != nil || tok.AccessToken != "at2" || tok.RefreshToken != "" {
		t.Fatalf("tok = %+v err=%v", tok, err)
	}

	_, err = o.Refresh(context.Background(), "revoked")
	e := apperr.As(err)
	if e == nil || e.Kind != apperr.KindAuthFailed || !strings.Contains(e.Message, "invalid_grant") {
		t.Fatalf("err = %#v", err)
	}
}

func TestOAuthRequiresClientCredentials(t *testing.T) {
	o := &OAuth{}
	_, err := o.Refresh(context.Background(), "rt")
	if kindOf(t, err) != apperr.KindMissingClientCredentials {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingScopes(t *testing.T) {
	if got := MissingScopes("playlist-modify-private playlist-read-private playlist-modify-public"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	got := MissingScopes("playlist-read-private")
	if len(got) != 2 || got[0] != "playlist-modify-public" || got[1] != "playlist-modify-private" {
		t.Fatalf("got %v", got)
	}
}
