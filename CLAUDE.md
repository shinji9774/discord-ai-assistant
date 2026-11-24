# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 概要

Go 製の Discord bot。AWS Lambda 上で動き、Lambda 側は Discord と Interactions Endpoint（HTTP）だけでつなぐ。録音タスク（`cmd/recorder`、ECS Fargate）だけが Gateway と Voice に接続する。管理者向けのセットアップ手順は `README.md`、コマンドの利用者向け説明は、`/help` が返す概要が `internal/help/USAGE.md`、詳細が `docs/`（GitHub Pages で公開）にある。上限値などの詳細は `docs/` だけに書く。

## コマンド

Go のバージョンは `go.mod` で指定する。`make`（引数なし）でターゲット一覧を表示する。

| 目的 | コマンド |
|---|---|
| 静的解析 + 単体テスト | `make vet test` |
| 結合テスト（IT イメージのビルド + 実行） | `make it`（= `make image-it test-it`） |
| Go なしで単体テスト | `make docker-test` |
| Go なしで結合テスト | `make image-it docker-test-it`（Linux の Docker Engine 専用。macOS の Docker Desktop では動かない） |
| GitHub Actions ワークフローの lint | `make lint`（actionlint のみ。Go 用の linter は導入していない） |
| Discord へのコマンド登録 | `make register`（SSM から bot トークンを取得する。aws CLI の認証が必要） |

単一テストの実行:

```sh
# 単体テスト
go test -run '^TestExecute_RecordingAccepted_StoresTargetAudioAndStartsJobs$' ./internal/voicesummary/
# 結合テスト（事前に make image-it が必要）
go test -tags integration -count=1 -run '<テスト名>' ./test/integration/...
```

CI（`.github/workflows/ci.yml`）は `make docker-test` と `make image-it docker-test-it` を実行する。`v*.*.*` タグの push で `release.yml` が ECR へ push し、CloudFormation の change set を作る（実行は管理者が手動で行う）。テストが通ると `docs/` を GitHub Pages に公開する（`release` ジョブとは独立）。`release.yml` をタグを選んで手動実行すると、公開だけを行う。

## アーキテクチャ

### 実行単位（`cmd/`）

`cmd/` の各 main は依存の組み立てだけを行う。ロジックは `internal/` に置く。

- `ingress`: Lambda（Function URL）。Discord の署名（`x-signature-ed25519` / `x-signature-timestamp`）を検証する。Command には type 5 の遅延応答を返し、worker を非同期に呼ぶ。`help` だけは ingress が自分で応答する。
- `worker`: コマンドを実行し、`reply.Responder` で遅延応答を更新する。失敗通知を送る時間（`reply_reserve_seconds`）を残してタイムアウトを管理する。
- `voicereporter`: Amazon Transcribe の完了を EventBridge 経由で受け、音声要約を投稿する。
- `voicefailure`: voicereporter が非同期呼び出しの再試行（2 回）を使い切ったときの失敗の宛先（OnFailure）。依頼者に失敗を投稿し、S3 のデータを消す。
- `recorder`: ECS Fargate のタスク。worker が `RunTask` の overrides で渡した `GUILD_ID` / `REQUEST_ID` の録音を 1 件行う。SIGTERM で ctx をキャンセルする。失敗は exit 1 で終え、後始末は recordingfailure に任せる。
- `recordingfailure`: EventBridge の ECS Task State Change（STOPPED）を受け、止まった録音タスクの後始末をする。recorder コンテナが exit 0（引き渡し済み）なら、同じ依頼のロックを解除するだけにする。それ以外の場合、ロックが同じ依頼のものなら、音声を消して失敗を投稿し、表示名とロックを戻す。
- `register`: Discord へコマンドを登録する CLI。

ingress から worker へは `command.Invocation`（`internal/command/command.go`）を JSON で渡す。

### パッケージ構成

package by feature。`internal/` 直下は機能パッケージ（`ask`, `translate`, `imagegen`, `summarize`, `voicesummary`, `voicerecording`, `websearch`, `help`）と基盤パッケージ（`command`, `catalog`, `config`, `llm`, `reply`, `ingress`, `worker`, `register`, `bottoken`, `discordapi`）に分かれる。

- `command.Registry` がコマンド名で実行対象を探す。結果は `command.Result` で返す。利用者が直せる状況（権限不足、入力不備など）は `command.Guided(...)` で案内として返し、失敗とは区別する。
- `voicerecording` のサブパッケージ `discordvoice` は、Gateway・Voice・DAVE の実装を閉じ込める。import してよいのは `cmd/recorder` と `tools/voiceprobe` だけ。fake は `voicerecordingtest` に置く。
- `SecretInput` を持つコマンド（`/summarize-voice` の Craig リンクなど）は、ephemeral（flags=64）で応答し、入力をログに出さない。
- `PrivateReply` を実装するコマンド（`/record-optout` など）も ephemeral で応答するが、入力はログに出してよい。`Registry.RepliesPrivately` が `PrivateReply` と `SecretInput` のどちらかを持つかを返し、ingress が応答の可視性を決める。
- 依存の注入は `catalog.Dependencies`（`internal/catalog/catalog.go`）に集約する。新しいコマンドは機能パッケージを作り、`catalog` に登録する。
- 各機能は interface（`llm.TextGenerator`, `llm.ToolGenerator`, `websearch.Searcher` など）に依存する。実装は AWS（Bedrock Runtime / Bedrock AgentCore Web Search / Amazon Translate / Transcribe / S3 / SSM）。画像生成は `us-west-2` の Bedrock Stability モデルを使う。

### 設定

- 調整値とモデルへの指示文は `internal/config/config.yaml` にまとめる。`//go:embed` でバイナリに同梱されるため、値を変えたらイメージを作り直す必要がある。
- `config.Parse` は未知のキーを拒否する（`KnownFields(true)`）。値の検証は `internal/config/check` が `go-playground/validator` の `validate` タグで行い、エラーには yaml のキー名を付ける。独自タグに `notblank` がある。
- 各機能パッケージが自前の `Config` struct を持ち、`config.Config` がそれらを束ねる。テストでは `internal/config/configtest` を使う。
- 環境変数は Lambda と ECS タスクの配線情報だけに使う（`DISCORD_PUBLIC_KEY`, `WORKER_FUNCTION_NAME`, `BOT_TOKEN_PARAMETER_NAME`, `VOICE_BUCKET_NAME`）。worker は録音タスクの起動に `RECORDER_CLUSTER_ARN`, `RECORDER_TASK_DEFINITION_ARN`, `RECORDER_SUBNET_IDS`, `RECORDER_SECURITY_GROUP_ID` を使い、recorder は RunTask の overrides で渡される `GUILD_ID` と `REQUEST_ID` を読む。bot トークン本体は SSM Parameter Store から読む。`DISCORD_API_BASE_URL` と `CRAIG_BASE_URL` は結合テストで fake へ向けるためのもの。

### `/summarize-voice` のフロー

1. worker が `voicesummary.Command` を実行する。Craig のリンクを解析し、投稿権限・録音の所属サーバ・録音状態・長さを確認する。問題があれば案内を返す。
2. 音声を S3 に置き、話者ごとに Transcribe ジョブを開始して受け付けを返す。
3. Transcribe 完了で `voicereporter` が起動する（`internal/voicesummary/reporter.go`）。manifest を読み、`ClaimReport` で重複投稿を防ぎ、LLM で要約する。要約は全文の `transcript.txt` を添付して投稿し、最後に S3 のデータを消す。
4. 失敗は `stage*` 定数で段階をログに出す。文字起こしと要約の本文はログに出さない。

### `/record-start` のフロー

1. worker が `voicerecording.StartCommand` を実行する。実行者のボイスチャンネル参加と bot の権限を確かめ、問題があれば案内を返す。`SessionStore.Acquire` でサーバごとのロックを取り、`RunTask` で recorder を起動する。起動に失敗したらロックを外す。
2. recorder（`internal/voicerecording/recorder.go`）がロックの依頼 ID を確かめ、ボイスチャンネルに参加し、`MarkStarted` で開始を記録して周知する。
3. `/record-stop`、参加者 0 人、上限、切断のどれかで `End` に理由を書き、退出して音声を閉じる（除外した人は外す）。
4. マニフェストと音声を S3 に置き、終了を投稿し、表示名を戻して、話者ごとに Transcribe ジョブを始める。以降は `/summarize-voice` のフロー 3 と同じ（voicereporter が要約を投稿する）。
5. 文字起こしを始めた時点を引き渡し完了とし、その後のロック解除の失敗は Warn ログにとどめて成功で終える。終了の投稿の失敗も Warn にとどめる。文字起こしを始められなければ、recorder が音声とマニフェストを消してから exit 1 で終える。
6. 引き渡し前の失敗（参加、保存など）で exit 1 になると、ECS の STOPPED を受けた recordingfailure が後始末をする。投稿が失敗した場合だけエラーを返して Lambda の再試行に任せ、ロック解除の失敗は Warn にとどめる。
7. recorder が引き渡しまで終えて exit 0 で止まった場合（ロック解除に失敗してロックが残ったとき）は、recordingfailure は STOPPED の `containers[]` の exit code 0 でそれを見分け、音声を消さず投稿もせず、ロックだけを外す。ロック解除の失敗はエラーにして再試行に任せる（投稿が無いので重複しない）。

## テスト

- テスト名は `Test<対象>_<条件>_<期待>` の形式。
- モックライブラリは使わない。インメモリの fake を各機能の `<pkg>test/` サブパッケージ（`llmtest`, `replytest`, `voicesummarytest` など）に置く。fake 自体のテストも書く。
- 非公開識別子のテストには `export_test.go` を使う。
- 結合テスト（`test/integration/`、ビルドタグ `integration`）は、Lambda RIE のコンテナを testcontainers で起動し、ingress と worker をつなぐ。外部サービスは `test/*fake/` の HTTP fake で置き換える。イメージは `IT_IMAGE`（既定 `discord-ai-assistant:it`）で指定する。
