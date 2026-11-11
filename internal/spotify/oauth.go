package spotify

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
)

const (
	// DefaultAccountsBaseURL は Accounts サービスのベース URL。
	DefaultAccountsBaseURL = "https://accounts.spotify.com"
	// RedirectURI は Spotify Developer Dashboard に登録するリダイレクト URI。
	RedirectURI = "http://127.0.0.1:8765/callback"
)

// Scopes は CLI が要求するスコープ。
var Scopes = []string{
	"playlist-read-private",
	"playlist-modify-public",
	"playlist-modify-private",
}

// OAuth は Authorization Code フロー（機密クライアント）の設定。
type OAuth struct {
	ClientID        string
	ClientSecret    string
	RedirectURI     string
	Scopes          []string
	AccountsBaseURL string
	Transport       *Transport
	// Now はテストで差し替えるための時計。
	Now func() time.Time
}

// TokenResponse はトークンエンドポイントの応答を有効期限付きで表したもの。
type TokenResponse struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	Scope        string
	ExpiresAt    time.Time
}

// AuthorizeURL は利用者がブラウザで開く認可 URL を返す。
func (o *OAuth) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", o.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", o.redirectURI())
	q.Set("scope", strings.Join(o.scopes(), " "))
	q.Set("state", state)
	// 既に許可済みでも同意画面を出し、スコープを確実に付与し直す。
	q.Set("show_dialog", "true")
	return o.baseURL() + "/authorize?" + q.Encode()
}

// Exchange は認可コードをトークンに交換する。
func (o *OAuth) Exchange(ctx context.Context, code string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.redirectURI())
	return o.token(ctx, form)
}

// Refresh はリフレッシュトークンでアクセストークンを更新する。
// 応答に新しいリフレッシュトークンが含まれない場合は RefreshToken を空で返す。
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return o.token(ctx, form)
}

func (o *OAuth) token(ctx context.Context, form url.Values) (*TokenResponse, error) {
	if o.ClientID == "" || o.ClientSecret == "" {
		return nil, apperr.New(apperr.KindMissingClientCredentials, "SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET must be set")
	}
	endpoint := o.baseURL() + "/api/token"
	encoded := form.Encode()

	resp, err := o.transport().do(ctx, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(o.ClientID, o.ClientSecret)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		return req, nil
	}, classifyAccountsError)
	if err != nil {
		return nil, err
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(resp.Body, &body); err != nil {
		return nil, err
	}
	if body.AccessToken == "" {
		return nil, apperr.New(apperr.KindAuthFailed, "token response did not contain an access token")
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	return &TokenResponse{
		AccessToken:  body.AccessToken,
		TokenType:    body.TokenType,
		RefreshToken: body.RefreshToken,
		Scope:        body.Scope,
		ExpiresAt:    now().Add(time.Duration(body.ExpiresIn) * time.Second),
	}, nil
}

func (o *OAuth) baseURL() string {
	if o.AccountsBaseURL != "" {
		return strings.TrimRight(o.AccountsBaseURL, "/")
	}
	return DefaultAccountsBaseURL
}

func (o *OAuth) redirectURI() string {
	if o.RedirectURI != "" {
		return o.RedirectURI
	}
	return RedirectURI
}

func (o *OAuth) scopes() []string {
	if len(o.Scopes) > 0 {
		return o.Scopes
	}
	return Scopes
}

func (o *OAuth) transport() *Transport {
	if o.Transport != nil {
		return o.Transport
	}
	return NewTransport()
}

// MissingScopes は granted（空白区切り）に含まれない必須スコープを返す。
func MissingScopes(granted string) []string {
	have := map[string]bool{}
	for _, s := range strings.Fields(granted) {
		have[s] = true
	}
	missing := []string{}
	for _, s := range Scopes {
		if !have[s] {
			missing = append(missing, s)
		}
	}
	return missing
}
