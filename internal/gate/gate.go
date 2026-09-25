package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	Skip Decision = iota
	Reply
)

type Decision int

// Verdict is the gate's answer. Effort is empty when the client has fewer
// than two levels or the model picked one outside them.
type Verdict struct {
	Decision Decision
	Effort   string
}

type Input struct {
	ChatName      string
	Private       bool
	WorkspaceHint string
	Quoted        string
	Attachment    string // e.g. "photo", "document application/pdf"; empty if none
	UserText      string
}

type Client struct {
	BaseURL   string // e.g. cfg.STT.BaseURL, no trailing slash required
	Model     string
	Reasoning string   // none|low|medium|high|xhigh|omit
	Levels    []string // reply-model reasoning levels to pick from, lowest first
	Timeout   time.Duration
	HTTP      *http.Client // optional; tests inject httptest client
	Token     string
}

const (
	maxWorkspaceHintBytes = 2048
	maxHTTPErrorBodyBytes = 200
	maxTokens             = 128
	systemPrompt          = "Kerfline is a Telegram bot for one git workspace. Reply true only if the user is addressing the bot, asking it to do work, answering a bot question, or dropping material that belongs in this workspace (notes, measurements, lists, facts to keep). False for ordinary chatter, jokes, and empty acknowledgements. In a private chat, lean true on short follow-ups (`ok`, `yes`, `do it`). In a group, lean false unless it is clearly for the bot or clearly workspace data."
	effortOnlyPrompt      = "Kerfline is a Telegram bot for one git workspace. The user is addressing the bot, which will reply."
)

// levelGuide describes each reasoning level for the effort rubric.
var levelGuide = map[string]string{
	"none":   "trivial acknowledgement or a fact to file as-is",
	"low":    "file a note, append to a list, simple lookup",
	"medium": "ordinary edits, summaries, short answers that need the workspace",
	"high":   "multi-file changes, planning, reasoning across workspace content",
	"xhigh":  "hard debugging, research, or long careful analysis",
}

func effortRubric(levels []string) string {
	var b strings.Builder
	b.WriteString(" Also pick `effort`, the reasoning level for the bot's reply. Use the lowest level that will do the job well:")
	for _, l := range levels {
		fmt.Fprintf(&b, "\n- %s: %s", l, levelGuide[l])
	}
	return b.String()
}

func pickEffort(c *Client) bool {
	return len(c.Levels) > 1
}

// ShouldReply asks whether the bot should reply and, with two or more
// levels, which reasoning level the reply should run at.
func ShouldReply(ctx context.Context, c *Client, in Input) (Verdict, error) {
	skip := Verdict{Decision: Skip}
	if c == nil {
		return skip, fmt.Errorf("gate: nil client")
	}
	system := systemPrompt
	props := map[string]any{"respond": map[string]any{"type": "boolean"}}
	required := []string{"respond"}
	if pickEffort(c) {
		system += effortRubric(c.Levels)
		props["effort"] = map[string]any{"type": "string", "enum": c.Levels}
		required = append(required, "effort")
	}
	content, err := complete(ctx, c, system, props, required, in)
	if err != nil {
		return skip, err
	}

	var parsed struct {
		Respond *bool  `json:"respond"`
		Effort  string `json:"effort"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return skip, fmt.Errorf("gate: content json: %w", err)
	}
	if parsed.Respond == nil {
		return skip, fmt.Errorf("gate: missing respond")
	}
	v := Verdict{Decision: Skip}
	if *parsed.Respond {
		v.Decision = Reply
	}
	if pickEffort(c) && slices.Contains(c.Levels, parsed.Effort) {
		v.Effort = parsed.Effort
	}
	return v, nil
}

// ChooseEffort picks the reply's reasoning level for a message the bot will
// answer regardless (explicit mention, /ask). With fewer than two levels it
// returns the only level, or "", without calling the API.
func ChooseEffort(ctx context.Context, c *Client, in Input) (string, error) {
	if c == nil {
		return "", fmt.Errorf("gate: nil client")
	}
	if !pickEffort(c) {
		if len(c.Levels) == 1 {
			return c.Levels[0], nil
		}
		return "", nil
	}
	props := map[string]any{"effort": map[string]any{"type": "string", "enum": c.Levels}}
	content, err := complete(ctx, c, effortOnlyPrompt+effortRubric(c.Levels), props, []string{"effort"}, in)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return "", fmt.Errorf("gate: content json: %w", err)
	}
	if !slices.Contains(c.Levels, parsed.Effort) {
		return "", fmt.Errorf("gate: effort %q not in levels", parsed.Effort)
	}
	return parsed.Effort, nil
}

// complete sends one structured chat completion and returns the message content.
func complete(ctx context.Context, c *Client, system string, props map[string]any, required []string, in Input) (string, error) {
	if strings.TrimSpace(c.Token) == "" {
		return "", fmt.Errorf("gate: empty token")
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

	body, err := buildRequestBody(c, system, props, required, in)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("gate: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", httpError(resp.StatusCode, respBody)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("gate: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("gate: empty choices")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("gate: empty content")
	}
	return content, nil
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "gate: http 0"
	}
	if e.Body == "" {
		return fmt.Sprintf("gate: http %d", e.Status)
	}
	return fmt.Sprintf("gate: http %d: %s", e.Status, e.Body)
}

func AuthHTTP(err error) bool {
	var h *HTTPError
	return errors.As(err, &h) && (h.Status == http.StatusUnauthorized || h.Status == http.StatusForbidden)
}

func httpError(status int, body []byte) error {
	snippet := body
	if len(snippet) > maxHTTPErrorBodyBytes {
		snippet = snippet[:maxHTTPErrorBodyBytes]
	}
	return &HTTPError{Status: status, Body: string(snippet)}
}

func buildRequestBody(c *Client, system string, props map[string]any, required []string, in Input) ([]byte, error) {
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
	if in.Attachment != "" {
		fmt.Fprintf(&b, "attachment: %s\n", in.Attachment)
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
					"type":                 "object",
					"properties":           props,
					"required":             required,
					"additionalProperties": false,
				},
			},
		},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": b.String()},
		},
	}
	if c.Reasoning != "" && c.Reasoning != "omit" {
		req["reasoning_effort"] = c.Reasoning
	}
	return json.Marshal(req)
}
