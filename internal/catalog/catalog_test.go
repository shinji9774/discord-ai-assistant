package catalog_test

import (
	"reflect"
	"testing"

	"discord-ai-assistant/internal/catalog"
	"discord-ai-assistant/internal/config/configtest"
)

// 条件: 空の Dependencies で New を呼ぶ。
// 内容: 定義の名前が translate、ask、image、summarize の順に並ぶ。
func TestNew_Always_ProvidesTranslateAskImageAndSummarize(t *testing.T) {
	registry := catalog.New(catalog.Dependencies{}, configtest.Load(t))

	definitions := registry.Definitions()

	var got []string
	for _, d := range definitions {
		got = append(got, d.CommandName())
	}
	if want := []string{"translate", "ask", "image", "summarize"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}
