// Package cli は spcli のコマンド定義と入出力を担う。
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
	"github.com/soli0222/spotify-playlist-cli/internal/spotify"
	"github.com/soli0222/spotify-playlist-cli/internal/token"
)

// Version はビルド時に -ldflags で埋め込む。
var Version = "dev"

const (
	envClientID     = "SPOTIFY_CLIENT_ID"
	envClientSecret = "SPOTIFY_CLIENT_SECRET"

	defaultCallbackTimeout = 5 * time.Minute
)

// App はコマンドが使う入出力と外部依存。テストで差し替える。
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string

	// TokenPath が空なら token.DefaultPath() を使う。
	TokenPath       string
	APIBaseURL      string
	AccountsBaseURL string
	Transport       *spotify.Transport
	OpenBrowser     func(url string) error
	CallbackTimeout time.Duration
	Now             func() time.Time
}

// DefaultApp は実行環境の標準入出力と環境変数を使う App を返す。
func DefaultApp() *App {
	return &App{
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		Getenv:          os.Getenv,
		Transport:       spotify.NewTransport(),
		OpenBrowser:     openBrowser,
		CallbackTimeout: defaultCallbackTimeout,
		Now:             time.Now,
	}
}

// NewRootCmd はルートコマンドを作る。
func NewRootCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "spcli",
		Short:         "Spotify Web API のプレイリスト操作だけを行う薄い CLI",
		Long:          "Spotify Web API のプレイリスト操作だけを行う薄い CLI。\n正常時は JSON を stdout に、エラー時は JSON を stderr に出力し、非0で終了する。",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.SetVersionTemplate(`{"version":"{{.Version}}"}` + "\n")
	cmd.SetIn(app.Stdin)
	cmd.SetOut(app.Stdout)
	cmd.SetErr(app.Stderr)
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return apperr.New(apperr.KindUsage, "%s", err.Error())
	})

	cmd.AddCommand(newAuthCmd(app))
	cmd.AddCommand(newPlaylistCmd(app))
	cmd.AddCommand(newVersionCmd(app))
	return cmd
}

// Run はコマンドを実行し、終了コードを返す。エラーは JSON で stderr に出力する。
func Run(ctx context.Context, app *App, args []string) int {
	cmd := NewRootCmd(app)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return apperr.ExitOK
	}
	e := apperr.As(err)
	if e == nil {
		// cobra の引数・サブコマンドの検証エラーなど。
		e = apperr.New(apperr.KindUsage, "%s", err.Error()).WithHint("run `spcli --help` for usage")
	}
	writeError(app.Stderr, e)
	return e.Kind.ExitCode()
}

// Execute は main から呼ばれる。
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	app := DefaultApp()
	spotify.UserAgent = "spcli/" + Version
	code := Run(ctx, app, os.Args[1:])
	stop()
	os.Exit(code)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return apperr.Wrap(apperr.KindInternal, err, "failed to write output")
	}
	return nil
}

func writeError(w io.Writer, e *apperr.Error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"error": e}); err != nil {
		_, _ = fmt.Fprintf(w, `{"error":{"kind":"internal","message":%q}}`+"\n", e.Error())
	}
}

func (a *App) getenv(key string) string {
	if a.Getenv == nil {
		return os.Getenv(key)
	}
	return a.Getenv(key)
}

func (a *App) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

func (a *App) tokenStore() (*token.Store, error) {
	path := a.TokenPath
	if path == "" {
		p, err := token.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return &token.Store{Path: path}, nil
}

func (a *App) oauth() *spotify.OAuth {
	return &spotify.OAuth{
		ClientID:        a.getenv(envClientID),
		ClientSecret:    a.getenv(envClientSecret),
		RedirectURI:     spotify.RedirectURI,
		Scopes:          spotify.Scopes,
		AccountsBaseURL: a.AccountsBaseURL,
		Transport:       a.Transport,
		Now:             a.now,
	}
}

func (a *App) tokenManager() (*token.Manager, error) {
	store, err := a.tokenStore()
	if err != nil {
		return nil, err
	}
	return &token.Manager{Store: store, Refresher: a.oauth(), Now: a.now}, nil
}

func (a *App) apiClient(source spotify.TokenSource) *spotify.Client {
	return &spotify.Client{BaseURL: a.APIBaseURL, Token: source, Transport: a.Transport}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
