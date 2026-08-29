package bot

import "testing"

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
