package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
	"github.com/soli0222/spotify-playlist-cli/internal/spotify"
	"github.com/soli0222/spotify-playlist-cli/internal/token"
)

const (
	testPlaylistID = "7xUQAlcjqXAnPin5jyeOHZ"
	testTrackID    = "0CmpgRM57Uq8KOQAzKOxrq"
	fullScopes     = "playlist-read-private playlist-modify-public playlist-modify-private"
)

var testNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// fakeSpotify は Web API と Accounts をまとめて模倣する。
type fakeSpotify struct {
	t           *testing.T
	mu          sync.Mutex
	items       []string // プレイリスト内のトラック URI
	requests    []string
	addStatus   int
	addMessage  string
	tokenScope  string
	accessToken string
}

func (f *fakeSpotify) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path == "/accounts/api/token" {
		_ = r.ParseForm()
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","scope":%q,"expires_in":3600,"refresh_token":"refresh-1"}`, f.accessToken, f.tokenScope)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.accessToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"status":401,"message":"Invalid access token"}}`)
		return
	}
	switch {
	case r.URL.Path == "/v1/me":
		_, _ = io.WriteString(w, `{"id":"soli","display_name":"Soli","uri":"spotify:user:soli"}`)
	case r.URL.Path == "/v1/playlists/"+testPlaylistID && r.Method == http.MethodGet:
		_, _ = fmt.Fprintf(w, `{"id":%q,"uri":"spotify:playlist:%s","name":"プレイリスト #26","description":null,"public":false,"collaborative":false,"snapshot_id":"snap","external_urls":{"spotify":"https://open.spotify.com/playlist/%s"},"owner":{"id":"soli","display_name":"Soli"},"items":{"total":%d}}`, testPlaylistID, testPlaylistID, testPlaylistID, len(f.items))
	case r.URL.Path == "/v1/playlists/"+testPlaylistID+"/items" && r.Method == http.MethodGet:
		items := []map[string]any{}
		for _, uri := range f.items {
			id := strings.TrimPrefix(uri, "spotify:track:")
			items = append(items, map[string]any{
				"added_at": "2026-11-11T00:00:00Z", "is_local": false,
				"item": map[string]any{"type": "track", "id": id, "uri": uri, "name": "Song " + id[:4],
					"artists": []map[string]any{{"id": "artist1", "name": "Artist A"}, {"id": "artist2", "name": "Artist B"}},
					"album":   map[string]any{"id": "album1", "name": "Album"}},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": len(f.items), "limit": 50, "offset": 0, "next": nil, "items": items})
	case r.URL.Path == "/v1/playlists/"+testPlaylistID+"/items" && r.Method == http.MethodPost:
		if f.addStatus != 0 {
			w.WriteHeader(f.addStatus)
			_, _ = fmt.Fprintf(w, `{"error":{"status":%d,"message":%q}}`, f.addStatus, f.addMessage)
			return
		}
		var body struct {
			URIs []string `json:"uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.items = append(f.items, body.URIs...)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"snapshot_id":"snap-after"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"status":404,"message":"Resource not found"}}`)
	}
}

type harness struct {
	app       *App
	fake      *fakeSpotify
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
	tokenPath string
	env       map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fake := &fakeSpotify{t: t, tokenScope: fullScopes, accessToken: "access-1"}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	h := &harness{
		fake:      fake,
		stdout:    &bytes.Buffer{},
		stderr:    &bytes.Buffer{},
		tokenPath: filepath.Join(t.TempDir(), "spotify-playlist-cli", "token.json"),
		env:       map[string]string{envClientID: "cid", envClientSecret: "csecret"},
	}
	h.app = &App{
		Stdin:           strings.NewReader(""),
		Stdout:          h.stdout,
		Stderr:          h.stderr,
		Getenv:          func(k string) string { return h.env[k] },
		TokenPath:       h.tokenPath,
		APIBaseURL:      srv.URL + "/v1",
		AccountsBaseURL: srv.URL + "/accounts",
		Transport:       &spotify.Transport{HTTPClient: srv.Client(), MaxRetries: 3, Sleep: func(context.Context, time.Duration) error { return nil }},
		Now:             func() time.Time { return testNow },
	}
	return h
}

func (h *harness) saveToken(t *testing.T, tok *token.Token) {
	t.Helper()
	if err := (&token.Store{Path: h.tokenPath}).Save(tok); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) login(t *testing.T) {
	h.saveToken(t, &token.Token{AccessToken: "access-1", TokenType: "Bearer", RefreshToken: "refresh-1", Scope: fullScopes, ExpiresAt: testNow.Add(time.Hour)})
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return Run(context.Background(), h.app, args)
}

func decode[T any](t *testing.T, b *bytes.Buffer) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b.Bytes(), &v); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, b.String())
	}
	return v
}

type errorEnvelope struct {
	Error struct {
		Kind    string         `json:"kind"`
		Status  int            `json:"status"`
		Message string         `json:"message"`
		Hint    string         `json:"hint"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func (h *harness) expectError(t *testing.T, code int, wantCode int, wantKind apperr.Kind) errorEnvelope {
	t.Helper()
	if code != wantCode {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, wantCode, h.stdout, h.stderr)
	}
	if h.stdout.Len() != 0 {
		t.Fatalf("stdout must be empty on error: %s", h.stdout)
	}
	env := decode[errorEnvelope](t, h.stderr)
	if env.Error.Kind != string(wantKind) {
		t.Fatalf("kind = %q, want %q (%s)", env.Error.Kind, wantKind, h.stderr)
	}
	return env
}

func TestAcceptanceFlowOnTestPlaylist(t *testing.T) {
	h := newHarness(t)
	h.login(t)

	// 1. 空であること
	if code := h.run("playlist", "items", testPlaylistID); code != 0 {
		t.Fatalf("items exit %d: %s", code, h.stderr)
	}
	out := decode[itemsOutput](t, h.stdout)
	if out.Total != 0 || out.Count != 0 || out.Items == nil {
		t.Fatalf("items = %+v", out)
	}
	if !strings.Contains(h.stdout.String(), `"items":[]`) {
		t.Fatalf("items must be an empty array: %s", h.stdout)
	}

	// 2. intl-ja + si 付き URL で追加できること
	code := h.run("playlist", "add", testPlaylistID, "https://open.spotify.com/intl-ja/track/"+testTrackID+"?si=bd303f89bf084bee")
	if code != 0 {
		t.Fatalf("add exit %d: %s", code, h.stderr)
	}
	added := decode[addOutput](t, h.stdout)
	if added.DryRun || added.Count != 1 || added.URIs[0] != "spotify:track:"+testTrackID || added.SnapshotID != "snap-after" {
		t.Fatalf("add = %+v", added)
	}

	// 3. 追加した1曲が入っていること
	if code := h.run("playlist", "items", "https://open.spotify.com/playlist/"+testPlaylistID+"?si=x"); code != 0 {
		t.Fatalf("items exit %d: %s", code, h.stderr)
	}
	out = decode[itemsOutput](t, h.stdout)
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("items = %+v", out)
	}
	it := out.Items[0]
	if *it.ID != testTrackID || it.URI != "spotify:track:"+testTrackID || it.Name == "" || len(it.Artists) != 2 || it.Artists[0].Name != "Artist A" {
		t.Fatalf("item = %+v", it)
	}
}

func TestAddSendsAllFormatsAsSameTrack(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	code := h.run("playlist", "add", "spotify:playlist:"+testPlaylistID,
		testTrackID,
		"spotify:track:"+testTrackID,
		"https://open.spotify.com/track/"+testTrackID,
		"https://open.spotify.com/intl-ja/track/"+testTrackID+"?si=bd303f89bf084bee",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := decode[addOutput](t, h.stdout)
	if out.Count != 4 {
		t.Fatalf("count = %d", out.Count)
	}
	for _, u := range out.URIs {
		if u != "spotify:track:"+testTrackID {
			t.Fatalf("uri = %q", u)
		}
	}
	// 重複チェックはせず、言われたとおり4件追加する。
	if len(h.fake.items) != 4 {
		t.Fatalf("playlist has %d items", len(h.fake.items))
	}
}

func TestAddDryRunDoesNotCallAPI(t *testing.T) {
	h := newHarness(t)
	// トークンが無くても dry-run は動く。
	args := []string{"playlist", "add", testPlaylistID, "--dry-run"}
	for i := range 150 {
		args = append(args, fmt.Sprintf("%022d", i))
	}
	if code := h.run(args...); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := decode[addOutput](t, h.stdout)
	if !out.DryRun || out.PlaylistID != testPlaylistID || out.Count != 150 || len(out.Requests) != 2 || len(out.Requests[0].URIs) != 100 {
		t.Fatalf("out = %+v", out)
	}
	if len(h.fake.requests) != 0 {
		t.Fatalf("dry-run made requests: %v", h.fake.requests)
	}
}

func TestAddValidatesBeforeCallingAPI(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	code := h.run("playlist", "add", testPlaylistID, testTrackID, "https://open.spotify.com/album/"+testTrackID, "nope")
	env := h.expectError(t, code, apperr.ExitUsage, apperr.KindInvalidArgument)
	invalid, _ := env.Error.Details["invalid"].([]any)
	if len(invalid) != 2 {
		t.Fatalf("details = %v", env.Error.Details)
	}
	if len(h.fake.requests) != 0 {
		t.Fatalf("requests made: %v", h.fake.requests)
	}
}

func TestInvalidPlaylistIsRejected(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	for _, args := range [][]string{
		{"playlist", "items", "https://open.spotify.com/track/" + testTrackID},
		{"playlist", "get", "bad"},
		{"playlist", "add", "bad", testTrackID},
	} {
		h.expectError(t, h.run(args...), apperr.ExitUsage, apperr.KindInvalidArgument)
	}
	if len(h.fake.requests) != 0 {
		t.Fatalf("requests made: %v", h.fake.requests)
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"playlist", "add", testPlaylistID},
		{"playlist", "items"},
		{"playlist", "get", "a", "b"},
		{"playlist", "items", testPlaylistID, "--limit", "0"},
		{"playlist", "items", testPlaylistID, "--offset", "-1"},
		{"playlist", "items", testPlaylistID, "--limit", "abc"},
		{"playlist", "items", testPlaylistID, "--unknown"},
		{"nosuch"},
	} {
		code := h.run(args...)
		if code != apperr.ExitUsage {
			t.Errorf("%v: exit %d, stderr %s", args, code, h.stderr)
			continue
		}
		env := decode[errorEnvelope](t, h.stderr)
		if env.Error.Kind != string(apperr.KindUsage) && env.Error.Kind != string(apperr.KindInvalidArgument) {
			t.Errorf("%v: kind %s", args, env.Error.Kind)
		}
	}
}

func TestInsufficientScopeIsDistinguished(t *testing.T) {
	h := newHarness(t)
	h.saveToken(t, &token.Token{AccessToken: "access-1", RefreshToken: "r", Scope: "playlist-read-private", ExpiresAt: testNow.Add(time.Hour)})
	h.fake.addStatus = http.StatusForbidden
	h.fake.addMessage = "Insufficient client scope"
	env := h.expectError(t, h.run("playlist", "add", testPlaylistID, testTrackID), apperr.ExitInsufficientScope, apperr.KindInsufficientScope)
	if env.Error.Status != 403 || env.Error.Message != "Insufficient client scope" {
		t.Fatalf("env = %+v", env)
	}
	missing, _ := env.Error.Details["missing_scopes"].([]any)
	if len(missing) != 2 {
		t.Fatalf("details = %v", env.Error.Details)
	}
	if env.Error.Details["added_count"] != float64(0) {
		t.Fatalf("added_count = %v", env.Error.Details["added_count"])
	}
}

func TestForbiddenIsNotScope(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	h.fake.addStatus = http.StatusForbidden
	h.fake.addMessage = "Forbidden"
	env := h.expectError(t, h.run("playlist", "add", testPlaylistID, testTrackID), apperr.ExitForbidden, apperr.KindForbidden)
	if !strings.Contains(env.Error.Hint, "not a scope problem") {
		t.Fatalf("hint = %q", env.Error.Hint)
	}
}

func TestUnauthorizedIsDistinguished(t *testing.T) {
	h := newHarness(t)
	h.saveToken(t, &token.Token{AccessToken: "revoked", RefreshToken: "r", Scope: fullScopes, ExpiresAt: testNow.Add(time.Hour)})
	h.expectError(t, h.run("playlist", "items", testPlaylistID), apperr.ExitAuth, apperr.KindUnauthorized)
}

func TestNotAuthenticated(t *testing.T) {
	h := newHarness(t)
	h.expectError(t, h.run("auth", "status"), apperr.ExitAuth, apperr.KindNotAuthenticated)
	h.expectError(t, h.run("playlist", "items", testPlaylistID), apperr.ExitAuth, apperr.KindNotAuthenticated)
}

func TestExpiredTokenIsRefreshedAutomatically(t *testing.T) {
	h := newHarness(t)
	h.saveToken(t, &token.Token{AccessToken: "stale", RefreshToken: "refresh-1", Scope: fullScopes, ExpiresAt: testNow.Add(-time.Minute)})
	if code := h.run("playlist", "get", testPlaylistID); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	p := decode[playlistOutput](t, h.stdout)
	if p.ID != testPlaylistID || p.TotalItems == nil || *p.TotalItems != 0 || p.Owner.ID != "soli" {
		t.Fatalf("playlist = %+v", p)
	}
	saved, err := (&token.Store{Path: h.tokenPath}).Load()
	if err != nil || saved.AccessToken != "access-1" || saved.RefreshToken != "refresh-1" {
		t.Fatalf("saved = %+v err=%v", saved, err)
	}
}

func TestExpiredTokenWithoutClientCredentials(t *testing.T) {
	h := newHarness(t)
	h.env = map[string]string{}
	h.saveToken(t, &token.Token{AccessToken: "stale", RefreshToken: "refresh-1", Scope: fullScopes, ExpiresAt: testNow.Add(-time.Minute)})
	h.expectError(t, h.run("playlist", "get", testPlaylistID), apperr.ExitAuth, apperr.KindMissingClientCredentials)
}

func TestAuthStatusDoesNotLeakSecrets(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	if code := h.run("auth", "status"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := decode[authOutput](t, h.stdout)
	if !out.Authenticated || out.User.ID != "soli" || out.User.DisplayName != "Soli" || len(out.Scopes) != 3 || len(out.MissingScopes) != 0 || out.ExpiresInSeconds != 3600 || !out.RefreshTokenStored {
		t.Fatalf("status = %+v", out)
	}
	for _, secret := range []string{"access-1", "refresh-1", "csecret"} {
		if strings.Contains(h.stdout.String()+h.stderr.String(), secret) {
			t.Fatalf("output leaks %q", secret)
		}
	}
}

func TestAuthLoginHeadless(t *testing.T) {
	h := newHarness(t)
	// state は実行ごとに変わるため、code 単体を貼り戻す。
	h.app.Stdin = strings.NewReader("the-code\n")
	if code := h.run("auth", "login", "--headless"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := decode[authOutput](t, h.stdout)
	if !out.Authenticated || out.User.ID != "soli" || len(out.MissingScopes) != 0 {
		t.Fatalf("out = %+v", out)
	}
	if !strings.Contains(h.stderr.String(), "/authorize?") || !strings.Contains(h.stderr.String(), "client_id=cid") {
		t.Fatalf("stderr does not show the authorize URL: %s", h.stderr)
	}
	for _, secret := range []string{"access-1", "refresh-1", "csecret"} {
		if strings.Contains(h.stdout.String()+h.stderr.String(), secret) {
			t.Fatalf("output leaks %q", secret)
		}
	}
	fi, err := os.Stat(h.tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token perm = %o", fi.Mode().Perm())
	}
	saved, _ := (&token.Store{Path: h.tokenPath}).Load()
	if saved.RefreshToken != "refresh-1" || saved.Scope != fullScopes {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestAuthLoginRequiresClientCredentials(t *testing.T) {
	h := newHarness(t)
	h.env = map[string]string{envClientID: "cid"}
	h.expectError(t, h.run("auth", "login", "--headless"), apperr.ExitAuth, apperr.KindMissingClientCredentials)
}

func TestVersionIsJSON(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"version"}, {"--version"}} {
		if code := h.run(args...); code != 0 {
			t.Fatalf("exit %d", code)
		}
		v := decode[map[string]string](t, h.stdout)
		if v["version"] == "" {
			t.Fatalf("version = %v", v)
		}
	}
}

func TestPlaylistItemsLimitOffsetFlags(t *testing.T) {
	h := newHarness(t)
	h.login(t)
	h.fake.items = []string{"spotify:track:" + testTrackID}
	if code := h.run("playlist", "items", testPlaylistID, "--limit", "1", "--offset", "0"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.stderr)
	}
	out := decode[itemsOutput](t, h.stdout)
	if out.Count != 1 || out.Items[0].Position != 0 {
		t.Fatalf("out = %+v", out)
	}
}
