package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
	"github.com/soli0222/spotify-playlist-cli/internal/login"
	"github.com/soli0222/spotify-playlist-cli/internal/spotify"
	"github.com/soli0222/spotify-playlist-cli/internal/token"
)

// authOutput は auth login / auth status の出力。トークンやシークレットの値は含めない。
type authOutput struct {
	Authenticated      bool       `json:"authenticated"`
	User               userOutput `json:"user"`
	Scopes             []string   `json:"scopes"`
	MissingScopes      []string   `json:"missing_scopes"`
	ExpiresAt          string     `json:"expires_at"`
	ExpiresInSeconds   int64      `json:"expires_in_seconds"`
	RefreshTokenStored bool       `json:"refresh_token_stored"`
	TokenFile          string     `json:"token_file"`
}

type userOutput struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func newAuthCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Spotify の認証",
	}
	cmd.AddCommand(newAuthLoginCmd(app))
	cmd.AddCommand(newAuthStatusCmd(app))
	return cmd
}

func newAuthLoginCmd(app *App) *cobra.Command {
	var headless bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authorization Code フローで認可し、トークンを保存する",
		Long: "Authorization Code フローで認可し、トークンを保存する。\n" +
			"通常モードは " + spotify.RedirectURI + " でコールバックを受けて自動完了する。\n" +
			"--headless では認可 URL を表示し、ブラウザで許可した後のリダイレクト先 URL（または code）を標準入力に貼り戻す。\n" +
			"案内は stderr、結果の JSON は stdout に出力する。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogin(cmd.Context(), app, headless)
		},
	}
	cmd.Flags().BoolVar(&headless, "headless", false, "ブラウザのない環境向け。リダイレクト先 URL（または code）を貼り戻して完了する")
	return cmd
}

func runAuthLogin(ctx context.Context, app *App, headless bool) error {
	oauth := app.oauth()
	if oauth.ClientID == "" || oauth.ClientSecret == "" {
		return apperr.New(apperr.KindMissingClientCredentials, "%s and %s must be set", envClientID, envClientSecret).
			WithHint("set them to the Client ID / Client Secret of the spotify-playlist-cli app in the Spotify Developer Dashboard")
	}
	store, err := app.tokenStore()
	if err != nil {
		return err
	}
	state, err := login.NewState()
	if err != nil {
		return err
	}
	authURL := oauth.AuthorizeURL(state)

	var code string
	if headless {
		_, _ = fmt.Fprintf(app.Stderr, "1. Open this URL in a browser and approve access:\n\n%s\n\n", authURL)
		_, _ = fmt.Fprintf(app.Stderr, "2. The browser is redirected to %s?code=... (the page may fail to load; that is expected).\n", spotify.RedirectURI)
		_, _ = fmt.Fprintf(app.Stderr, "3. Copy the full URL from the address bar (or just the code) and paste it here, then press Enter:\n> ")
		code, err = login.ReadPasted(app.Stdin, state)
		if err != nil {
			return err
		}
	} else {
		timeout := app.CallbackTimeout
		if timeout <= 0 {
			timeout = defaultCallbackTimeout
		}
		code, err = login.WaitForCallback(ctx, spotify.RedirectURI, state, timeout, func() {
			_, _ = fmt.Fprintf(app.Stderr, "Open this URL in a browser and approve access (waiting for %s):\n\n%s\n\n", spotify.RedirectURI, authURL)
			if app.OpenBrowser != nil {
				_ = app.OpenBrowser(authURL)
			}
		})
		if err != nil {
			return err
		}
	}

	resp, err := oauth.Exchange(ctx, code)
	if err != nil {
		return err
	}
	if resp.RefreshToken == "" {
		return apperr.New(apperr.KindAuthFailed, "token response did not contain a refresh token")
	}
	t := &token.Token{
		AccessToken:  resp.AccessToken,
		TokenType:    resp.TokenType,
		RefreshToken: resp.RefreshToken,
		Scope:        resp.Scope,
		ExpiresAt:    resp.ExpiresAt,
	}
	if err := store.Save(t); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(app.Stderr, "\nToken saved to %s\n", store.Path)

	client := app.apiClient(func(context.Context) (string, error) { return t.AccessToken, nil })
	me, err := client.Me(ctx)
	if err != nil {
		return err
	}
	return writeJSON(app.Stdout, buildAuthOutput(app, store.Path, t, me))
}

func newAuthStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "認証状態・アカウント・スコープ・有効期限を表示する（トークンの値は表示しない）",
		Long: "認証状態・アカウント・スコープ・有効期限を表示する（トークンの値は表示しない）。\n" +
			"アクセストークンが期限切れ間近ならリフレッシュしてから確認する。未認証の場合は stderr にエラーを出し、非0で終了する。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mgr, err := app.tokenManager()
			if err != nil {
				return err
			}
			if _, err := mgr.Token(ctx); err != nil {
				return err
			}
			me, err := app.apiClient(mgr.AccessToken).Me(ctx)
			if err != nil {
				return withScopeDetails(err, mgr.Current())
			}
			return writeJSON(app.Stdout, buildAuthOutput(app, mgr.Store.Path, mgr.Current(), me))
		},
	}
}

func buildAuthOutput(app *App, path string, t *token.Token, me *spotify.User) authOutput {
	scopes := strings.Fields(t.Scope)
	if scopes == nil {
		scopes = []string{}
	}
	expiresIn := int64(t.ExpiresAt.Sub(app.now()) / time.Second)
	if expiresIn < 0 {
		expiresIn = 0
	}
	return authOutput{
		Authenticated:      true,
		User:               userOutput{ID: me.ID, DisplayName: me.DisplayName},
		Scopes:             scopes,
		MissingScopes:      spotify.MissingScopes(t.Scope),
		ExpiresAt:          t.ExpiresAt.UTC().Format(time.RFC3339),
		ExpiresInSeconds:   expiresIn,
		RefreshTokenStored: t.RefreshToken != "",
		TokenFile:          path,
	}
}

// withScopeDetails は 403 系のエラーに、保存済みトークンのスコープ情報を添える。
func withScopeDetails(err error, t *token.Token) error {
	e := apperr.As(err)
	if e == nil || t == nil {
		return err
	}
	if e.Kind == apperr.KindInsufficientScope || e.Kind == apperr.KindForbidden {
		granted := strings.Fields(t.Scope)
		if granted == nil {
			granted = []string{}
		}
		e.SetDetail("granted_scopes", granted)
		e.SetDetail("missing_scopes", spotify.MissingScopes(t.Scope))
	}
	return e
}
