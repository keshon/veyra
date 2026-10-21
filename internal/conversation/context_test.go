package conversation

import (
	"context"
	"strings"
	"testing"
)

func TestModelBudgetReplacesByteCutoffAndPrunesReportedExchanges(t *testing.T) {
	count := 0
	s := ready(t, &fake{respond: func(ctx context.Context, turn Turn, emit func(Event) error) error {
		count++
		if count == 2 && len(turn.History) != 2 {
			t.Fatal("large exchange lost", len(turn.History))
		}
		dropped := 0
		if count == 2 {
			dropped = 2
		}
		if count == 3 && (len(turn.History) != 2 || turn.History[0].Text != "second") {
			t.Fatal("discarded history returned", turn.History)
		}
		if err := emit(Event{Kind: "context", Context: &ContextUsage{Limit: 8192, PromptTokens: 1000, Reserved: 320, DroppedMessages: dropped}}); err != nil {
			return err
		}
		return emit(Event{Kind: "text", Text: strings.Repeat("я", 2000)})
	}})
	for _, input := range []string{"first", "second", "third"} {
		if err := s.Respond(context.Background(), Input{Text: input}, func(Event) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if s.Status().Context.Limit != 8192 {
		t.Fatal(s.Status())
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Context.Limit != 0 {
		t.Fatal("reset retained prompt usage")
	}
}
