package speech

import (
	"context"
	"errors"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/language"
	"strings"
	"testing"
)

type contextGenerator struct {
	generateFunc
	failed bool
	calls  int
}

func (g *contextGenerator) Prepare(ctx context.Context, m []conversation.Message) ([]conversation.Message, conversation.ContextUsage, error) {
	g.calls++
	usage := conversation.ContextUsage{Limit: 4096, PromptTokens: 400, Reserved: 320}
	if g.calls == 1 {
		usage.DroppedMessages = 2
		m = append([]conversation.Message{m[0]}, m[3:]...)
	}
	return m, usage, nil
}
func (g *contextGenerator) Compact(ctx context.Context, m []conversation.Message, previous string) (string, error) {
	if len(m) != 2 || m[0].Text != "I am Alice" {
		return "", errors.New("incorrect dropped exchanges")
	}
	if g.failed {
		return "", errors.New("summary unavailable")
	}
	return "User is Alice. Likes quiet conversations.", nil
}

func TestCompactionRecountsAndFallsBackVisibly(t *testing.T) {
	for _, fail := range []bool{false, true} {
		g := &contextGenerator{failed: fail}
		d := New(nil, g, nil)
		turn := conversation.Turn{Preferences: language.Preferences{Reply: "en", Fallback: "en"}, History: []conversation.Message{{Role: "user", Text: "I am Alice"}, {Role: "assistant", Text: "Hello Alice."}, {Role: "user", Text: "Recent question"}, {Role: "assistant", Text: "Recent reply"}}}
		var events []conversation.Event
		messages, err := d.prepareContext(context.Background(), turn, "Current question", "en", func(e conversation.Event) error { events = append(events, e); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 4 || messages[len(messages)-1].Text != "Current question" {
			t.Fatal(messages)
		}
		var summary string
		var warning bool
		for _, e := range events {
			if e.Kind == "context" {
				summary = e.Summary
				if e.Context.DroppedMessages != 2 {
					t.Fatal(e.Context)
				}
			}
			if strings.Contains(e.Text, "Compaction failed") {
				warning = true
			}
		}
		if !fail && (summary == "" || !strings.Contains(messages[0].Text, "User is Alice") || g.calls != 2) {
			t.Fatal(summary, messages, g.calls)
		}
		if fail && (!warning || summary != "" || g.calls != 1) {
			t.Fatal(events, g.calls)
		}
	}
}

func TestPostHistorySurvivesContextCompaction(t *testing.T) {
	g := &contextGenerator{}
	d := New(nil, g, nil)
	turn := conversation.Turn{Preferences: language.Preferences{Reply: "en", Fallback: "en", PostHistory: "Stay warm."}, History: []conversation.Message{{Role: "user", Text: "I am Alice"}, {Role: "assistant", Text: "Hello Alice."}, {Role: "user", Text: "Recent"}, {Role: "assistant", Text: "Reply"}}}
	prepared, err := d.prepareContext(context.Background(), turn, "Current", "en", func(conversation.Event) error { return nil })
	if err != nil || prepared[0].PostHistory != "Stay warm." {
		t.Fatal(prepared, err)
	}
}
