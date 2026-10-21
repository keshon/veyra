package llamacpp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/keshon/veyra/internal/conversation"
)

//go:embed prompts/compact.txt
var compactPrompt string

// Compact is an internal text-only call to the same model. It never emits speech
// and never accepts partial output as a successful memory summary.
func (c *Client) Compact(parent context.Context, history []conversation.Message, previous string) (string, error) {
	if c.sampling.ContextTokens == 0 || len(history) == 0 || len(history)%2 != 0 {
		return "", fmt.Errorf("compaction requires complete exchanges and a configured context")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	summaryClient := *c
	summaryClient.sampling.MaxTokens = 256
	summaryClient.sampling.Temperature = 0
	summaryClient.sampling.TopP = 1
	summaryClient.sampling.RepeatPenalty = 1.1
	summaryClient.jsonSummary = true
	budget := c.sampling.ContextTokens - 320
	summary := previous
	for batch := 0; len(history) > 0; batch++ {
		if batch >= 4 {
			return "", fmt.Errorf("compaction exceeded four bounded summary calls")
		}
		request := func(pairs int) []conversation.Message {
			data, _ := json.Marshal(map[string]any{"previous_summary": summary, "exchanges": history[:pairs*2]})
			return []conversation.Message{{Role: "system", Text: strings.TrimSpace(compactPrompt)}, {Role: "user", Text: string(data)}}
		}
		n, err := c.countPrompt(ctx, request(1))
		if err != nil {
			return "", err
		}
		if n > budget {
			return "", fmt.Errorf("an exchange is too large for bounded compaction")
		}
		low, high := 1, len(history)/2
		for low < high {
			mid := low + (high-low+1)/2
			n, err := c.countPrompt(ctx, request(mid))
			if err != nil {
				return "", err
			}
			if n <= budget {
				low = mid
			} else {
				high = mid - 1
			}
		}
		var output strings.Builder
		if err := summaryClient.Generate(ctx, request(low), func(delta string) error {
			if output.Len()+len(delta) > 8192 {
				return fmt.Errorf("summary output exceeds limit")
			}
			output.WriteString(delta)
			return nil
		}); err != nil {
			return "", err
		}
		var decoded struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal([]byte(output.String()), &decoded); err != nil {
			return "", fmt.Errorf("invalid compaction JSON: %w", err)
		}
		summary = strings.TrimSpace(decoded.Summary)
		if summary == "" || len(summary) > 4096 || strings.Contains(summary, "<think>") {
			return "", fmt.Errorf("invalid compaction summary")
		}
		history = history[low*2:]
	}
	return summary, nil
}
