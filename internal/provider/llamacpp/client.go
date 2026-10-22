package llamacpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keshon/veyra/internal/conversation"
)

type Sampling struct {
	Temperature, TopP, RepeatPenalty float64
	TopK, Seed                       int
	MaxTokens                        int
	ContextTokens                    int
}
type Client struct {
	jsonSummary bool
	address     string
	sampling    Sampling
}

func New(address string) *Client {
	return NewConfigured(address, Sampling{Temperature: 0.2, TopP: 0.9, RepeatPenalty: 1.05, MaxTokens: 128})
}
func NewConfigured(address string, sampling Sampling) *Client {
	return &Client{address: address, sampling: sampling}
}

// SetSampling is called only while the service excludes active operations.
func (c *Client) SetSampling(s Sampling) { c.sampling = s }

func (c *Client) Generate(ctx context.Context, messages []conversation.Message, emit func(string) error) error {
	return c.generate(ctx, messages, nil, emit)
}

func (c *Client) generate(ctx context.Context, messages []conversation.Message, format any, emit func(string) error) error {
	url := c.address
	native := nativeMessages(messages)
	payload := map[string]any{"top_k": c.sampling.TopK, "seed": c.sampling.Seed, "messages": native, "stream": true, "max_tokens": c.sampling.MaxTokens, "temperature": c.sampling.Temperature, "top_p": c.sampling.TopP, "repeat_penalty": c.sampling.RepeatPenalty, "chat_template_kwargs": map[string]any{"enable_thinking": false}}
	if c.jsonSummary {
		payload["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "conversation_summary", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string", "maxLength": 1024}}, "required": []string{"summary"}, "additionalProperties": false}}}
	}
	if format != nil {
		payload["response_format"] = format
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(url, "/")+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("LLM: %s: %s", resp.Status, b)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	complete := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if body == "[DONE]" {
			complete = true
			break
		}
		var event struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			return fmt.Errorf("invalid LLM event: %w", err)
		}
		if len(event.Error) > 0 && string(event.Error) != "null" {
			return fmt.Errorf("LLM stream error: %s", event.Error)
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content != "" {
				if err := emit(choice.Delta.Content); err != nil {
					return err
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason == "length" {
				return conversation.ErrResponseLimit
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !complete {
		return errors.New("LLM stream ended without DONE")
	}
	return nil
}
