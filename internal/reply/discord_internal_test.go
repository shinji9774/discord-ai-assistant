package reply

import (
	"encoding/json"
	"reflect"
	"testing"

	"discord-ai-assistant/internal/command"
)

// 条件: 本文に @everyone、ロールメンション、ユーザーメンションを含む。
// 内容: 更新の JSON は空の allowed_mentions を持ち（どの種類も解釈させない）、本文の文字はそのまま残る。
func TestBuildMessageUpdate_TextWithMentions_DisablesAllMentionParsing(t *testing.T) {
	text := "@everyone <@&123456789012345678> <@123456789012345678>"
	want := map[string]any{
		"content": text,
		"allowed_mentions": map[string]any{
			"parse":        []any{},
			"roles":        []any{},
			"users":        []any{},
			"replied_user": false,
		},
	}

	body, err := json.Marshal(buildMessageUpdate(command.Result{Text: text}))

	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("update JSON = %s, want %+v", body, want)
	}
}
