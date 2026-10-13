// Package apperr defines business errors that carry a stable code, an HTTP status
// and a message that is safe to show to end users (in the Mini App, the admin panel or Telegram).
package apperr

import (
	"errors"
	"net/http"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, msg string) *Error {
	return &Error{Code: code, Message: msg, Status: status}
}

func BadRequest(code, msg string) *Error { return New(http.StatusBadRequest, code, msg) }
func Conflict(code, msg string) *Error   { return New(http.StatusConflict, code, msg) }
func NotFound(code, msg string) *Error   { return New(http.StatusNotFound, code, msg) }
func Forbidden(msg string) *Error        { return New(http.StatusForbidden, "FORBIDDEN", msg) }

var (
	ErrUnauthorized  = New(http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required.")
	ErrForbidden     = Forbidden("You do not have permission to do this.")
	ErrNotRegistered = New(http.StatusForbidden, "NOT_REGISTERED", "You are not registered as an employee. Please contact the administrator.")
	ErrInactive      = New(http.StatusForbidden, "INACTIVE", "Your employee account is not active. Please contact the administrator.")
)

// As extracts an *Error; unknown errors become a generic 500 without leaking internals.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(http.StatusInternalServerError, "INTERNAL", "Something went wrong. Please try again.")
}
