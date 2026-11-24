package voicerecording

// PaddingSamples は、時間軸の計算をテストから確かめるための公開。
var PaddingSamples = paddingSamples

// ChannelPermissions は、権限の計算をテストから確かめるための公開。
var ChannelPermissions = channelPermissions

// 表示名と周知の文言を、テストから確かめるための公開。
var (
	RecordingNickname = recordingNickname
	OriginalNickname  = originalNickname
	EndReasonLabel    = endReasonLabel
	StartNotice       = startNotice
	EndNotice         = endNotice
	FailureNotice     = failureNotice
)

// ActiveKey は、ロックのオブジェクトのキー。ロックの解除の失敗を fake に仕込むために公開する。
var ActiveKey = activeKey
