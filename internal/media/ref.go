package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

const InboxDir = ".local/telegram-inbox"

// AttachmentRef is metadata only. Bytes live on disk after Materialize.
type AttachmentRef struct {
	Source       string // "message" | "reply_to"
	ChatID       int64
	MessageID    int64
	MediaGroupID string
	Kind         string // "photo" | "document"
	FileID       string
	FileUniqueID string
	FileName     string
	MIME         string
	Width        int
	Height       int
	FileSize     int64
	Caption      string
	Date         int64
}

type StagedFile struct {
	Ref     AttachmentRef
	RelPath string
	AbsPath string
	Bytes   int64
}

var safeComponent = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func FromMessage(msg *gotgbot.Message) *AttachmentRef {
	if msg == nil {
		return nil
	}
	if len(msg.Photo) > 0 {
		p := msg.Photo[len(msg.Photo)-1]
		return &AttachmentRef{
			Source:       "message",
			ChatID:       msg.Chat.Id,
			MessageID:    msg.MessageId,
			MediaGroupID: msg.MediaGroupId,
			Kind:         "photo",
			FileID:       p.FileId,
			FileUniqueID: p.FileUniqueId,
			MIME:         "image/jpeg",
			Width:        int(p.Width),
			Height:       int(p.Height),
			FileSize:     p.FileSize,
			Caption:      msg.Caption,
			Date:         msg.Date,
		}
	}
	if msg.Document != nil {
		d := msg.Document
		return &AttachmentRef{
			Source:       "message",
			ChatID:       msg.Chat.Id,
			MessageID:    msg.MessageId,
			MediaGroupID: msg.MediaGroupId,
			Kind:         "document",
			FileID:       d.FileId,
			FileUniqueID: d.FileUniqueId,
			FileName:     d.FileName,
			MIME:         d.MimeType,
			FileSize:     d.FileSize,
			Caption:      msg.Caption,
			Date:         msg.Date,
		}
	}
	return nil
}

func FromReply(msg *gotgbot.Message) *AttachmentRef {
	if msg == nil || msg.ReplyToMessage == nil {
		return nil
	}
	ref := FromMessage(msg.ReplyToMessage)
	if ref != nil {
		ref.Source = "reply_to"
	}
	return ref
}

// Extract prefers media on this message, then media on ReplyToMessage.
func Extract(msg *gotgbot.Message) *AttachmentRef {
	if ref := FromMessage(msg); ref != nil {
		return ref
	}
	return FromReply(msg)
}

func SanitizeComponent(s string) string {
	if safeComponent.MatchString(s) {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func ExtForMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	case "application/pdf":
		return "pdf"
	case "image/webp":
		return "webp"
	case "image/heic", "image/heif":
		return "heic"
	default:
		return "bin"
	}
}

func RelPath(chatName string, ref AttachmentRef) string {
	chat := SanitizeComponent(chatName)
	id := SanitizeComponent(ref.FileUniqueID)
	name := fmt.Sprintf("%d-%s.%s", ref.MessageID, id, ExtForMIME(ref.MIME))
	return path.Join(InboxDir, chat, name)
}

func InboxDenyRules() []string {
	return []string{
		"Read(.local/telegram-inbox/**)",
		"Grep(.local/telegram-inbox/**)",
		"Read(**/.local/telegram-inbox/**)",
		"Grep(**/.local/telegram-inbox/**)",
	}
}

func KindLine(ref AttachmentRef) string {
	if ref.Kind == "photo" {
		return "photo (Telegram-compressed JPEG)"
	}
	var b strings.Builder
	b.WriteString("document")
	if ref.FileName == "" && ref.MIME == "" {
		return b.String()
	}
	b.WriteString(" (")
	if ref.FileName != "" {
		b.WriteString("original filename ")
		b.WriteString(ref.FileName)
		if ref.MIME != "" {
			b.WriteString(", ")
		}
	}
	b.WriteString(ref.MIME)
	b.WriteByte(')')
	return b.String()
}

func VisionPromptLine(class Class, mime string, attached bool) string {
	if attached {
		return "attached. You may use the image to answer. Copy from the handle if the user also asked to save."
	}
	extra := ""
	if class == ClassLook || class == ClassMixed {
		if !VisionMIME(mime) {
			extra = " (MIME not supported in v1)"
		} else {
			extra = " yet (looking at pictures is not enabled yet)"
		}
	}
	return "not attached" + extra + ". Copy the file with cp/mv from the handle. Do not Read/Grep/open the image. Destination and Markdown follow this workspace's AGENTS.md. Then git-add only the dest + notes (never .local/) and commit."
}

func FormatAttachment(staged StagedFile, visionLine string) string {
	ref := staged.Ref
	var b strings.Builder
	b.WriteString("\n\n---\nAttachment (do not confuse with the user's words):\n")
	fmt.Fprintf(&b, "- handle: %s\n", staged.RelPath)
	fmt.Fprintf(&b, "- kind: %s\n", KindLine(ref))
	fmt.Fprintf(&b, "- source: %s  message_id=%d", ref.Source, ref.MessageID)
	if ref.Date != 0 {
		fmt.Fprintf(&b, "  date=%s", time.Unix(ref.Date, 0).UTC().Format("2006-01-02"))
	}
	b.WriteByte('\n')
	switch {
	case ref.Width > 0 && ref.Height > 0 && staged.Bytes > 0:
		fmt.Fprintf(&b, "- size: %dx%d  bytes=%d\n", ref.Width, ref.Height, staged.Bytes)
	case ref.Width > 0 && ref.Height > 0:
		fmt.Fprintf(&b, "- size: %dx%d\n", ref.Width, ref.Height)
	case staged.Bytes > 0:
		fmt.Fprintf(&b, "- size: bytes=%d\n", staged.Bytes)
	}
	fmt.Fprintf(&b, "- vision: %s\n", visionLine)
	return b.String()
}
