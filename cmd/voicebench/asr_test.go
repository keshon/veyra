package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestASRServerAutoLanguageAndRequestIsolation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for name, want := range map[string]string{"language": "auto", "no_context": "true", "translate": "false", "no_language_probabilities": "true"} {
			if got := r.FormValue(name); got != want {
				t.Errorf("%s: got %q want %q", name, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"language":"russian","text":"Привет."}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "input.wav")
	if err := os.WriteFile(input, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	tx, err := transcribeHTTP(context.Background(), srv.URL, input, dir)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Result.Language != "ru" || tx.Transcription[0].Text != "Привет." {
		t.Fatalf("unexpected transcript: %+v", tx)
	}
}
