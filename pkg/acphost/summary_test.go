package acphost_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gfx-labs/acphost/pkg/acphost"
)

func promptFrame(seq int64, text string) acphost.Frame {
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": seq, "method": "session/prompt",
		"params": map[string]any{"sessionId": "s1", "prompt": []any{map[string]any{"type": "text", "text": text}}},
	})
	return acphost.Frame{Seq: seq, From: "controller", Msg: msg}
}

func chunkFrame(seq int64, text string) acphost.Frame {
	update := map[string]any{
		"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text},
	}
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "session/update",
		"params": map[string]any{"sessionId": "s1", "update": update},
	})
	return acphost.Frame{Seq: seq, From: "agent", Msg: msg}
}

func TestSummarizeEmpty(t *testing.T) {
	assert.Empty(t, acphost.Summarize(nil, 0))
	// Frames with nothing to summarize (initialize) give nothing either.
	frames, _, err := acphost.ReadJournal(filepath.Join("testdata", "journal.ndjson"), 0)
	require.NoError(t, err)
	assert.Empty(t, acphost.Summarize(frames[:2], 0))
}

func TestSummarizeJournal(t *testing.T) {
	frames, _, err := acphost.ReadJournal(filepath.Join("testdata", "journal.ndjson"), 0)
	require.NoError(t, err)
	s := acphost.Summarize(frames, 0)
	assert.Contains(t, s, "User: You have claimed ticket js-1.\nMore text")
	assert.Contains(t, s, "Agent: Reading the spec.")
	assert.Equal(t, 1, strings.Count(s, "Tool: Running shell command"), "one line per tool call")
	assert.Contains(t, s, "Latest plan:\n  [x] read issue\n  [>] write code\n  [ ] open PR")
	assert.Contains(t, s, "Agent: Done.")
}

func TestSummarizeTruncates(t *testing.T) {
	frames := []acphost.Frame{promptFrame(1, "FIRST PROMPT: fix the bug")}
	for i := range 200 {
		frames = append(frames, promptFrame(int64(2*i+2), fmt.Sprintf("prompt %d %s", i, strings.Repeat("x", 100))))
		frames = append(frames, chunkFrame(int64(2*i+3), fmt.Sprintf("reply %d", i)))
	}
	frames = append(frames, chunkFrame(999, " NEWEST"))
	for _, limit := range []int{0, 4096, 1000, 300} {
		s := acphost.Summarize(frames, limit)
		want := limit
		if want == 0 {
			want = acphost.DefaultSummaryBytes
		}
		assert.LessOrEqual(t, len(s), want, "limit %d", limit)
		assert.Contains(t, s, "FIRST PROMPT: fix the bug", "limit %d", limit)
		assert.Contains(t, s, "NEWEST", "limit %d", limit)
		assert.Contains(t, s, "omitted", "limit %d", limit)
		assert.NotContains(t, s, "prompt 0 ", "limit %d: oldest middle dropped", limit)
	}
	// Plenty of room: nothing dropped.
	s := acphost.Summarize(frames, 1<<20)
	assert.NotContains(t, s, "omitted")
	assert.Contains(t, s, "prompt 0 ")
}
