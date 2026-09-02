package bot

import (
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func TestPromptFromMessage(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		user    string
		require bool
		want    string
		wantOK  bool
	}{
		{name: "ask", text: "/ask what is for dinner", user: "notesbot", require: true, want: "what is for dinner", wantOK: true},
		{name: "ask at bot", text: "/ask@notesbot buy milk", user: "notesbot", require: true, want: "buy milk", wantOK: true},
		{name: "ask empty", text: "/ask", user: "notesbot", require: true, wantOK: false},
		{name: "other command", text: "/start", user: "notesbot", require: true, wantOK: false},
		{name: "mention prefix", text: "@notesbot add eggs to the list", user: "notesbot", require: true, want: "add eggs to the list", wantOK: true},
		{name: "mention suffix", text: "add eggs @notesbot", user: "notesbot", require: true, want: "add eggs", wantOK: true},
		{name: "plain group", text: "hello", user: "notesbot", require: true, wantOK: false},
		{name: "plain dm", text: "hello", user: "notesbot", require: false, want: "hello", wantOK: true},
		{name: "empty", text: "  ", user: "notesbot", require: false, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PromptFromMessage(tc.text, tc.user, tc.require)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("got (%q, %v) want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestBuildGrokPrompt(t *testing.T) {
	fromUser := &gotgbot.User{FirstName: "Test", LastName: "User"}
	origText := &gotgbot.Message{From: fromUser, Text: "the oven is at 180"}
	botOrig := &gotgbot.Message{
		From: &gotgbot.User{IsBot: true, FirstName: "Kerfline"},
		RichMessage: &gotgbot.RichMessage{
			Blocks: gotgbot.RichBlockArray{
				gotgbot.RichBlockParagraph{Text: gotgbot.RichTextString("hello from grok")},
			},
		},
	}
	photoCaption := &gotgbot.Message{
		From:    fromUser,
		Caption: "a red truck",
		Photo:   []gotgbot.PhotoSize{{FileId: "x"}},
	}
	forum := &gotgbot.Message{
		From:              fromUser,
		ForumTopicCreated: &gotgbot.ForumTopicCreated{Name: "Kitchen"},
	}

	cases := []struct {
		name    string
		msg     *gotgbot.Message
		require bool
		wantOK  bool
		contain []string
		absent  []string
		exact   string
	}{
		{
			name:    "no reply",
			msg:     &gotgbot.Message{Text: "/ask what is for dinner"},
			require: true,
			wantOK:  true,
			exact:   "what is for dinner",
		},
		{
			name: "reply with ask",
			msg: &gotgbot.Message{
				Text:           "/ask what does this mean?",
				ReplyToMessage: origText,
			},
			require: true,
			wantOK:  true,
			contain: []string{
				"The user is replying to this Telegram message:",
				"From: Test User",
				"the oven is at 180",
				"User:\nwhat does this mean?",
			},
		},
		{
			name: "manual quote",
			msg: &gotgbot.Message{
				Text:           "/ask explain",
				ReplyToMessage: origText,
				Quote:          &gotgbot.TextQuote{Text: "180", IsManual: true},
			},
			require: true,
			wantOK:  true,
			contain: []string{"Quoted excerpt:\n180", "the oven is at 180", "User:\nexplain"},
		},
		{
			name: "auto quote omitted",
			msg: &gotgbot.Message{
				Text:           "/ask explain",
				ReplyToMessage: origText,
				Quote:          &gotgbot.TextQuote{Text: "180", IsManual: false},
			},
			require: true,
			wantOK:  true,
			contain: []string{"the oven is at 180"},
			absent:  []string{"Quoted excerpt:"},
		},
		{
			name: "forum topic created skipped",
			msg: &gotgbot.Message{
				Text:           "/ask what is this",
				ReplyToMessage: forum,
			},
			require: true,
			wantOK:  true,
			exact:   "what is this",
		},
		{
			name: "empty ask with reply",
			msg: &gotgbot.Message{
				Text:           "/ask",
				ReplyToMessage: origText,
			},
			require: true,
			wantOK:  true,
			contain: []string{"the oven is at 180", "From: Test User"},
			absent:  []string{"User:"},
		},
		{
			name: "mention only with reply",
			msg: &gotgbot.Message{
				Text:           "@notesbot",
				ReplyToMessage: origText,
			},
			require: true,
			wantOK:  true,
			contain: []string{"the oven is at 180"},
		},
		{
			name:    "empty ask no reply",
			msg:     &gotgbot.Message{Text: "/ask"},
			require: true,
			wantOK:  false,
		},
		{
			name: "plain reply in group ignored",
			msg: &gotgbot.Message{
				Text:           "nice",
				ReplyToMessage: origText,
			},
			require: true,
			wantOK:  false,
		},
		{
			name: "photo caption",
			msg: &gotgbot.Message{
				Text:           "/ask what is this",
				ReplyToMessage: photoCaption,
			},
			require: true,
			wantOK:  true,
			contain: []string{"a red truck", "User:\nwhat is this"},
		},
		{
			name: "bot rich reply",
			msg: &gotgbot.Message{
				Text:           "/ask shorten that",
				ReplyToMessage: botOrig,
			},
			require: true,
			wantOK:  true,
			contain: []string{"From: the bot", "hello from grok", "User:\nshorten that"},
		},
		{
			name: "dm reply without mention",
			msg: &gotgbot.Message{
				Text:           "what about this",
				ReplyToMessage: origText,
			},
			require: false,
			wantOK:  true,
			contain: []string{"the oven is at 180", "User:\nwhat about this"},
		},
		{
			name: "captioned photo ask group",
			msg: &gotgbot.Message{
				Caption: "/ask save this",
				Photo:   []gotgbot.PhotoSize{{FileId: "x", Width: 1280, Height: 960}},
			},
			require: true,
			wantOK:  true,
			exact:   "save this",
		},
		{
			name: "captioned photo ask dm",
			msg: &gotgbot.Message{
				Caption: "/ask save this",
				Photo:   []gotgbot.PhotoSize{{FileId: "x"}},
			},
			require: false,
			wantOK:  true,
			exact:   "save this",
		},
		{
			name:    "bare photo no turn",
			msg:     &gotgbot.Message{Photo: []gotgbot.PhotoSize{{FileId: "x"}}},
			require: false,
			wantOK:  false,
		},
		{
			name: "document caption ask",
			msg: &gotgbot.Message{
				Caption:  "/ask file this",
				Document: &gotgbot.Document{FileName: "x.pdf", MimeType: "application/pdf"},
			},
			require: true,
			wantOK:  true,
			exact:   "file this",
		},
		{
			name: "empty ask with reply to photo",
			msg: &gotgbot.Message{
				Text: "/ask",
				ReplyToMessage: &gotgbot.Message{
					From:  fromUser,
					Photo: []gotgbot.PhotoSize{{FileId: "x", Width: 10, Height: 10}},
				},
			},
			require: true,
			wantOK:  true,
			contain: []string{"[photo 10x10]", "From: Test User"},
			absent:  []string{"User:"},
		},
		{
			name: "empty ask caption on photo with text reply-to",
			msg: &gotgbot.Message{
				Caption:        "/ask",
				Photo:          []gotgbot.PhotoSize{{FileId: "x"}},
				ReplyToMessage: origText,
			},
			require: true,
			wantOK:  true,
			contain: []string{"the oven is at 180", "From: Test User"},
			absent:  []string{"User:"},
		},
		{
			name: "plain photo caption in group ignored",
			msg: &gotgbot.Message{
				Caption: "nice",
				Photo:   []gotgbot.PhotoSize{{FileId: "x"}},
			},
			require: true,
			wantOK:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := BuildGrokPrompt(tc.msg, "notesbot", tc.require)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v, prompt=%q", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if tc.exact != "" && got != tc.exact {
				t.Fatalf("got %q want exact %q", got, tc.exact)
			}
			for _, s := range tc.contain {
				if !strings.Contains(got, s) {
					t.Fatalf("missing %q in %q", s, got)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(got, s) {
					t.Fatalf("unexpected %q in %q", s, got)
				}
			}
		})
	}
}
