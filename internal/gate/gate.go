package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	Skip Decision = iota
	Reply
)

type Decision int

type Input struct {
	ChatName      string
	Private       bool
	WorkspaceHint string
	Quoted        string
	UserText      string
}

type Client struct {
	BaseURL   string // e.g. cfg.STT.BaseURL, no trailing slash required
	Model     string
	Reasoning string // none|low|medium|high|xhigh|omit
	Timeout   time.Duration
	HTTP      *http.Client // optional; tests inject httptest client
	Token     string
}

const (
	maxWorkspaceHintBytes = 2048
	maxHTTPErrorBodyBytes = 200
	maxTokens             = 128
	systemPrompt          = "Kerfline is a Telegram bot for one git workspace. Reply true only if the user is addressing the bot, asking it to do work, answering a bot question, or dropping material that belongs in this workspace (notes, measurements, lists, facts to keep). False for ordinary chatter, jokes, and empty acknowledgements. In a private chat, lean true on short follow-ups (`ok`, `yes`, `do it`). In a group, lean false unless it is clearly for the bot or clearly workspace data."
)

func ShouldReply(ctx context.Context, c *Client, in Input) (Decision, error) {
	if c == nil {
		return Skip, fmt.Errorf("gate: nil client")
	}
	if strings.TrimSpace(c.Token) == "" {
		return Skip, fmt.Errorf("gate: empty token")
	}

	base := strings.TrimRight(c.BaseURL, "/")
	client := c.HTTP
	if client == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	body, err := buildRequestBody(c, in)
	if err != nil {
		return Skip, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Skip, fmt.Errorf("gate: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return Skip, fmt.Errorf("gate: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Skip, fmt.Errorf("gate: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := respBody
		if len(snippet) > maxHTTPErrorBodyBytes {
			snippet = snippet[:maxHTTPErrorBodyBytes]
		}
		if len(snippet) == 0 {
			return Skip, fmt.Errorf("gate: http %d", resp.StatusCode)
		}
		return Skip, fmt.Errorf("gate: http %d: %s", resp.StatusCode, snippet)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return Skip, fmt.Errorf("gate: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return Skip, fmt.Errorf("gate: empty choices")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return Skip, fmt.Errorf("gate: empty content")
	}

	var parsed struct {
		Respond *bool `json:"respond"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return Skip, fmt.Errorf("gate: content json: %w", err)
	}
	if parsed.Respond == nil {
		return Skip, fmt.Errorf("gate: missing respond")
	}
	if *parsed.Respond {
		return Reply, nil
	}
	return Skip, nil
}

func buildRequestBody(c *Client, in Input) ([]byte, error) {
	hint := in.WorkspaceHint
	if len(hint) > maxWorkspaceHintBytes {
		hint = hint[:maxWorkspaceHintBytes]
	}

	private := "no"
	if in.Private {
		private = "yes"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "chat: %s\n", in.ChatName)
	fmt.Fprintf(&b, "private: %s\n", private)
	fmt.Fprintf(&b, "workspace_hint:\n%s\n", hint)
	if in.Quoted != "" {
		fmt.Fprintf(&b, "quoted:\n%s\n", in.Quoted)
	}
	fmt.Fprintf(&b, "user:\n%s", in.UserText)

	req := map[string]any{
		"model":      c.Model,
		"max_tokens": maxTokens,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "gate",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"respond": map[string]any{"type": "boolean"},
					},
					"required":             []string{"respond"},
					"additionalProperties": false,
				},
			},
		},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": b.String()},
		},
	}
	if c.Reasoning != "" && c.Reasoning != "omit" {
		req["reasoning_effort"] = c.Reasoning
	}
	return json.Marshal(req)
}
