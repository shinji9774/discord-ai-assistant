package summarize

// 非公開の関数を外部テスト（summarize_test）から呼べるようにする。
var ResolvePeriod = resolvePeriod
var Collect = collect

var (
	RenderTranscript = renderTranscript
	FormatReport     = formatReport
)
