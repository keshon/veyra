// Package terminal presents the local conversation service in a terminal.
package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/httpapi"
	"github.com/keshon/veyra/internal/language"
)

type serviceStatus struct {
	Prompts      []conversation.Prompt     `json:"prompts"`
	Context      conversation.ContextUsage `json:"context_usage"`
	State        string                    `json:"state"`
	Detail       string                    `json:"detail"`
	Preferences  language.Preferences      `json:"preferences"`
	Capabilities conversation.Capabilities `json:"capabilities"`
	Setup        httpapi.Metadata          `json:"setup"`
}
type wireEvent struct {
	TurnID     uint64                    `json:"turn_id"`
	Phase      string                    `json:"phase"`
	Context    conversation.ContextUsage `json:"context_usage"`
	Kind       string                    `json:"kind"`
	Text       string                    `json:"text"`
	Transcript string                    `json:"transcript"`
	Audio      string                    `json:"audio"`
	Error      string                    `json:"error"`
}
type client struct {
	address string
	http    *http.Client
}

func (c client) request(ctx context.Context, method, path string, body []byte, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("service: %s", strings.TrimSpace(string(b)))
	}
	return resp, nil
}
func (c client) status(ctx context.Context) (serviceStatus, error) {
	var status serviceStatus
	resp, err := c.request(ctx, "GET", "/api/status", nil, "")
	if err != nil {
		return status, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&status)
	return status, err
}
func (c client) action(ctx context.Context, path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	resp, err := c.request(ctx, "POST", path, data, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
func (c client) turn(ctx context.Context, text string, audio []byte, emit func(wireEvent) error) error {
	path, body, kind := "/api/turn", audio, "audio/wav"
	if len(audio) == 0 {
		path = "/api/text"
		body, _ = json.Marshal(map[string]string{"text": text})
		kind = "application/json"
	}
	resp, err := c.request(ctx, "POST", path, body, kind)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	completed := false
	for {
		var event wireEvent
		if err = dec.Decode(&event); err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if event.Error != "" {
			return fmt.Errorf("%s", event.Error)
		}
		if event.Kind == "done" {
			completed = true
		}
		if err = emit(event); err != nil {
			return err
		}
	}
	if !completed {
		return fmt.Errorf("response stream ended before completion")
	}
	return nil
}
