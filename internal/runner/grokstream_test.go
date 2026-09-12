package runner

import (
	"strings"
	"testing"
)

func TestLastTurnIdleAlarm(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"thought","data":"Need to search git history."}`,
		`{"type":"text","data":"I'll look through the codebase and git history for the idle alarm to see when it appeared and who added it."}`,
		`{"type":"tool_call","toolCallId":"1","toolName":"grep"}`,
		`{"type":"tool_call_update","toolCallId":"1","status":"completed"}`,
		`{"type":"usage","stopReason":"tool_use"}`,
		`{"type":"text","data":"The constant is old; I'll check recent protocol work and whether a server-side idle alarm was added."}`,
		`{"type":"tool_call","toolCallId":"2","toolName":"grep"}`,
		`{"type":"usage","stopReason":"tool_use"}`,
		`{"type":"text","data":"Not a new alarm type. idle has been in Traccar since March 2018 (ALARM_IDLE), added by Anton Tananaev when Xirgo events were decoded."}`,
		`{"type":"text","data":" The server does not generate it itself — a device reports it and a protocol decoder maps it."}`,
		`{"type":"usage","stopReason":"end_turn"}`,
		`{"type":"end","stopReason":"end_turn","sessionId":"abc"}`,
	}, "\n")
	got, err := lastTurn(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := "Not a new alarm type. idle has been in Traccar since March 2018 (ALARM_IDLE), added by Anton Tananaev when Xirgo events were decoded. The server does not generate it itself — a device reports it and a protocol decoder maps it."
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestLastTurnSingleText(t *testing.T) {
	in := `{"type":"text","data":"Yes."}` + "\n" + `{"type":"end","stopReason":"end_turn"}`
	got, err := lastTurn(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Yes." {
		t.Fatalf("got %q", got)
	}
}

func TestLastTurnAckAfterProgress(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"text","data":"I'll file this under Thursday and commit."}`,
		`{"type":"tool_call","toolCallId":"1"}`,
		`{"type":"usage","stopReason":"tool_use"}`,
		`{"type":"text","data":"👍"}`,
		`{"type":"end","stopReason":"end_turn"}`,
	}, "\n")
	got, err := lastTurn(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got != "👍" {
		t.Fatalf("got %q", got)
	}
}

func TestLastTurnIgnoresJunkAndThoughts(t *testing.T) {
	in := strings.Join([]string{
		`not json`,
		``,
		`{"type":"thought","data":"secret chain of thought"}`,
		`{"type":"plan","entries":[]}`,
		`{"type":"text","data":"  hello  "}`,
		`{"type":"end"}`,
	}, "\n")
	got, err := lastTurn(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestLastTurnEmpty(t *testing.T) {
	got, err := lastTurn(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("got %q", got)
	}
}
