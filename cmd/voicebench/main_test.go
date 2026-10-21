package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestPhraseAssemblyAcrossTokenBoundaries(t *testing.T) {
	var pending string
	var got []string
	for _, token := range []string{"Привет", ".", " Как ", "дела", "?"} {
		pending += token
		ready, rest := splitPhrases(pending, false)
		got = append(got, ready...)
		pending = rest
	}
	ready, _ := splitPhrases(pending, true)
	got = append(got, ready...)
	want := []string{"Привет.", "Как дела?"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDecimalDoesNotCommitPhrase(t *testing.T) {
	got, rest := splitPhrases("It costs 3.14 dollars", false)
	if len(got) != 0 || rest != "It costs 3.14 dollars" {
		t.Fatalf("split decimal: %q %q", got, rest)
	}
}

func TestGenerationRejectsIncompleteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n"))
	}))
	defer srv.Close()
	err := generate(context.Background(), srv.URL, "Hello", "en", func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "without DONE") {
		t.Fatalf("got %v", err)
	}
}
