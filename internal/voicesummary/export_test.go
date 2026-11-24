package voicesummary

// 非公開の関数を外部テスト（voicesummary_test）から呼べるようにする。
var (
	JobName     = jobName
	RequestIDOf = requestIDOf

	RetryAttempt = retryAttempt

	ErrTrackMissing = errTrackMissing

	MissingPostPermissions = missingPostPermissions
)

var (
	ParseSegments    = parseSegments
	MergeUtterances  = mergeUtterances
	RenderTranscript = renderTranscript
)

var (
	FormatReport         = formatReport
	FormatNoSpeechReport = formatNoSpeechReport
)

const (
	CraigDeletedNotice      = craigDeletedNotice
	CraigDeleteFailedNotice = craigDeleteFailedNotice
)
