package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
)

func doJSON(s *Server, method, path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func strconvFormat(i int64) string { return strconv.FormatInt(i, 10) }
