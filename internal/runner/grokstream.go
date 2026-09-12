package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

const grokStreamMaxLine = 16 * 1024 * 1024

type grokEvent struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type grokTurns struct {
	last string
	cur  strings.Builder
}

func lastTurn(r io.Reader) (string, error) {
	var turns grokTurns
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), grokStreamMaxLine)
	for sc.Scan() {
		turns.add(sc.Bytes())
	}
	return turns.text(), sc.Err()
}

func (t *grokTurns) add(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var ev grokEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return
	}
	switch ev.Type {
	case "text":
		t.cur.WriteString(ev.Data)
	case "tool_call", "usage", "end":
		t.flush()
	}
}

func (t *grokTurns) flush() {
	if s := strings.TrimSpace(t.cur.String()); s != "" {
		t.last = s
	}
	t.cur.Reset()
}

func (t *grokTurns) text() string {
	t.flush()
	return t.last
}
