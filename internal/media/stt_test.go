package media

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSTTTranscribeParsesResponse(t *testing.T) {
	var gotAuth, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stt" || r.Method != http.MethodPost {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"buy milk","language":"en","duration":3.2}`))
	}))
	defer srv.Close()

	c := &STT{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
	}
	tr, err := c.Transcribe(context.Background(), "SECRETTOKEN", bytes.NewReader([]byte("ogg-bytes")), "77-voice.ogg", "audio/ogg")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "buy milk" || tr.Language != "en" || tr.Duration != 3.2 {
		t.Fatalf("%+v", tr)
	}
	if gotAuth != "Bearer SECRETTOKEN" {
		t.Fatalf("auth %q", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data; boundary=") {
		t.Fatalf("content-type %q", gotCT)
	}
	body := string(gotBody)
	fileIdx := strings.Index(body, `name="file"`)
	if fileIdx < 0 {
		t.Fatalf("missing file field: %q", body)
	}
	if !strings.Contains(body, `filename="77-voice.ogg"`) {
		t.Fatalf("filename: %q", body)
	}
	if !strings.Contains(body, "ogg-bytes") {
		t.Fatalf("payload: %q", body)
	}
	if n := strings.Count(body, `form-data; name="`); n != 1 {
		t.Fatalf("want one form field (file last), got %d in %q", n, body)
	}
}

func TestSTTErrorHasNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthenticated","error":"bad credentials"}`))
	}))
	defer srv.Close()
	c := &STT{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Transcribe(context.Background(), "SECRETTOKEN", bytes.NewReader([]byte("x")), "a.ogg", "audio/ogg")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "SECRETTOKEN") {
		t.Fatalf("token leaked: %q", msg)
	}
	if UserMessage(err) != "Couldn't transcribe that voice note." {
		t.Fatalf("user msg %q", UserMessage(err))
	}
}

func TestSTTForbiddenIsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"unauthenticated:bad-credentials","error":"The OAuth2 access token could not be validated."}`))
	}))
	defer srv.Close()
	c := &STT{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Transcribe(context.Background(), "tok", bytes.NewReader([]byte("x")), "a.ogg", "audio/ogg")
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
	if UserMessage(err) != "Couldn't transcribe that voice note." {
		t.Fatalf("user msg %q", UserMessage(err))
	}
}

func TestSTTServerErrorIsNotAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := &STT{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Transcribe(context.Background(), "tok", bytes.NewReader([]byte("x")), "a.ogg", "audio/ogg")
	if err == nil {
		t.Fatal("expected error")
	}
	if AuthHTTP(err) {
		t.Fatalf("500 must not be auth: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error missing status: %q", err.Error())
	}
}

func TestTokenFromEnvOrAuth(t *testing.T) {
	got, err := TokenFromEnvOrAuth(" env-tok ", []byte(`{"a":{"key":"json-tok"}}`))
	if err != nil || got != "env-tok" {
		t.Fatalf("env wins: %q %v", got, err)
	}
	got, err = TokenFromEnvOrAuth("", []byte(`{"a":{"key":"json-tok"}}`))
	if err != nil || got != "json-tok" {
		t.Fatalf("auth json: %q %v", got, err)
	}
	if _, err := TokenFromEnvOrAuth("  ", []byte(`{}`)); err == nil {
		t.Fatal("both missing should fail")
	}
}

func TestParseGrokAuthJSON(t *testing.T) {
	raw := []byte(`{
		"https://auth.x.ai::abc": {
			"key": "tok-1",
			"create_time": "2026-01-01T00:00:00Z"
		}
	}`)
	got, err := ParseGrokAuthJSON(raw)
	if err != nil || got != "tok-1" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := ParseGrokAuthJSON([]byte(`{}`)); err == nil {
		t.Fatal("empty object should fail")
	}
	if _, err := ParseGrokAuthJSON([]byte(`not json`)); err == nil {
		t.Fatal("invalid json should fail")
	}
}
