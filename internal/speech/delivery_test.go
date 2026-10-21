package speech

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
)

func TestDeliveryStreamEveryTokenBoundary(t *testing.T) {
	raw := "Well, [cue: chuckle]you caught me. [delivery: unsupported]Try [this]."
	want := "Well, you caught me. Try [this]."
	for width := 1; width <= len(raw); width++ {
		var parser deliveryStream
		var got strings.Builder
		for i := 0; i < len(raw); i += width {
			end := i + width
			if end > len(raw) {
				end = len(raw)
			}
			got.WriteString(parser.push(raw[i:end], false))
		}
		got.WriteString(parser.push("", true))
		if got.String() != want {
			t.Fatalf("width %d: %q", width, got.String())
		}
	}
	var parser deliveryStream
	if got := parser.push("Hello [cue: chuck", true); got != "Hello " {
		t.Fatal(got)
	}
	if got := parser.push("Array [", true); got != "Array [" {
		t.Fatal(got)
	}
}

func TestMultipleCueOffsetsAndUnknownFallback(t *testing.T) {
	u := parseUtterance("Hey [cue: unknown][cue: chuckle]you. [cue: laugh]Again.", []string{"chuckle", "laugh"})
	if u.Text != "Hey you. Again." || len(u.Cues) != 2 || u.Cues[0].Offset != 4 || u.Cues[1].Offset != 9 {
		t.Fatalf("%+v", u)
	}
	var raw strings.Builder
	for i := 0; i < 8; i++ {
		raw.WriteString("[cue: chuckle]Yes ")
	}
	if got := parseUtterance(raw.String(), []string{"chuckle"}); len(got.Cues) != 8 {
		t.Fatalf("cues silently capped: %+v", got)
	}
}

type expressiveRecorder struct {
	phrases []Utterance
	first   chan struct{}
}

func (r *expressiveRecorder) DeliveryCues() []string { return []string{"chuckle", "whispering"} }
func (r *expressiveRecorder) DeliveryInstructions() string {
	return "Choose whispering for a hushed delivery."
}
func (r *expressiveRecorder) Synthesize(context.Context, string, string, string) ([]byte, error) {
	panic("plain synthesis used")
}
func (r *expressiveRecorder) SynthesizeUtterance(_ context.Context, u Utterance, _, _ string) ([]byte, error) {
	r.phrases = append(r.phrases, u)
	if len(r.phrases) == 1 {
		close(r.first)
	}
	return []byte("audio"), nil
}

func TestDeliveryStreamsAndKeepsTranscriptClean(t *testing.T) {
	r := &expressiveRecorder{first: make(chan struct{})}
	g := generateFunc(func(ctx context.Context, m []conversation.Message, emit func(string) error) error {
		if !strings.Contains(m[0].Text, "chuckle") || !strings.Contains(m[0].Text, "hushed delivery") || strings.Contains(m[0].Text, "three per reply") {
			t.Fatal("missing delivery instructions")
		}
		for _, part := range []string{"[chu", "ckles][whisp", "ering]You caught me. "} {
			if err := emit(part); err != nil {
				return err
			}
		}
		select {
		case <-r.first:
		case <-ctx.Done():
			return ctx.Err()
		}
		return emit("Let's try again.")
	})
	d := New(nil, g, r)
	var visible strings.Builder
	turn := conversation.Turn{Input: conversation.Input{Text: "Hello"}, Preferences: language.Preferences{Reply: "en", Fallback: "en"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if err := d.Respond(ctx, turn, func(e conversation.Event) error {
		if e.Kind == "text" {
			visible.WriteString(e.Text)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visible.String() != "You caught me. Let's try again." {
		t.Fatal(visible.String())
	}
	if len(r.phrases) != 2 || len(r.phrases[0].Cues) != 2 || len(r.phrases[1].Cues) != 0 {
		t.Fatal(r.phrases)
	}
}

func TestStageDirectionsDoNotLeakAtAnyTokenBoundary(t *testing.T) {
	raw := "[clears throat]Falling for an AI. [sighs]Think it through. [leans in]Tell her. [1] [this]"
	want := "Falling for an AI. Think it through. Tell her. [1] [this]"
	for width := 1; width <= len(raw); width++ {
		var parser deliveryStream
		var got strings.Builder
		for i := 0; i < len(raw); i += width {
			end := i + width
			if end > len(raw) {
				end = len(raw)
			}
			got.WriteString(parser.push(raw[i:end], false))
		}
		got.WriteString(parser.push("", true))
		if got.String() != want {
			t.Fatalf("width %d: %q", width, got.String())
		}
	}
	u := parseUtterance(raw, []string{"clear throat", "sigh"})
	if u.Text != want || len(u.Cues) != 2 || u.Cues[0].Name != "clear throat" || u.Cues[1].Name != "sigh" {
		t.Fatalf("%+v", u)
	}
}

type structuredGenerator struct {
	generateFunc
	first <-chan struct{}
}

func (g structuredGenerator) GenerateSpeech(ctx context.Context, m []conversation.Message, cues []string, emit func(Utterance) error) error {
	if !strings.Contains(m[0].Text, "JSON array") {
		return fmt.Errorf("missing structured contract")
	}
	if err := emit(Utterance{Text: "The secret.", Cues: []Cue{{Name: "whispering", Offset: 0}}}); err != nil {
		return err
	}
	select {
	case <-g.first:
	case <-ctx.Done():
		return ctx.Err()
	}
	return emit(Utterance{Text: "Normal again."})
}
func TestStructuredDialogueKeepsMetadataOutOfText(t *testing.T) {
	r := &expressiveRecorder{first: make(chan struct{})}
	g := structuredGenerator{generateFunc: func(context.Context, []conversation.Message, func(string) error) error {
		panic("unconstrained generator used")
	}, first: r.first}
	d := New(nil, g, r)
	var visible strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	turn := conversation.Turn{Input: conversation.Input{Text: "Whisper a secret"}, Preferences: language.Preferences{Reply: "en", Fallback: "en"}}
	if err := d.Respond(ctx, turn, func(e conversation.Event) error {
		if e.Kind == "text" {
			visible.WriteString(e.Text)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visible.String() != "The secret. Normal again. " || len(r.phrases) != 2 || r.phrases[0].Cues[0].Name != "whispering" {
		t.Fatalf("%q %+v", visible.String(), r.phrases)
	}
}
