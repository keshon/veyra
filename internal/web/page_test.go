package web

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedUIAssets(t *testing.T) {
	h := Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Veyra") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	paths := regexp.MustCompile(`(?:src|href)="(/assets/[^\"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(paths) < 2 {
		t.Fatal("missing compiled assets")
	}
	for _, p := range paths {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest("GET", "http://localhost"+p[1], nil))
		if out.Code != 200 || out.Body.Len() == 0 {
			t.Fatal(p[1], out.Code)
		}
	}
}
