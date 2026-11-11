package token

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
	"github.com/soli0222/spotify-playlist-cli/internal/spotify"
)

func TestDefaultPathUsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "spotify-playlist-cli", "token.json"); got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestDefaultPathFallsBackToDotConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "spotify-playlist-cli", "token.json"); got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestSaveLoadAndPermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "token.json")
	s := &Store{Path: path}
	in := &Token{AccessToken: "a", RefreshToken: "r", Scope: "x y", ExpiresAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("perm = %o, want 600", perm)
		}
		di, _ := os.Stat(filepath.Dir(path))
		if perm := di.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir perm = %o, want 700", perm)
		}
	}
	out, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if out.AccessToken != "a" || out.RefreshToken != "r" || !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Fatalf("out = %+v", out)
	}

	// 既存ファイルのパーミッションが緩くても、保存し直すと 0600 になる。
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, 0o644)
		if err := s.Save(in); err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Stat(path)
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("perm after resave = %o", perm)
		}
	}
}

func TestLoadMissingIsNotAuthenticated(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "token.json")}
	_, err := s.Load()
	if e := apperr.As(err); e == nil || e.Kind != apperr.KindNotAuthenticated {
		t.Fatalf("err = %v", err)
	}
}

type fakeRefresher struct {
	calls int
	resp  *spotify.TokenResponse
	err   error
}

func (f *fakeRefresher) Refresh(_ context.Context, rt string) (*spotify.TokenResponse, error) {
	f.calls++
	return f.resp, f.err
}

func TestManagerUsesValidToken(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	s := &Store{Path: filepath.Join(t.TempDir(), "token.json")}
	_ = s.Save(&Token{AccessToken: "a", RefreshToken: "r", ExpiresAt: now.Add(10 * time.Minute)})
	f := &fakeRefresher{}
	m := &Manager{Store: s, Refresher: f, Now: func() time.Time { return now }}
	at, err := m.AccessToken(context.Background())
	if err != nil || at != "a" || f.calls != 0 {
		t.Fatalf("at=%q err=%v calls=%d", at, err, f.calls)
	}
}

func TestManagerRefreshesExpiredTokenAndKeepsRefreshToken(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	s := &Store{Path: filepath.Join(t.TempDir(), "token.json")}
	_ = s.Save(&Token{AccessToken: "old", RefreshToken: "r", Scope: "s1", ExpiresAt: now.Add(30 * time.Second)})
	f := &fakeRefresher{resp: &spotify.TokenResponse{AccessToken: "new", TokenType: "Bearer", ExpiresAt: now.Add(time.Hour)}}
	m := &Manager{Store: s, Refresher: f, Now: func() time.Time { return now }}
	at, err := m.AccessToken(context.Background())
	if err != nil || at != "new" || f.calls != 1 {
		t.Fatalf("at=%q err=%v calls=%d", at, err, f.calls)
	}
	saved, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "new" || saved.RefreshToken != "r" || saved.Scope != "s1" || !saved.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("saved = %+v", saved)
	}
	// 2回目はキャッシュを使い、再リフレッシュしない。
	if _, err := m.AccessToken(context.Background()); err != nil || f.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, f.calls)
	}
}

func TestManagerStoresRotatedRefreshToken(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	s := &Store{Path: filepath.Join(t.TempDir(), "token.json")}
	_ = s.Save(&Token{AccessToken: "old", RefreshToken: "r1", ExpiresAt: now.Add(-time.Hour)})
	f := &fakeRefresher{resp: &spotify.TokenResponse{AccessToken: "new", RefreshToken: "r2", Scope: "s2", ExpiresAt: now.Add(time.Hour)}}
	m := &Manager{Store: s, Refresher: f, Now: func() time.Time { return now }}
	if _, err := m.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Load()
	if saved.RefreshToken != "r2" || saved.Scope != "s2" {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestManagerRefreshFailure(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	s := &Store{Path: filepath.Join(t.TempDir(), "token.json")}
	_ = s.Save(&Token{AccessToken: "old", RefreshToken: "r", ExpiresAt: now.Add(-time.Hour)})
	f := &fakeRefresher{err: apperr.New(apperr.KindAuthFailed, "invalid_grant")}
	m := &Manager{Store: s, Refresher: f, Now: func() time.Time { return now }}
	_, err := m.Token(context.Background())
	if e := apperr.As(err); e == nil || e.Kind != apperr.KindAuthFailed {
		t.Fatalf("err = %v", err)
	}
}
