package piper

import (
	"github.com/keshon/veyra/internal/conversation"
	"testing"
)

func TestVoiceSelectionNeverCrossesLanguage(t *testing.T) {
	s := New("", "", []Voice{{Voice: conversation.Voice{ID: "en-a", Language: "en", Default: true}, Model: "en.onnx"}, {Voice: conversation.Voice{ID: "ru-a", Language: "ru", Default: true}, Model: "ru-a.onnx"}, {Voice: conversation.Voice{ID: "ru-b", Language: "ru"}, Model: "ru-b.onnx"}})
	for _, tc := range []struct{ language, id, model string }{{"en", "", "en.onnx"}, {"ru", "", "ru-a.onnx"}, {"ru", "ru-b", "ru-b.onnx"}} {
		voice, err := s.selectVoice(tc.language, tc.id)
		if err != nil || voice.Model != tc.model {
			t.Fatal(tc, voice, err)
		}
	}
	if _, err := s.selectVoice("en", "ru-b"); err == nil {
		t.Fatal("wrong-language voice accepted")
	}
	if _, err := s.selectVoice("ru", "unknown"); err == nil {
		t.Fatal("unknown voice accepted")
	}
}
