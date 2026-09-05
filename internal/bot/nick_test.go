package bot

import "testing"

func TestExplicitlyAddressed(t *testing.T) {
	cases := []struct {
		name string
		text string
		user string
		want bool
	}{
		{name: "ask with prompt", text: "/ask buy milk", user: "notesbot", want: true},
		{name: "ask at bot with prompt", text: "/ask@notesbot buy milk", user: "notesbot", want: true},
		{name: "mention with prompt", text: "@notesbot add eggs", user: "notesbot", want: true},
		{name: "mention suffix", text: "add eggs @notesbot", user: "notesbot", want: true},
		{name: "empty ask", text: "/ask", user: "notesbot", want: false},
		{name: "bare mention", text: "@notesbot", user: "notesbot", want: false},
		{name: "vocative only", text: "hey kerf save this", user: "notesbot", want: false},
		{name: "name mid sentence", text: "the grok model is slow", user: "notesbot", want: false},
		{name: "plain text", text: "hello", user: "notesbot", want: false},
		{name: "empty", text: "  ", user: "notesbot", want: false},
		{name: "other command", text: "/start", user: "notesbot", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplicitlyAddressed(tc.text, tc.user)
			if got != tc.want {
				t.Fatalf("ExplicitlyAddressed(%q, %q) = %v, want %v", tc.text, tc.user, got, tc.want)
			}
		})
	}
}

func TestVocative(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{name: "kerfline at start", text: "kerfline save this", want: true},
		{name: "hey kerf", text: "hey kerf save this", want: true},
		{name: "hi grok comma", text: "hi grok, buy milk", want: true},
		{name: "hey grok list", text: "hey grok, list", want: true},
		{name: "hei kerfline", text: "hei kerfline, ping", want: true},
		{name: "case insensitive", text: "Hey Kerf do it", want: true},
		{name: "name mid sentence", text: "the grok model is slow", want: false},
		{name: "ask command", text: "/ask buy milk", want: false},
		{name: "greeting only", text: "hey there", want: false},
		{name: "empty", text: "  ", want: false},
		{name: "not addressed", text: "save this", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Vocative(tc.text)
			if got != tc.want {
				t.Fatalf("Vocative(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
