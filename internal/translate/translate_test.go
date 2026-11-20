package translate_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/config/configtest"
	"discord-ai-assistant/internal/translate"
	"discord-ai-assistant/internal/translate/translatetest"
)

func invocation(options map[string]string) command.Invocation {
	return command.Invocation{CommandName: "translate", Options: options}
}

const bytesPerKB = 1000

func translateConfig(t *testing.T) translate.Config {
	t.Helper()
	return configtest.Load(t).Translate
}

func fileInvocation(options map[string]string, file command.Attachment) command.Invocation {
	return command.Invocation{
		CommandName: "translate",
		Options:     options,
		Attachments: map[string]command.Attachment{"file": file},
	}
}

func attachment(filename string, size int) command.Attachment {
	return command.Attachment{Filename: filename, URL: "https://cdn.example/" + filename, Size: size}
}

// 条件: text と to（en）を指定して実行する。
// 内容: 指定した言語（英語 en）への翻訳依頼が 1 件、Translator へ渡る。
func TestExecute_ToSpecified_TranslatesIntoThatLanguage(t *testing.T) {
	fake := &translatetest.Translator{}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{}, translateConfig(t))

	_, _ = cmd.Execute(context.Background(), invocation(map[string]string{"text": "こんにちは", "to": "en"}))

	want := []translatetest.Call{{Text: "こんにちは", Target: translate.Language{Name: "英語", Code: "en"}}}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, want)
	}
}

// 条件: text だけを指定し、to を省略して実行する。
// 内容: 英語（en）への翻訳依頼が 1 件、Translator へ渡る。
func TestExecute_ToOmitted_TranslatesIntoEnglish(t *testing.T) {
	fake := &translatetest.Translator{}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{}, translateConfig(t))

	_, _ = cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	want := []translatetest.Call{{Text: "Hello", Target: translate.Language{Name: "英語", Code: "en"}}}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Errorf("Calls = %+v, want %+v", fake.Calls, want)
	}
}

// 条件: to に choices 外の値（xx）が来る。
// 内容: エラーが返り、Translator は呼ばれない。
func TestExecute_ToNotInChoices_ReturnsErrorWithoutTranslating(t *testing.T) {
	fake := &translatetest.Translator{}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{}, translateConfig(t))

	_, err := cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello", "to": "xx"}))

	if err == nil || len(fake.Calls) != 0 {
		t.Errorf("Execute() error = %v, calls = %+v, want error and no calls", err, fake.Calls)
	}
}

// 条件: 翻訳が成功し、結果が「こんにちは」。
// 内容: 原文の引用の下に翻訳文が置かれた Result が返る。
func TestExecute_Success_ReturnsOriginalQuotedAboveTranslation(t *testing.T) {
	fake := &translatetest.Translator{Result: "こんにちは"}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{}, translateConfig(t))

	got, _ := cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	if want := (command.Result{Text: "> Hello\n\nこんにちは"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, want %+v", got, want)
	}
}

// 条件: Translator がエラーを返す。
// 内容: そのエラーがそのまま返る。
func TestExecute_TranslatorFails_ReturnsError(t *testing.T) {
	failure := errors.New("translate failed")
	fake := &translatetest.Translator{Err: failure}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{}, translateConfig(t))

	_, err := cmd.Execute(context.Background(), invocation(map[string]string{"text": "Hello"}))

	if !errors.Is(err, failure) {
		t.Errorf("Execute() error = %v, want %v", err, failure)
	}
}

// 条件: text も file も指定せずに実行する。
// 内容: 指定を促す案内文が返り、Translator は呼ばれない。
func TestExecute_NoTextNorFile_ReturnsGuidanceWithoutTranslating(t *testing.T) {
	fake := &translatetest.Translator{}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{}, translateConfig(t))

	got, err := cmd.Execute(context.Background(), invocation(map[string]string{}))

	want := command.Result{Text: "翻訳する文章（text）かファイル（file）を指定してください。", Guidance: &command.Guidance{Reason: "no_input"}}
	if err != nil || !reflect.DeepEqual(got, want) || len(fake.Calls) != 0 || len(fake.DocumentCalls) != 0 {
		t.Errorf("Execute() = %+v, %v, calls = %+v %+v, want %+v and no calls", got, err, fake.Calls, fake.DocumentCalls, want)
	}
}

// 条件: text と file を両方指定して実行する。
// 内容: どちらか一方にする旨の案内文が返り、Translator は呼ばれない。
func TestExecute_BothTextAndFile_ReturnsGuidanceWithoutTranslating(t *testing.T) {
	fake := &translatetest.Translator{}
	fetcher := &translatetest.AttachmentFetcher{}
	cmd := translate.New(fake, fetcher, translateConfig(t))

	got, err := cmd.Execute(context.Background(), fileInvocation(map[string]string{"text": "Hello"}, attachment("memo.txt", 10)))

	want := command.Result{Text: "text と file はどちらか一方だけ指定してください。", Guidance: &command.Guidance{Reason: "both_inputs"}}
	if err != nil || !reflect.DeepEqual(got, want) || len(fake.Calls) != 0 || len(fake.DocumentCalls) != 0 || len(fetcher.URLs) != 0 {
		t.Errorf("Execute() = %+v, %v, want %+v and no calls", got, err, want)
	}
}

// 条件: 対応外の拡張子（.pdf）のファイルを指定して実行する。
// 内容: 対応形式の案内文が返り、取得も翻訳も行われない。
func TestExecute_UnsupportedFileExtension_ReturnsGuidanceWithoutFetching(t *testing.T) {
	fake := &translatetest.Translator{}
	fetcher := &translatetest.AttachmentFetcher{}
	cmd := translate.New(fake, fetcher, translateConfig(t))

	got, err := cmd.Execute(context.Background(), fileInvocation(nil, attachment("memo.pdf", 10)))

	want := command.Result{
		Text:     "対応しているファイル形式は .txt / .html / .docx です。",
		Guidance: &command.Guidance{Reason: "unsupported_extension", Attrs: []slog.Attr{slog.String("extension", ".pdf")}},
	}
	if err != nil || !reflect.DeepEqual(got, want) || len(fake.DocumentCalls) != 0 || len(fetcher.URLs) != 0 {
		t.Errorf("Execute() = %+v, %v, want %+v and no calls", got, err, want)
	}
}

// 条件: 上限（100000 バイト）を 1 バイト超えるファイルを指定して実行する。
// 内容: サイズ超過の案内文が返り、取得も翻訳も行われない。
func TestExecute_FileOverSizeLimit_ReturnsGuidanceWithoutFetching(t *testing.T) {
	fake := &translatetest.Translator{}
	fetcher := &translatetest.AttachmentFetcher{}
	cmd := translate.New(fake, fetcher, translateConfig(t))

	got, err := cmd.Execute(context.Background(), fileInvocation(nil, attachment("memo.txt", 100001)))

	want := command.Result{
		Text:     "ファイルは 100 KB 以下にしてください。",
		Guidance: &command.Guidance{Reason: "file_too_large", Attrs: []slog.Attr{slog.Int("size_bytes", 100001), slog.Int("max_bytes", 100000)}},
	}
	if err != nil || !reflect.DeepEqual(got, want) || len(fake.DocumentCalls) != 0 || len(fetcher.URLs) != 0 {
		t.Errorf("Execute() = %+v, %v, want %+v and no calls", got, err, want)
	}
}

// 条件: 上限ちょうど（100000 バイト）の memo.txt を to=en で翻訳する。
// 内容: 翻訳依頼が受理され、memo_en.txt に訳文が入った Result が返る。
func TestExecute_FileAtSizeLimit_ReturnsTranslatedFile(t *testing.T) {
	fake := &translatetest.Translator{DocumentResult: []byte("Hello")}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{Content: []byte("こんにちは")}, translateConfig(t))

	got, err := cmd.Execute(context.Background(), fileInvocation(map[string]string{"to": "en"}, attachment("memo.txt", 100000)))

	want := command.Result{
		Text:  "memo.txt を英語に翻訳しました。",
		Files: []command.File{{Name: "memo_en.txt", Content: []byte("Hello")}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Execute() = %+v, %v, want %+v", got, err, want)
	}
}

// 条件: 拡張子が大文字の NOTES.HTML を to 省略で翻訳する。
// 内容: text/html の形式で、取得した中身と英語が Translator へ渡り、URL がそのまま取得される。
func TestExecute_UppercaseHTMLExtension_TranslatesAsHTMLIntoEnglish(t *testing.T) {
	fake := &translatetest.Translator{}
	fetcher := &translatetest.AttachmentFetcher{Content: []byte("<p>Hello</p>")}
	cmd := translate.New(fake, fetcher, translateConfig(t))

	_, _ = cmd.Execute(context.Background(), fileInvocation(nil, attachment("NOTES.HTML", 12)))

	wantCalls := []translatetest.DocumentCall{{
		Document: translate.Document{Content: []byte("<p>Hello</p>"), ContentType: "text/html"},
		Target:   translate.Language{Name: "英語", Code: "en"},
	}}
	if !reflect.DeepEqual(fake.DocumentCalls, wantCalls) || !reflect.DeepEqual(fetcher.URLs, []string{"https://cdn.example/NOTES.HTML"}) {
		t.Errorf("DocumentCalls = %+v, URLs = %v, want %+v", fake.DocumentCalls, fetcher.URLs, wantCalls)
	}
}

// 条件: .docx のファイルを翻訳する。
// 内容: Word 文書の形式で Translator へ渡る。
func TestExecute_DocxFile_TranslatesAsWordDocument(t *testing.T) {
	fake := &translatetest.Translator{}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{Content: []byte("docx")}, translateConfig(t))

	_, _ = cmd.Execute(context.Background(), fileInvocation(nil, attachment("report.docx", 4)))

	if got := fake.DocumentCalls[0].Document.ContentType; got != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("ContentType = %q, want the docx content type", got)
	}
}

// 条件: 添付ファイルの取得が失敗する。
// 内容: そのエラーがそのまま返り、翻訳は行われない。
func TestExecute_FetchFails_ReturnsErrorWithoutTranslating(t *testing.T) {
	failure := errors.New("fetch failed")
	fake := &translatetest.Translator{}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{Err: failure}, translateConfig(t))

	_, err := cmd.Execute(context.Background(), fileInvocation(nil, attachment("memo.txt", 10)))

	if !errors.Is(err, failure) || len(fake.DocumentCalls) != 0 {
		t.Errorf("Execute() error = %v, calls = %+v, want %v and no calls", err, fake.DocumentCalls, failure)
	}
}

// 条件: ファイルの翻訳が失敗する。
// 内容: そのエラーがそのまま返る。
func TestExecute_TranslateDocumentFails_ReturnsError(t *testing.T) {
	failure := errors.New("translate document failed")
	fake := &translatetest.Translator{DocumentErr: failure}
	cmd := translate.New(fake, &translatetest.AttachmentFetcher{Content: []byte("x")}, translateConfig(t))

	_, err := cmd.Execute(context.Background(), fileInvocation(nil, attachment("memo.txt", 1)))

	if !errors.Is(err, failure) {
		t.Errorf("Execute() error = %v, want %v", err, failure)
	}
}

// 条件: 定義を取得する。
// 内容: サーバ内専用で、text（最大 3000 文字）・file（添付）・to がすべて任意の定義になる。
func TestDefinition_Always_IsGuildOnlyWithOptionalTextAndFile(t *testing.T) {
	cmd := translate.New(&translatetest.Translator{}, &translatetest.AttachmentFetcher{}, translateConfig(t))

	got, err := json.Marshal(withoutChoices(t, cmd.Definition()))

	want := `{"type":1,"name":"translate","description":"文章またはファイルを指定した言語に翻訳します",` +
		`"options":[` +
		`{"type":3,"name":"text","description":"翻訳する文章","max_length":3000},` +
		`{"type":11,"name":"file","description":"翻訳するファイル（.txt / .html / .docx、100 KB まで）"},` +
		`{"type":3,"name":"to","description":"翻訳先の言語（省略時は英語）"}],` +
		`"contexts":[0]}`
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	assertJSONEqual(t, string(got), want)
}

// 条件: 定義を取得する。
// 内容: to の choices が、日本語 ja から始まりペルシア語 fa で終わる 25 件の（名前, コード）になる。
func TestDefinition_Always_ToHasTwentyFiveLanguageChoices(t *testing.T) {
	cmd := translate.New(&translatetest.Translator{}, &translatetest.AttachmentFetcher{}, translateConfig(t))

	got := toChoices(t, cmd.Definition())

	want := []choice{
		{"日本語", "ja"}, {"英語", "en"}, {"中国語（簡体字）", "zh"}, {"中国語（繁体字）", "zh-TW"}, {"韓国語", "ko"},
		{"スペイン語", "es"}, {"フランス語", "fr"}, {"ドイツ語", "de"}, {"イタリア語", "it"}, {"ポルトガル語", "pt"},
		{"ロシア語", "ru"}, {"アラビア語", "ar"}, {"ヒンディー語", "hi"}, {"インドネシア語", "id"}, {"タイ語", "th"},
		{"ベトナム語", "vi"}, {"トルコ語", "tr"}, {"オランダ語", "nl"}, {"ポーランド語", "pl"}, {"ウクライナ語", "uk"},
		{"スウェーデン語", "sv"}, {"マレー語", "ms"}, {"タガログ語", "tl"}, {"ベンガル語", "bn"}, {"ペルシア語", "fa"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("choices = %+v, want %+v", got, want)
	}
}

type choice struct {
	Name  string
	Value string
}

// definitionJSON は定義を JSON を経由して汎用の map にする。
func definitionJSON(t *testing.T, definition any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return decoded
}

// toOption は定義の to オプション（options の 3 番目）を返す。
func toOption(t *testing.T, definition any) map[string]any {
	t.Helper()
	options := definitionJSON(t, definition)["options"].([]any)
	return options[2].(map[string]any)
}

// withoutChoices は to の choices を除いた定義を返す。choices は別のテストで検証する。
func withoutChoices(t *testing.T, definition any) map[string]any {
	t.Helper()
	decoded := definitionJSON(t, definition)
	delete(decoded["options"].([]any)[2].(map[string]any), "choices")
	return decoded
}

func toChoices(t *testing.T, definition any) []choice {
	t.Helper()
	var got []choice
	for _, c := range toOption(t, definition)["choices"].([]any) {
		entry := c.(map[string]any)
		got = append(got, choice{Name: entry["name"].(string), Value: entry["value"].(string)})
	}
	return got
}

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

// 条件: 設定の上限より 1 バイト大きいファイルを指定して実行する。
// 内容: 案内文に書かれた上限（KB）が、設定の値と一致する（7.5）。
func TestExecute_FileOverSizeLimit_GuidanceStatesConfiguredLimit(t *testing.T) {
	cfg := translateConfig(t)
	cmd := translate.New(&translatetest.Translator{}, &translatetest.AttachmentFetcher{}, cfg)
	want := fmt.Sprintf("%d KB", cfg.MaxFileSizeBytes/bytesPerKB)

	got, err := cmd.Execute(context.Background(), fileInvocation(nil, attachment("memo.txt", cfg.MaxFileSizeBytes+1)))

	if err != nil || !strings.Contains(got.Text, want) {
		t.Errorf("Execute() = %q, %v, want it to contain %q", got.Text, err, want)
	}
}

// 条件: コマンドの定義を取得する。
// 内容: ファイルの説明に書かれた上限（KB）が、設定の値と一致する（7.5）。
func TestDefinition_FileDescription_StatesConfiguredLimit(t *testing.T) {
	cfg := translateConfig(t)
	cmd := translate.New(nil, nil, cfg)
	want := fmt.Sprintf("%d KB まで", cfg.MaxFileSizeBytes/bytesPerKB)

	got, err := json.Marshal(cmd.Definition())

	if err != nil || !strings.Contains(string(got), want) {
		t.Errorf("Definition() = %s, %v, want it to contain %q", got, err, want)
	}
}
