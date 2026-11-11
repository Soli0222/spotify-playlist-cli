// Package apperr は CLI が呼び出し側へ返すエラーの種類と終了コードを定義する。
package apperr

import (
	"errors"
	"fmt"
)

// Kind は呼び出し側が機械的に判別するためのエラー種別。
type Kind string

const (
	KindUsage                    Kind = "usage"
	KindInvalidArgument          Kind = "invalid_argument"
	KindMissingClientCredentials Kind = "missing_client_credentials"
	KindNotAuthenticated         Kind = "not_authenticated"
	KindUnauthorized             Kind = "unauthorized"
	KindAuthFailed               Kind = "auth_failed"
	KindInsufficientScope        Kind = "insufficient_scope"
	KindForbidden                Kind = "forbidden"
	KindNotFound                 Kind = "not_found"
	KindRateLimited              Kind = "rate_limited"
	KindAPIError                 Kind = "api_error"
	KindNetworkError             Kind = "network_error"
	KindInternal                 Kind = "internal"
)

// 終了コード。README の表と一致させる。
const (
	ExitOK                = 0
	ExitGeneral           = 1
	ExitUsage             = 2
	ExitAuth              = 3
	ExitInsufficientScope = 4
	ExitForbidden         = 5
	ExitNotFound          = 6
	ExitRateLimited       = 7
)

// ExitCode は種別に対応する終了コードを返す。
func (k Kind) ExitCode() int {
	switch k {
	case KindUsage, KindInvalidArgument:
		return ExitUsage
	case KindMissingClientCredentials, KindNotAuthenticated, KindUnauthorized, KindAuthFailed:
		return ExitAuth
	case KindInsufficientScope:
		return ExitInsufficientScope
	case KindForbidden:
		return ExitForbidden
	case KindNotFound:
		return ExitNotFound
	case KindRateLimited:
		return ExitRateLimited
	default:
		return ExitGeneral
	}
}

// Error は stderr に JSON として出力されるエラー。
type Error struct {
	Kind    Kind           `json:"kind"`
	Status  int            `json:"status,omitempty"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s (status %d): %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// New は種別とメッセージからエラーを作る。
func New(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Wrap は原因エラーを保持したまま種別付きのエラーを作る。
func Wrap(kind Kind, cause error, format string, args ...any) *Error {
	msg := fmt.Sprintf(format, args...)
	if cause != nil {
		msg = msg + ": " + cause.Error()
	}
	return &Error{Kind: kind, Message: msg, cause: cause}
}

// WithHint は対処方法を付与して e を返す（メソッドチェーン用）。
func (e *Error) WithHint(hint string) *Error {
	e.SetHint(hint)
	return e
}

// WithDetail は補足情報を付与して e を返す（メソッドチェーン用）。
func (e *Error) WithDetail(key string, value any) *Error {
	e.SetDetail(key, value)
	return e
}

// SetHint は対処方法を付与する。
func (e *Error) SetHint(hint string) {
	e.Hint = hint
}

// SetDetail は補足情報を付与する。
func (e *Error) SetDetail(key string, value any) {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
}

// As は err を *Error として取り出す。該当しない場合は nil を返す。
func As(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return nil
}
