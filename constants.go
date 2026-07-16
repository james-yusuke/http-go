package httpgo

import "net/http"

const (
	MethodGet     = "GET"
	MethodHead    = "HEAD"
	MethodPost    = "POST"
	MethodPut     = "PUT"
	MethodPatch   = "PATCH"
	MethodDelete  = "DELETE"
	MethodConnect = "CONNECT"
	MethodOptions = "OPTIONS"
	MethodTrace   = "TRACE"
)

const (
	StatusContinue                    = 100
	StatusSwitchingProtocols          = 101
	StatusOK                          = 200
	StatusCreated                     = 201
	StatusNoContent                   = 204
	StatusMovedPermanently            = 301
	StatusFound                       = 302
	StatusSeeOther                    = 303
	StatusTemporaryRedirect           = 307
	StatusPermanentRedirect           = 308
	StatusBadRequest                  = 400
	StatusUnauthorized                = 401
	StatusForbidden                   = 403
	StatusNotFound                    = 404
	StatusMethodNotAllowed            = 405
	StatusRequestTimeout              = 408
	StatusRequestEntityTooLarge       = 413
	StatusExpectationFailed           = 417
	StatusRequestHeaderFieldsTooLarge = 431
	StatusInternalServerError         = 500
	StatusNotImplemented              = 501
	StatusServiceUnavailable          = 503
)

func StatusText(code int) string { return http.StatusText(code) }

var (
	ErrServerClosed = http.ErrServerClosed
	ErrNotSupported = http.ErrNotSupported
)

type Cookie = http.Cookie
type PushOptions = http.PushOptions
