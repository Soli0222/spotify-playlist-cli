// Package login は認可コードの受け取り（ローカルコールバック / headless の貼り戻し）を担う。
package login

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
)

// NewState は CSRF 対策の state を生成する。
func NewState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", apperr.Wrap(apperr.KindInternal, err, "failed to generate state")
	}
	return hex.EncodeToString(b), nil
}

// ParsePasted は headless モードで貼り戻された文字列から認可コードを取り出す。
// リダイレクト先 URL 全体、クエリ文字列（code=...&state=...）、code 単体のいずれも受け付ける。
// state が含まれる場合は expectedState と一致することを確認する。
func ParsePasted(input, expectedState string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", apperr.New(apperr.KindInvalidArgument, "no authorization code was entered")
	}

	var q url.Values
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return "", apperr.Wrap(apperr.KindInvalidArgument, err, "invalid redirect URL")
		}
		q = u.Query()
	case strings.Contains(s, "code=") || strings.Contains(s, "error="):
		var err error
		q, err = url.ParseQuery(strings.TrimPrefix(s, "?"))
		if err != nil {
			return "", apperr.Wrap(apperr.KindInvalidArgument, err, "invalid query string")
		}
	default:
		if strings.ContainsAny(s, " \t&?=/") {
			return "", apperr.New(apperr.KindInvalidArgument, "input is neither a redirect URL nor an authorization code")
		}
		return s, nil
	}
	return codeFromQuery(q, expectedState, false)
}

func codeFromQuery(q url.Values, expectedState string, requireState bool) (string, error) {
	if e := q.Get("error"); e != "" {
		return "", apperr.New(apperr.KindAuthFailed, "authorization was not granted: %s", e)
	}
	state := q.Get("state")
	if state != "" || requireState {
		if state != expectedState {
			return "", apperr.New(apperr.KindAuthFailed, "state mismatch; the redirect does not belong to this login attempt").
				WithHint("run `spcli auth login` again and use the newly printed URL")
		}
	}
	code := q.Get("code")
	if code == "" {
		return "", apperr.New(apperr.KindInvalidArgument, "redirect URL does not contain a code parameter")
	}
	return code, nil
}

// ReadPasted は r から1行読み、認可コードを取り出す。
func ReadPasted(r io.Reader, expectedState string) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	switch {
	case err == nil:
	case errors.Is(err, io.EOF) && line != "":
		// 改行なしで終わった入力も受け付ける。
	case errors.Is(err, io.EOF):
		return "", apperr.New(apperr.KindInvalidArgument, "no input: paste the redirect URL (or code) into stdin")
	default:
		return "", apperr.Wrap(apperr.KindInternal, err, "failed to read input")
	}
	return ParsePasted(line, expectedState)
}

// WaitForCallback は redirectURI のホスト・ポートで待ち受け、コールバックから認可コードを受け取る。
// ready は待ち受け開始後に呼ばれる（認可 URL の表示やブラウザ起動に使う）。
func WaitForCallback(ctx context.Context, redirectURI, expectedState string, timeout time.Duration, ready func()) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", apperr.Wrap(apperr.KindInternal, err, "invalid redirect URI")
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return "", apperr.Wrap(apperr.KindInternal, err, "failed to listen on %s", u.Host).
			WithHint("free the port, or use `spcli auth login --headless`")
	}
	return serveCallback(ctx, ln, u.Path, expectedState, timeout, ready)
}

type result struct {
	code string
	err  error
}

func serveCallback(ctx context.Context, ln net.Listener, path, expectedState string, timeout time.Duration, ready func()) (string, error) {
	results := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		code, err := codeFromQuery(r.URL.Query(), expectedState, true)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<!doctype html><title>spcli</title><p>Authorization failed: %s</p><p>You can close this window.</p>", html.EscapeString(err.Error()))
		} else {
			_, _ = io.WriteString(w, "<!doctype html><title>spcli</title><p>Authorization complete. You can close this window and return to the terminal.</p>")
		}
		select {
		case results <- result{code: code, err: err}:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if ready != nil {
		ready()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-results:
		return r.code, r.err
	case <-timer.C:
		return "", apperr.New(apperr.KindAuthFailed, "timed out after %s waiting for the authorization callback", timeout).
			WithHint("use `spcli auth login --headless` when the browser cannot reach this machine")
	case <-ctx.Done():
		return "", apperr.Wrap(apperr.KindAuthFailed, ctx.Err(), "login cancelled")
	}
}
