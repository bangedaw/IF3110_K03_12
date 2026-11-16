package httpx

import (
	"net/http"
	"strconv"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

func Pagination(r *http.Request) (limit, offset int32) {
	limit = defaultLimit
	if n, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32); err == nil && n > 0 && n < maxLimit {
		limit = int32(n)
	} else if err == nil && n >= maxLimit {
		limit = maxLimit
	}
	if n, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32); err == nil && n >= 0 {
		offset = int32(n)
	}
	return limit, offset
}
