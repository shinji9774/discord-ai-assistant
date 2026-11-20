package voicesummary_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"discord-ai-assistant/internal/command"
	"discord-ai-assistant/internal/reply"
	"discord-ai-assistant/internal/reply/replytest"
	"discord-ai-assistant/internal/voicesummary"
	"discord-ai-assistant/internal/voicesummary/craig"
	"discord-ai-assistant/internal/voicesummary/voicesummarytest"
)

// /summarize-voice のテストで使う識別子と録音リンク。依頼 ID は manifest_test.go の requestID。
const (
	voiceGuildID     = "1440000000000000009"
	voiceChannelID   = "1440000000000000001" // manifestOf の ChannelID と同じ
	voiceRequesterID = "1440000000000000002" // manifestOf の RequesterID と同じ
	voiceLink        = "https://craig.horse/rec/AbCdEfGhIjKl?key=XyZ123"
	voiceRecordingID = "AbCdEfGhIjKl"
	voiceLinkKey     = "XyZ123"
	acceptedText     = "録音の要約を受け付けました。完了したら、このチャンネルに結果を投稿します。"

	// recordingLength は、録音の長さ（validConfig の上限 180 分に収まる）。
	recordingLength = 83 * time.Minute
)

// voiceInvocation は、voiceGuildID のテキストチャンネルで link を指定した /summarize-voice の実行。
// bot の権限は、テキストチャンネルへの投稿に足りる textPostPermissions（permission_test.go）。
func voiceInvocation(link string) command.Invocation {
	return command.Invocation{
		ApplicationID:    "app1",
		InteractionToken: "tok1",
		ChannelID:        voiceChannelID,
		CommandName:      "summarize-voice",
		Options:          map[string]string{"link": link},
		AppPermissions:   textPostPermissions,
		InteractionID:    requestID,
		GuildID:          voiceGuildID,
		UserID:           voiceRequesterID,
		ChannelType:      discord.ChannelTypeGuildText,
	}
}

// recordingWithBot は、たろう（1）、はなこ（2）、Craig の bot（3）が入った、停止済みの録音。
func recordingWithBot() craig.Recording {
	return craig.Recording{
		GuildID:   voiceGuildID,
		StartedAt: reportStartedAt,
		Duration:  recordingLength,
		Speakers: []craig.Speaker{
			{Track: 1, Name: "たろう"},
			{Track: 2, Name: "はなこ"},
			{Track: 3, Name: "craig", Bot: true},
		},
	}
}

// tracksWithBot は、recordingWithBot の各トラックの音声。
func tracksWithBot() map[int][]byte {
	return map[int][]byte{1: []byte("たろうの音声"), 2: []byte("はなこの音声"), 3: []byte("bot の音声")}
}

// acceptedManifest は、recordingWithBot を受け付けたときのマニフェスト。bot の話者は含まない。
func acceptedManifest() voicesummary.Manifest {
	return manifestOf(recordingLength, "たろう", "はなこ")
}

// noSpeechPost は、m の依頼の「発話なし」を、依頼者に通知してチャンネルへ投稿する内容。
func noSpeechPost(m voicesummary.Manifest) reply.ChannelPost {
	return reply.ChannelPost{
		ChannelID:    voiceChannelID,
		NotifyUserID: voiceRequesterID,
		Result:       command.Result{Text: voicesummary.FormatNoSpeechReport(m), Format: command.TextFormatMarkdown},
	}
}

// commandScenario は、fake の依存で作った /summarize-voice。録音は recordingWithBot。
type commandScenario struct {
	cfg        voicesummary.Config
	recordings *voicesummarytest.Recordings
	store      *voicesummarytest.Store
	jobs       *voicesummarytest.Jobs
	poster     *replytest.ChannelPoster
}

func newCommandScenario() *commandScenario {
	return &commandScenario{
		cfg:        validConfig(),
		recordings: &voicesummarytest.Recordings{Recording: recordingWithBot(), Tracks: tracksWithBot()},
		store:      &voicesummarytest.Store{},
		jobs:       &voicesummarytest.Jobs{},
		poster:     &replytest.ChannelPoster{},
	}
}

// execute は、いまの依存と設定で作ったコマンドを 1 回実行する。
func (s *commandScenario) execute(inv command.Invocation) (command.Result, error) {
	deps := voicesummary.Dependencies{Recordings: s.recordings, Store: s.store, Jobs: s.jobs, Poster: s.poster}
	return voicesummary.New(deps, s.cfg).Execute(context.Background(), inv)
}

// commandOutcome は、実行の結果と、置き場・ジョブ・投稿に残ったもの。
type commandOutcome struct {
	Result   command.Result
	Keys     []string
	Manifest voicesummary.Manifest // 依頼のマニフェスト。無ければゼロ値
	Audio    map[int][]byte
	Jobs     []voicesummarytest.Job
	Posts    []reply.ChannelPost
}

func (s *commandScenario) outcome(result command.Result) commandOutcome {
	manifest, _ := s.store.Manifest(requestID)
	return commandOutcome{
		Result:   result,
		Keys:     s.store.Keys(),
		Manifest: manifest,
		Audio:    s.store.Audio(requestID),
		Jobs:     s.jobs.Started,
		Posts:    s.poster.Posts,
	}
}

// verify は、エラーなく終わり、結果と残ったものが want と一致することを確かめる。
func (s *commandScenario) verify(t *testing.T, result command.Result, err error, want commandOutcome) {
	t.Helper()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := s.outcome(result); !reflect.DeepEqual(got, want) {
		t.Errorf("outcome =\n%+v\nwant\n%+v", got, want)
	}
}

// verifyFailure は、wantErr を包んだエラー（wantErr が nil なら、なんらかのエラー）が返り、
// その文に録音 ID と鍵が無く、結果と残ったものが want と一致することを確かめる。
func (s *commandScenario) verifyFailure(t *testing.T, result command.Result, err, wantErr error, want commandOutcome) {
	t.Helper()
	if err == nil || (wantErr != nil && !errors.Is(err, wantErr)) {
		t.Fatalf("Execute error = %v, want %v", err, wantErr)
	}
	if text := errorText(err); strings.Contains(text, voiceRecordingID) || strings.Contains(text, voiceLinkKey) {
		t.Errorf("error %q contains the recording link", text)
	}
	if got := s.outcome(result); !reflect.DeepEqual(got, want) {
		t.Errorf("outcome =\n%+v\nwant\n%+v", got, want)
	}
}

// acceptedOutcome は、recordingWithBot を受け付けたあとの状態。manifest はその依頼のマニフェスト。
// bot（トラック 3）の音声は置かず、文字起こしも始めない。
func acceptedOutcome(manifest voicesummary.Manifest) commandOutcome {
	return commandOutcome{
		Result:   command.Result{Text: acceptedText},
		Keys:     []string{requestID + "/audio/1.flac", requestID + "/audio/2.flac", requestID + "/manifest.json"},
		Manifest: manifest,
		Audio:    map[int][]byte{1: []byte("たろうの音声"), 2: []byte("はなこの音声")},
		Jobs:     []voicesummarytest.Job{{RequestID: requestID, Track: 1}, {RequestID: requestID, Track: 2}},
	}
}

// assertDefinitionJSON は、コマンドの定義を JSON にして、wantJSON と値として等しいかを比べる（キーの順と空白の違いは無視する）。
func assertDefinitionJSON(t *testing.T, got discord.SlashCommandCreate, wantJSON string) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &wantValue); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("definition =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

// 条件: 依存を渡さずに作ったコマンドの定義。
// 内容: 名前 summarize-voice、必須の文字列オプション link だけを持ち、サーバのチャンネルでだけ使える。visibility は持たない。
func TestDefinition_Always_DescribesSummarizeVoiceCommand(t *testing.T) {
	cmd := voicesummary.New(voicesummary.Dependencies{}, voicesummary.Config{})

	got := cmd.Definition()

	assertDefinitionJSON(t, got, `{
		"type": 1,
		"name": "summarize-voice",
		"description": "Craig の録音を文字起こしして要約する",
		"options": [
			{"type": 3, "name": "link", "description": "Craig の録音リンク（ほかの人には見えません）", "required": true}
		],
		"contexts": [0]
	}`)
}

// 条件: /summarize-voice だけを登録した Registry。
// 内容: 入力に秘密を含むコマンドとして扱われる（ingress は実行者だけに応答し、worker は入力をログに出さない）。
func TestHasSecretInput_SummarizeVoice_ReturnsTrue(t *testing.T) {
	registry := command.NewRegistry(voicesummary.New(voicesummary.Dependencies{}, voicesummary.Config{}))

	got := registry.HasSecretInput("summarize-voice")

	if !got {
		t.Error("HasSecretInput(summarize-voice) = false, want true")
	}
}

// 条件: 停止済みで上限内の、bot を含む 3 トラックの録音のリンク。
// 内容: bot を除いた話者のマニフェストと音声を置き、その 2 トラックの文字起こしを始めて、受け付けの文を返す。チャンネルへは投稿しない。
func TestExecute_RecordingAccepted_StoresTargetAudioAndStartsJobs(t *testing.T) {
	s := newCommandScenario()

	result, err := s.execute(voiceInvocation(voiceLink))

	s.verify(t, result, err, acceptedOutcome(acceptedManifest()))
}

// 条件: 長さがちょうど上限（180 分）の録音。
// 内容: 上限は含むので、断らずに受け付ける。
func TestExecute_RecordingAtMaxLength_Accepts(t *testing.T) {
	s := newCommandScenario()
	s.recordings.Recording.Duration = 180 * time.Minute

	result, err := s.execute(voiceInvocation(voiceLink))

	s.verify(t, result, err, acceptedOutcome(manifestOf(180*time.Minute, "たろう", "はなこ")))
}

// 条件: 同じ interaction の実行が 2 回届く（worker の重複起動）。
// 内容: 2 回目は何も置かず、文字起こしも始めずに受け付けの文を返す。状態は 1 回目のあとのまま。
func TestExecute_SameInteractionTwice_StartsTranscriptionOnce(t *testing.T) {
	s := newCommandScenario()
	if _, err := s.execute(voiceInvocation(voiceLink)); err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	result, err := s.execute(voiceInvocation(voiceLink))

	s.verify(t, result, err, acceptedOutcome(acceptedManifest()))
}

// 条件: リンクの形式の誤り、bot の権限の不足、取得できない録音、別のサーバの録音、録音中、上限を超える長さ。
// 内容: それぞれの案内文と理由を返し、マニフェストも音声も置かず、文字起こしも投稿もしない。確認の順序はリンク、権限、取得、サーバ、録音中、長さ。
func TestExecute_PreconditionNotMet_RepliesGuidanceWithoutStoring(t *testing.T) {
	const (
		notRecordingLink = "Craig の録音リンクを指定してください。録音の開始時に Craig から届く、`/rec/` を含むリンクです。"
		botCannotPost    = "bot にこのチャンネルへメッセージとファイルを投稿する権限がないため、結果を投稿できません。サーバの管理者に相談してください。"
		unavailable      = "録音を取得できませんでした。リンクが正しいか、録音の保存期限が切れていないかを確かめてください。"
		otherGuild       = "このサーバで録音された録音だけを要約できます。"
		live             = "録音がまだ続いています。録音を停止してから、もう一度実行してください。"
		otherGuildID     = "1440000000000000999"
	)
	tests := []struct {
		name    string
		arrange func(s *commandScenario, inv *command.Invocation)
		want    command.Result
	}{
		{"not a link", func(_ *commandScenario, inv *command.Invocation) { inv.Options["link"] = "録音のリンクです" },
			command.Guided(notRecordingLink, "not_recording_link")},
		{"link of another host", func(_ *commandScenario, inv *command.Invocation) {
			inv.Options["link"] = "https://example.com/rec/AbCdEfGhIjKl?key=XyZ123"
		}, command.Guided(notRecordingLink, "not_recording_link")},
		{"link checked before permissions", func(_ *commandScenario, inv *command.Invocation) {
			inv.Options["link"] = "録音のリンクです"
			inv.AppPermissions = 0
		}, command.Guided(notRecordingLink, "not_recording_link")},
		{"bot cannot attach files", func(_ *commandScenario, inv *command.Invocation) {
			inv.AppPermissions = textPostPermissions.Remove(discord.PermissionAttachFiles)
		}, command.Guided(botCannotPost, "bot_cannot_post")},
		{"recording unavailable", func(s *commandScenario, _ *command.Invocation) {
			s.recordings.DescribeErr = craig.ErrRecordingUnavailable
		}, command.Guided(unavailable, "recording_unavailable")},
		{"recorded in another guild", func(s *commandScenario, _ *command.Invocation) {
			s.recordings.Recording.GuildID = otherGuildID
		}, command.Guided(otherGuild, "other_guild")},
		{"guild checked before live", func(s *commandScenario, _ *command.Invocation) {
			s.recordings.Recording.GuildID = otherGuildID
			s.recordings.Recording.Live = true
		}, command.Guided(otherGuild, "other_guild")},
		{"still recording", func(s *commandScenario, _ *command.Invocation) {
			s.recordings.Recording.Live = true
		}, command.Guided(live, "recording_live")},
		{"one minute over max", func(s *commandScenario, _ *command.Invocation) {
			s.recordings.Recording.Duration = 181 * time.Minute
		}, command.Guided("録音が長すぎるため、要約できません。扱える長さは 180 分までです。", "recording_too_long")},
		{"over configured max of 60 minutes", func(s *commandScenario, _ *command.Invocation) {
			s.cfg.MaxRecordingMinutes = 60
			s.recordings.Recording.Duration = 61 * time.Minute
		}, command.Guided("録音が長すぎるため、要約できません。扱える長さは 60 分までです。", "recording_too_long")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCommandScenario()
			inv := voiceInvocation(voiceLink)
			tt.arrange(s, &inv)

			result, err := s.execute(inv)

			s.verify(t, result, err, commandOutcome{Result: tt.want})
		})
	}
}

// 条件: 話者が Craig の bot だけの録音。
// 内容: 文字起こしを始めず、依頼者に通知して「発話なし」をチャンネルへ投稿し、置いたマニフェストを消して受け付けの文を返す。
func TestExecute_OnlyBotSpeaker_PostsNoSpeechReportAndDiscards(t *testing.T) {
	s := newCommandScenario()
	s.recordings.Recording.Speakers = []craig.Speaker{{Track: 3, Name: "craig", Bot: true}}

	result, err := s.execute(voiceInvocation(voiceLink))

	s.verify(t, result, err, commandOutcome{
		Result: command.Result{Text: acceptedText},
		Posts:  []reply.ChannelPost{noSpeechPost(manifestOf(recordingLength))},
	})
}

// 条件: 録音の情報の取得、ダウンロード、音声の保存、2 トラック目の文字起こしの開始、「発話なし」の投稿のいずれかが失敗する。
//
//	または、対象の話者のトラックがダウンロードした録音に無い。
//
// 内容: エラーを返し、その文に録音 ID と鍵を含まない。置いたマニフェストと音声は消し、置き場に何も残らない。
//
//	開始済みの文字起こしは止めない（完了しても、マニフェストが無いので無視される）。
func TestExecute_SystemFailure_ReturnsErrorWithoutLeavingData(t *testing.T) {
	errInjected := errors.New("injected failure")
	tests := []struct {
		name      string
		arrange   func(s *commandScenario)
		wantErr   error // nil なら、なんらかのエラー
		wantJobs  []voicesummarytest.Job
		wantPosts []reply.ChannelPost
	}{
		{"describe fails", func(s *commandScenario) { s.recordings.DescribeErr = errInjected }, errInjected, nil, nil},
		{"download fails", func(s *commandScenario) { s.recordings.DownloadErr = errInjected }, errInjected, nil, nil},
		{"put audio fails", func(s *commandScenario) { s.store.PutAudioErr = errInjected }, errInjected, nil, nil},
		{"second job start fails", func(s *commandScenario) { s.jobs.StartErrs = []error{nil, errInjected} }, errInjected,
			[]voicesummarytest.Job{{RequestID: requestID, Track: 1}}, nil},
		{"target track missing from download", func(s *commandScenario) { delete(s.recordings.Tracks, 2) }, nil,
			[]voicesummarytest.Job{{RequestID: requestID, Track: 1}}, nil},
		{"all target tracks missing from download", func(s *commandScenario) {
			delete(s.recordings.Tracks, 1)
			delete(s.recordings.Tracks, 2)
		}, voicesummary.ErrTrackMissing, nil, nil},
		{"no speech post fails", func(s *commandScenario) {
			s.recordings.Recording.Speakers = []craig.Speaker{{Track: 3, Name: "craig", Bot: true}}
			s.poster.Errs = []error{errInjected}
		}, errInjected, nil, []reply.ChannelPost{noSpeechPost(manifestOf(recordingLength))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCommandScenario()
			tt.arrange(s)

			result, err := s.execute(voiceInvocation(voiceLink))

			s.verifyFailure(t, result, err, tt.wantErr, commandOutcome{Jobs: tt.wantJobs, Posts: tt.wantPosts})
		})
	}
}
