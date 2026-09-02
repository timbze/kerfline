package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

type TelegramFiles struct {
	Token   string
	GetFile func(ctx context.Context, fileID string) (*gotgbot.File, error)
	HTTP    *http.Client
}

func NewTelegram(bot *gotgbot.Bot) *TelegramFiles {
	return &TelegramFiles{
		Token: bot.Token,
		GetFile: func(ctx context.Context, fileID string) (*gotgbot.File, error) {
			return bot.GetFileWithContext(ctx, fileID, &gotgbot.GetFileOpts{
				RequestOpts: &gotgbot.RequestOpts{Timeout: 30 * time.Second},
			})
		},
		HTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

func (t *TelegramFiles) Lookup(ctx context.Context, fileID string) (string, int64, error) {
	if t.GetFile == nil {
		return "", 0, fmt.Errorf("download failed")
	}
	f, err := t.GetFile(ctx, fileID)
	if err != nil || f == nil {
		return "", 0, fmt.Errorf("download failed")
	}
	return f.FilePath, f.FileSize, nil
}

func (t *TelegramFiles) Fetch(ctx context.Context, filePath string) (io.ReadCloser, error) {
	if filePath == "" || t.Token == "" {
		return nil, fmt.Errorf("download failed")
	}
	u := "https://api.telegram.org/file/bot" + t.Token + "/" + filePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("download failed")
	}
	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed")
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("download failed: status %d", resp.StatusCode)
	}
	return resp.Body, nil
}
