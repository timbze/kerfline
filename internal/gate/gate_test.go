package gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShouldReplyTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":true}"}}]}`))
	}))
	defer srv.Close()

	d, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		Model:   "grok-4.3",
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{ChatName: "notes", UserText: "buy milk"})
	if err != nil {
		t.Fatal(err)
	}
	if d != Reply {
		t.Fatalf("got %v, want Reply", d)
	}
}

func TestShouldReplyFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":false}"}}]}`))
	}))
	defer srv.Close()

	d, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		Model:   "grok-4.3",
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{UserText: "lol"})
	if err != nil {
		t.Fatal(err)
	}
	if d != Skip {
		t.Fatalf("got %v, want Skip", d)
	}
}

func TestShouldReplyHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if AuthHTTP(err) {
		t.Fatalf("500 must not be auth: %v", err)
	}
}

func TestShouldReplyForbiddenIsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials","error":"The OAuth2 access token could not be validated."}`))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !AuthHTTP(err) {
		t.Fatalf("want auth http error, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") {
		t.Fatalf("error missing status: %q", msg)
	}
	if !strings.Contains(msg, "unauthenticated:bad-credentials") {
		t.Fatalf("error missing body: %q", msg)
	}
}

func TestShouldReplyHTTP400IncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_tokens too low","type":"invalid_request"}}`))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "400") {
		t.Fatalf("error missing status: %q", msg)
	}
	if !strings.Contains(msg, "max_tokens too low") {
		t.Fatalf("error missing body snippet: %q", msg)
	}
}

func TestShouldReplyHTTPErrorTruncatesBody(t *testing.T) {
	long := strings.Repeat("x", 500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, strings.Repeat("x", 201)) {
		t.Fatalf("body not truncated to 200 bytes: len=%d", len(msg))
	}
	if !strings.Contains(msg, strings.Repeat("x", 200)) {
		t.Fatalf("expected 200-byte body snippet: %q", msg)
	}
}

func TestShouldReplyGarbageJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not-json"}}]}`))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldReplyEmptyToken(t *testing.T) {
	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: "http://example.invalid",
		Token:   "",
	}, Input{UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldReplyRequestBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":true}"}}]}`))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL:   srv.URL,
		Model:     "grok-4.3",
		Reasoning: "none",
		HTTP:      srv.Client(),
		Token:     "tok",
	}, Input{
		ChatName:      "notes",
		Private:       true,
		WorkspaceHint: "keep lists here",
		Quoted:        "what should we buy?",
		UserText:      "buy milk please",
	})
	if err != nil {
		t.Fatal(err)
	}

	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request json: %v body=%s", err, gotBody)
	}
	if req["model"] != "grok-4.3" {
		t.Fatalf("model=%v", req["model"])
	}
	if req["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens=%v", req["max_tokens"])
	}
	if req["reasoning_effort"] != "none" {
		t.Fatalf("reasoning_effort=%v, want none", req["reasoning_effort"])
	}

	rf, ok := req["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("missing response_format: %v", req["response_format"])
	}
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format.type=%v", rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("missing json_schema: %v", rf["json_schema"])
	}
	schema, ok := js["schema"].(map[string]any)
	if !ok {
		t.Fatalf("missing schema: %v", js["schema"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || props["respond"] == nil {
		t.Fatalf("schema missing respond: %v", schema)
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "respond" {
		t.Fatalf("required=%v", required)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties=%v", schema["additionalProperties"])
	}

	msgs, ok := req["messages"].([]any)
	if !ok || len(msgs) < 2 {
		t.Fatalf("messages=%v", req["messages"])
	}
	user, ok := msgs[1].(map[string]any)
	if !ok {
		t.Fatalf("user msg=%v", msgs[1])
	}
	content, _ := user["content"].(string)
	if !strings.Contains(content, "buy milk please") {
		t.Fatalf("user content missing text: %q", content)
	}
	if !strings.Contains(content, "notes") {
		t.Fatalf("user content missing chat name: %q", content)
	}
	if !strings.Contains(strings.ToLower(content), "private") {
		t.Fatalf("user content missing private flag: %q", content)
	}
	if !strings.Contains(content, "keep lists here") {
		t.Fatalf("user content missing workspace hint: %q", content)
	}
	if !strings.Contains(content, "what should we buy?") {
		t.Fatalf("user content missing quoted: %q", content)
	}
}

func TestShouldReplyReasoningOmit(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":false}"}}]}`))
	}))
	defer srv.Close()

	_, err := ShouldReply(context.Background(), &Client{
		BaseURL:   srv.URL,
		Model:     "grok-4.3",
		Reasoning: "omit",
		HTTP:      srv.Client(),
		Token:     "tok",
	}, Input{UserText: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatal(err)
	}
	if _, ok := req["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort present: %v", req["reasoning_effort"])
	}
}

func TestShouldReplyTruncatesWorkspaceHint(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"respond\":true}"}}]}`))
	}))
	defer srv.Close()

	hint := strings.Repeat("a", 3000)
	orig := hint
	_, err := ShouldReply(context.Background(), &Client{
		BaseURL: srv.URL,
		Model:   "grok-4.3",
		HTTP:    srv.Client(),
		Token:   "tok",
	}, Input{WorkspaceHint: hint, UserText: "save this"})
	if err != nil {
		t.Fatal(err)
	}
	if hint != orig {
		t.Fatal("mutated caller's WorkspaceHint")
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatal(err)
	}
	msgs := req["messages"].([]any)
	user := msgs[1].(map[string]any)
	content := user["content"].(string)
	if strings.Contains(content, strings.Repeat("a", 2049)) {
		t.Fatalf("hint not truncated: len content=%d", len(content))
	}
	if !strings.Contains(content, strings.Repeat("a", 2048)) {
		t.Fatalf("expected 2048-byte hint in content, got len=%d", len(content))
	}
}
