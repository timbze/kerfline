package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strings"
	"time"
)

const (
	defaultSTTBaseURL = "https://api.x.ai"
	defaultSTTTimeout = 2 * time.Minute
)

type STT struct {
	BaseURL string
	HTTP    *http.Client
}

func (s *STT) Transcribe(ctx context.Context, token string, r io.Reader, filename, mime string) (Transcript, error) {
	if token == "" {
		return Transcript{}, &StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		base = defaultSTTBaseURL
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: defaultSTTTimeout}
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		defer mw.Close()
		if filename == "" {
			filename = "voice.ogg"
		}
		if mime == "" {
			mime = "application/octet-stream"
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, path.Base(filename)))
		h.Set("Content-Type", mime)
		part, err := mw.CreatePart(h)
		if err != nil {
			errCh <- err
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			errCh <- err
			_ = pw.CloseWithError(err)
			return
		}
		errCh <- nil
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/stt", pr)
	if err != nil {
		return Transcript{}, &StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(req)
	copyErr := <-errCh
	if err != nil {
		return Transcript{}, &StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if copyErr != nil {
		return Transcript{}, &StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	if resp.StatusCode != http.StatusOK {
		return Transcript{}, &StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	var out struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Transcript{}, &StageError{Reason: "stt", Msg: "Couldn't transcribe that voice note."}
	}
	return Transcript{Text: out.Text, Language: out.Language, Duration: out.Duration}, nil
}

func TokenFromEnvOrAuth(envKey string, authJSON []byte) (string, error) {
	if k := strings.TrimSpace(envKey); k != "" {
		return k, nil
	}
	return ParseGrokAuthJSON(authJSON)
}

func ParseGrokAuthJSON(raw []byte) (string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("grok auth.json: %w", err)
	}
	type entry struct {
		Key        string `json:"key"`
		CreateTime string `json:"create_time"`
	}
	bestKey := ""
	bestTime := ""
	for _, rawEnt := range m {
		var e entry
		if err := json.Unmarshal(rawEnt, &e); err != nil {
			continue
		}
		if e.Key == "" {
			continue
		}
		if bestKey == "" || e.CreateTime > bestTime {
			bestKey = e.Key
			bestTime = e.CreateTime
		}
	}
	if bestKey == "" {
		return "", fmt.Errorf("grok auth.json: no key")
	}
	return bestKey, nil
}
