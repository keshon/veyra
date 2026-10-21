package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keshon/veyra/internal/conversation"
)

func nativeMessages(messages []conversation.Message) []map[string]string {
	native := make([]map[string]string, 0, len(messages))
	post := ""
	interrupted := false
	for _, m := range messages {
		interrupted = interrupted || m.Interrupted
	}
	for _, m := range messages {
		text := m.Text
		if m.Role == "system" && m.PostHistory != "" {
			post = m.PostHistory
		}
		if m.Role == "system" && interrupted {
			text += "\nSome earlier assistant replies were interrupted and may not have been heard in full. Respond to the latest user message; do not repeat interruption notices."
		}
		native = append(native, map[string]string{"role": m.Role, "content": text})
	}
	if post != "" {
		native = append(native, map[string]string{"role": "system", "content": post})
	}
	return native
}

// Prepare preserves the character instructions and latest input, discarding only
// complete oldest exchanges. Token counts include this model's chat template.
func (c *Client) Prepare(ctx context.Context, messages []conversation.Message) ([]conversation.Message, conversation.ContextUsage, error) {
	usage := conversation.ContextUsage{Limit: c.sampling.ContextTokens, Reserved: c.sampling.MaxTokens + 64}
	if usage.Limit == 0 {
		return messages, conversation.ContextUsage{}, nil
	}
	budget := usage.Limit - usage.Reserved
	if budget <= 0 {
		return nil, usage, fmt.Errorf("context %d cannot reserve %d response and safety tokens; increase -ctx or reduce -max-tokens", usage.Limit, usage.Reserved)
	}
	if len(messages) < 2 || messages[0].Role != "system" || messages[len(messages)-1].Role != "user" || (len(messages)-2)%2 != 0 {
		return nil, usage, fmt.Errorf("context requires system instructions, complete exchanges and a latest user message")
	}
	for i := 1; i < len(messages)-1; i += 2 {
		if messages[i].Role != "user" || messages[i+1].Role != "assistant" {
			return nil, usage, fmt.Errorf("context history contains an incomplete exchange")
		}
	}
	candidate := func(drop int) []conversation.Message {
		out := []conversation.Message{messages[0]}
		return append(out, messages[1+drop*2:]...)
	}
	tokens, err := c.countPrompt(ctx, messages)
	if err != nil {
		return nil, usage, err
	}
	if tokens <= budget {
		usage.PromptTokens = tokens
		return messages, usage, nil
	}
	pairs := (len(messages) - 2) / 2
	shortest := candidate(pairs)
	tokens, err = c.countPrompt(ctx, shortest)
	if err != nil {
		return nil, usage, err
	}
	if tokens > budget {
		return nil, usage, fmt.Errorf("character instructions and latest input need %d tokens; available prompt budget is %d. Increase -ctx, reduce -max-tokens, or shorten the character/input", tokens, budget)
	}
	// Keep the largest fitting suffix with logarithmically many tokenizer calls.
	low, high := 1, pairs
	for low < high {
		mid := low + (high-low)/2
		n, err := c.countPrompt(ctx, candidate(mid))
		if err != nil {
			return nil, usage, err
		}
		if n <= budget {
			high = mid
			tokens = n
		} else {
			low = mid + 1
		}
	}
	usage.PromptTokens, usage.DroppedMessages = tokens, low*2
	return candidate(low), usage, nil
}

func (c *Client) countPrompt(ctx context.Context, messages []conversation.Message) (int, error) {
	var template struct {
		Prompt *string `json:"prompt"`
	}
	if err := c.contextRequest(ctx, "/apply-template", map[string]any{"messages": nativeMessages(messages), "add_generation_prompt": true, "chat_template_kwargs": map[string]any{"enable_thinking": false}}, &template); err != nil {
		return 0, err
	}
	if template.Prompt == nil {
		return 0, fmt.Errorf("LLM context: template response lacks prompt")
	}
	var result struct {
		Tokens []int `json:"tokens"`
	}
	if err := c.contextRequest(ctx, "/tokenize", map[string]any{"content": *template.Prompt, "add_special": true, "parse_special": true}, &result); err != nil {
		return 0, err
	}
	if len(result.Tokens) == 0 {
		return 0, fmt.Errorf("LLM context: tokenizer returned no tokens")
	}
	return len(result.Tokens), nil
}

func (c *Client) contextRequest(ctx context.Context, path string, value any, result any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.address, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("LLM context %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("LLM context %s: %s: %s", path, resp.Status, body)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(result); err != nil {
		return fmt.Errorf("LLM context %s: %w", path, err)
	}
	return nil
}
