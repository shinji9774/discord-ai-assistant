package voicerecording

import "time"

// 録音の時間の見込み（設計書「設定設計」）。ロックの期限と録音タスクの待ち時間に使う。
const (
	// startupAllowance は、録音タスクの起動にかかる見込み。
	startupAllowance = 5 * time.Minute
	// joinTimeout は、ボイスチャンネルへの参加を待つ時間。
	joinTimeout = 60 * time.Second
	// shutdownBudget は、録音を終えてから文字起こしを起動し終えるまでの予算。
	shutdownBudget = 10 * time.Minute
)

// Config は、/record-start の録音の設定。
type Config struct {
	// MaxRecordingMinutes は、1 回の録音の上限（分）。Amazon Transcribe の上限（480 分）以下にする。
	MaxRecordingMinutes int `yaml:"max_recording_minutes" validate:"min=1,max=480"`
}

// MaxDuration は、1 回の録音の上限。
func (c Config) MaxDuration() time.Duration {
	return time.Duration(c.MaxRecordingMinutes) * time.Minute
}

// RecorderTiming は、録音タスクが使う時間の設定を返す。
func (c Config) RecorderTiming() RecorderTiming {
	return RecorderTiming{MaxDuration: c.MaxDuration(), StopPoll: endMarkerPollInterval, JoinTimeout: joinTimeout}
}

// sessionLength は、受付から録音タスクが終わっているはずの時刻までの長さ。
func (c Config) sessionLength() time.Duration {
	return startupAllowance + joinTimeout + c.MaxDuration() + shutdownBudget
}
