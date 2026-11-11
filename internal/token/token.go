// Package token はトークンファイルの保存と、期限切れ前の自動リフレッシュを担う。
package token

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
	"github.com/soli0222/spotify-playlist-cli/internal/spotify"
)

const (
	appDirName = "spotify-playlist-cli"
	fileName   = "token.json"
	// refreshMargin より残り時間が短いアクセストークンは使う前に更新する。
	refreshMargin = 60 * time.Second
)

// Token はディスクに保存する内容。
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Scope        string    `json:"scope"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// DefaultPath は $XDG_CONFIG_HOME/spotify-playlist-cli/token.json
// （未設定なら ~/.config/spotify-playlist-cli/token.json）を返す。
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, appDirName, fileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", apperr.Wrap(apperr.KindInternal, err, "cannot determine home directory")
	}
	return filepath.Join(home, ".config", appDirName, fileName), nil
}

// Store はトークンファイルの読み書きを行う。
type Store struct {
	Path string
}

// Load はトークンを読み込む。ファイルが無ければ not_authenticated を返す。
func (s *Store) Load() (*Token, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, apperr.New(apperr.KindNotAuthenticated, "not authenticated: token file %s does not exist", s.Path).
			WithHint("run `spcli auth login` (or `spcli auth login --headless` on a remote host)")
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.KindInternal, err, "failed to read token file %s", s.Path)
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, apperr.Wrap(apperr.KindNotAuthenticated, err, "token file %s is corrupted", s.Path).
			WithHint("run `spcli auth login` again")
	}
	if t.RefreshToken == "" && t.AccessToken == "" {
		return nil, apperr.New(apperr.KindNotAuthenticated, "token file %s contains no token", s.Path).
			WithHint("run `spcli auth login` again")
	}
	return &t, nil
}

// Save はトークンをパーミッション 0600 で原子的に書き込む。
func (s *Store) Save(t *Token) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return apperr.Wrap(apperr.KindInternal, err, "failed to create config directory %s", dir)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return apperr.Wrap(apperr.KindInternal, err, "failed to encode token")
	}
	tmp, err := os.CreateTemp(dir, ".token-*.json")
	if err != nil {
		return apperr.Wrap(apperr.KindInternal, err, "failed to create temporary token file")
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return apperr.Wrap(apperr.KindInternal, err, "failed to set token file permission")
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		cleanup()
		return apperr.Wrap(apperr.KindInternal, err, "failed to write token file")
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return apperr.Wrap(apperr.KindInternal, err, "failed to write token file")
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		cleanup()
		return apperr.Wrap(apperr.KindInternal, err, "failed to save token file %s", s.Path)
	}
	return nil
}

// Refresher はリフレッシュトークンでアクセストークンを更新する。
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*spotify.TokenResponse, error)
}

// Manager は保存済みトークンを読み、必要なら更新して返す。
type Manager struct {
	Store     *Store
	Refresher Refresher
	Now       func() time.Time

	current *Token
}

// Current は最後に読み込んだ（または更新した）トークンを返す。
func (m *Manager) Current() *Token {
	return m.current
}

// Token は有効なトークンを返す。期限切れ間近なら更新して保存する。
func (m *Manager) Token(ctx context.Context) (*Token, error) {
	if m.current == nil {
		t, err := m.Store.Load()
		if err != nil {
			return nil, err
		}
		m.current = t
	}
	t := m.current
	if t.AccessToken != "" && m.now().Add(refreshMargin).Before(t.ExpiresAt) {
		return t, nil
	}
	if t.RefreshToken == "" {
		return nil, apperr.New(apperr.KindNotAuthenticated, "access token expired and no refresh token is stored").
			WithHint("run `spcli auth login` again")
	}
	if m.Refresher == nil {
		return nil, apperr.New(apperr.KindInternal, "no refresher configured")
	}
	resp, err := m.Refresher.Refresh(ctx, t.RefreshToken)
	if err != nil {
		if e := apperr.As(err); e != nil && e.Kind == apperr.KindMissingClientCredentials {
			e.Message = "access token expired; refreshing it requires SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET"
		}
		return nil, err
	}
	next := &Token{
		AccessToken:  resp.AccessToken,
		TokenType:    resp.TokenType,
		RefreshToken: t.RefreshToken,
		Scope:        t.Scope,
		ExpiresAt:    resp.ExpiresAt,
	}
	if resp.RefreshToken != "" {
		next.RefreshToken = resp.RefreshToken
	}
	if resp.Scope != "" {
		next.Scope = resp.Scope
	}
	if err := m.Store.Save(next); err != nil {
		return nil, err
	}
	m.current = next
	return next, nil
}

// AccessToken は spotify.TokenSource として使える。
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	t, err := m.Token(ctx)
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
