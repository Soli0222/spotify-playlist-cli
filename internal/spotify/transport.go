// Package spotify は Spotify Web API と Accounts サービスを直接叩く薄いクライアント。
package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/soli0222/spotify-playlist-cli/internal/apperr"
)

const (
	// DefaultMaxRetries は 429 を受けたときの最大リトライ回数。
	DefaultMaxRetries = 3
	// DefaultMaxRetryWait は Retry-After に従って待つ上限。これを超える指定は待たずにエラーにする。
	DefaultMaxRetryWait = 60 * time.Second
	// defaultRetryWait は Retry-After が無い 429 のときの待ち時間。
	defaultRetryWait = 1 * time.Second

	maxResponseBytes = 10 << 20
)

// UserAgent はリクエストに付ける User-Agent。
var UserAgent = "spcli"

// Transport は 429 のリトライとエラーの分類を担う。
type Transport struct {
	HTTPClient   *http.Client
	MaxRetries   int
	MaxRetryWait time.Duration
	// Sleep はテストで差し替えるための待機関数。
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewTransport は既定値の Transport を返す。
func NewTransport() *Transport {
	return &Transport{
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		MaxRetries:   DefaultMaxRetries,
		MaxRetryWait: DefaultMaxRetryWait,
		Sleep:        sleepContext,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// response は読み終えたレスポンス。
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// errorClassifier はステータスコードと本文からエラーを作る。
type errorClassifier func(status int, header http.Header, body []byte) *apperr.Error

// do は newReq でリクエストを作って送り、2xx 以外は classify でエラーにする。
// 429 のときだけ Retry-After に従って待ち、MaxRetries 回までリトライする。
func (t *Transport) do(ctx context.Context, newReq func(ctx context.Context) (*http.Request, error), classify errorClassifier) (*response, error) {
	client := t.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	sleep := t.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	for attempt := 0; ; attempt++ {
		req, err := newReq(ctx)
		if err != nil {
			return nil, apperr.Wrap(apperr.KindInternal, err, "failed to build request")
		}
		req.Header.Set("User-Agent", UserAgent)

		resp, err := client.Do(req)
		if err != nil {
			return nil, apperr.Wrap(apperr.KindNetworkError, err, "request to %s failed", req.URL.Host)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, apperr.Wrap(apperr.KindNetworkError, readErr, "failed to read response from %s", req.URL.Host)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return &response{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait, ok := retryAfter(resp.Header)
			if !ok {
				wait = defaultRetryWait
			}
			maxWait := t.MaxRetryWait
			if maxWait <= 0 {
				maxWait = DefaultMaxRetryWait
			}
			if attempt < t.MaxRetries && wait <= maxWait {
				if err := sleep(ctx, wait); err != nil {
					return nil, apperr.Wrap(apperr.KindNetworkError, err, "interrupted while waiting for rate limit")
				}
				continue
			}
			e := classify(resp.StatusCode, resp.Header, body)
			e.Kind = apperr.KindRateLimited
			e.SetDetail("retries", attempt)
			if ok {
				e.SetDetail("retry_after_seconds", int(wait.Seconds()))
			}
			if wait > maxWait {
				e.SetHint(fmt.Sprintf("Retry-After (%ds) exceeds the wait limit (%ds); retry later", int(wait.Seconds()), int(maxWait.Seconds())))
			} else {
				e.SetHint("rate limit persisted after retries; retry later")
			}
			return nil, e
		}

		return nil, classify(resp.StatusCode, resp.Header, body)
	}
}

// retryAfter は Retry-After ヘッダ（秒数または HTTP-date）を解釈する。
func retryAfter(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(v); err == nil {
		d := time.Until(at)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// classifyAPIError は Web API（api.spotify.com）のエラーを分類する。
// 本文は {"error":{"status":403,"message":"..."}} 形式。
func classifyAPIError(status int, _ http.Header, body []byte) *apperr.Error {
	msg := apiErrorMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}

	var e *apperr.Error
	switch {
	case status == http.StatusUnauthorized:
		e = apperr.New(apperr.KindUnauthorized, "%s", msg).
			WithHint("access token is invalid, expired or revoked; run `spcli auth login`")
	case status == http.StatusForbidden && IsInsufficientScopeMessage(msg):
		e = apperr.New(apperr.KindInsufficientScope, "%s", msg).
			WithHint("the token lacks a required scope; run `spcli auth login` to re-authorize (required: " + strings.Join(Scopes, " ") + ")")
	case status == http.StatusForbidden:
		e = apperr.New(apperr.KindForbidden, "%s", msg).
			WithHint("not a scope problem: check that the playlist is owned by (or collaborative with) the authenticated user, that the user is registered in the app's User Management (Development Mode), and that the app owner's Premium subscription is active")
	case status == http.StatusNotFound:
		e = apperr.New(apperr.KindNotFound, "%s", msg)
	default:
		e = apperr.New(apperr.KindAPIError, "%s", msg)
	}
	e.Status = status
	return e
}

// IsInsufficientScopeMessage は 403 の本文がスコープ不足を表すかを返す。
func IsInsufficientScopeMessage(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "insufficient client scope")
}

func apiErrorMessage(body []byte) string {
	var withObject struct {
		Error struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &withObject); err == nil && withObject.Error.Message != "" {
		return withObject.Error.Message
	}
	// Accounts 形式が返ってきた場合にも備える。
	if m := accountsErrorMessage(body); m != "" {
		return m
	}
	return truncate(strings.TrimSpace(string(body)), 300)
}

// classifyAccountsError は Accounts サービス（accounts.spotify.com）のエラーを分類する。
// 本文は {"error":"invalid_grant","error_description":"..."} 形式。
func classifyAccountsError(status int, _ http.Header, body []byte) *apperr.Error {
	msg := accountsErrorMessage(body)
	if msg == "" {
		msg = truncate(strings.TrimSpace(string(body)), 300)
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	var e *apperr.Error
	if status >= 500 {
		e = apperr.New(apperr.KindAPIError, "token endpoint error: %s", msg)
	} else {
		e = apperr.New(apperr.KindAuthFailed, "token request rejected: %s", msg).
			WithHint("check SPOTIFY_CLIENT_ID / SPOTIFY_CLIENT_SECRET; if the refresh token was revoked or expired, run `spcli auth login`")
	}
	e.Status = status
	return e
}

func accountsErrorMessage(body []byte) string {
	var v struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.Error == "" {
		return ""
	}
	if v.ErrorDescription != "" {
		return v.Error + ": " + v.ErrorDescription
	}
	return v.Error
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func decodeJSON(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return apperr.Wrap(apperr.KindAPIError, err, "unexpected response body")
	}
	return nil
}
