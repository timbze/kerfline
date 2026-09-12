package media

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type acpBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

func PromptJSONRelPath(rel string) string {
	return strings.TrimSuffix(rel, path.Ext(rel)) + ".prompt.json"
}

func EncodePromptJSON(prompt, mime string, image []byte) ([]byte, error) {
	if strings.TrimSpace(mime) == "" || len(image) == 0 {
		return nil, errAttach()
	}
	return json.Marshal([]acpBlock{
		{Type: "text", Text: prompt},
		{Type: "image", MIMEType: mime, Data: base64.StdEncoding.EncodeToString(image)},
	})
}

func WritePromptJSON(staged StagedFile, prompt string) (string, error) {
	raw, err := os.ReadFile(staged.AbsPath)
	if err != nil || len(raw) == 0 {
		return "", errAttach()
	}
	mime := staged.Ref.MIME
	if mime == "" {
		mime = "image/jpeg"
	}
	body, err := EncodePromptJSON(prompt, mime, raw)
	if err != nil {
		return "", err
	}
	rel := PromptJSONRelPath(staged.RelPath)
	abs := filepath.Join(filepath.Dir(staged.AbsPath), filepath.Base(filepath.FromSlash(rel)))
	if err := os.WriteFile(abs, body, 0o600); err != nil {
		return "", errAttach()
	}
	return rel, nil
}

func errAttach() error {
	return &StageError{Reason: "attach", Msg: "Couldn't attach that picture."}
}
