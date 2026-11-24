package voicerecording

import (
	"fmt"
	"time"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
)

const (
	startNoticeFormat           = "ボイスチャンネル <#%s> の録音を開始しました（開始: <@%s>）。自分の音声を録音から除きたい人は `/record-optout` を実行してください。"
	endNoticeFormat             = "録音を終了しました（理由: %s）。文字起こしと要約ができたら、このチャンネルに投稿します。"
	endWithoutAudioNoticeFormat = "録音を終了しました（理由: %s）。文字起こしの対象になる音声がなかったため、要約は投稿しません。"
	notStartedNotice            = "録音を始められませんでした。もう一度 `/record-start` を実行してください。"
	recordingFailedNotice       = "録音が失敗しました。この録音の文字起こしと要約は行いません。"
	timeLimitLabelFormat        = "録音の上限（%d 分）への到達"
)

// endReasonLabels は、上限への到達を除く終了の理由の表記。
var endReasonLabels = map[EndReason]string{
	EndReasonStopCommand:  "停止コマンド",
	EndReasonEveryoneLeft: "参加者全員の退出",
	EndReasonDisconnected: "ボイスチャンネルからの切断",
}

// endReasonLabel は、終了の投稿に入れる理由の表記を返す。
func endReasonLabel(reason EndReason, maxDuration time.Duration) string {
	if reason == EndReasonTimeLimit {
		return fmt.Sprintf(timeLimitLabelFormat, int(maxDuration/time.Minute))
	}
	return endReasonLabels[reason]
}

func startNotice(session Session) reply.ChannelPost {
	return channelPost(session, fmt.Sprintf(startNoticeFormat, session.VoiceChannelID, session.StarterID))
}

func endNotice(session Session, label string, hasAudio bool) reply.ChannelPost {
	if !hasAudio {
		return channelPost(session, fmt.Sprintf(endWithoutAudioNoticeFormat, label))
	}
	return channelPost(session, fmt.Sprintf(endNoticeFormat, label))
}

// failureNotice は、録音の異常終了の投稿を返す。started は開始の投稿の後かどうか。
func failureNotice(session Session, started bool) reply.ChannelPost {
	if !started {
		return channelPost(session, notStartedNotice)
	}
	return channelPost(session, recordingFailedNotice)
}

// channelPost は、録音を開始したテキストチャンネルへ、開始した人に通知する投稿を作る。
func channelPost(session Session, text string) reply.ChannelPost {
	return reply.ChannelPost{
		ChannelID:    session.TextChannelID,
		NotifyUserID: session.StarterID,
		Result:       command.Result{Text: text},
	}
}
