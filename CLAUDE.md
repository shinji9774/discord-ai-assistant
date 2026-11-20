# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 概要

Go 製の Discord bot。AWS Lambda 上で動き、Discord とは Interactions Endpoint（HTTP）だけでつなぐ（Gateway には接続しない）。管理者向けのセットアップ手順は `README.md`、コマンドの利用者向け説明は `internal/help/USAGE.md` にある。

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

CI（`.github/workflows/ci.yml`）は `make docker-test` と `make image-it docker-test-it` を実行する。`v*.*.*` タグの push で `release.yml` が ECR へ push し、CloudFormation の change set を作る（実行は管理者が手動で行う）。

## アーキテクチャ

### 実行単位（`cmd/`）

`cmd/` の各 main は依存の組み立てだけを行う。ロジックは `internal/` に置く。

- `ingress`: Lambda（Function URL）。Discord の署名（`x-signature-ed25519` / `x-signature-timestamp`）を検証する。Command には type 5 の遅延応答を返し、worker を非同期に呼ぶ。`help` だけは ingress が自分で応答する。
- `worker`: コマンドを実行し、`reply.Responder` で遅延応答を更新する。失敗通知を送る時間（`reply_reserve_seconds`）を残してタイムアウトを管理する。
- `voicereporter`: Amazon Transcribe の完了を EventBridge 経由で受け、音声要約を投稿する。
- `voicefailure`: voicereporter が非同期呼び出しの再試行（2 回）を使い切ったときの失敗の宛先（OnFailure）。依頼者に失敗を投稿し、S3 のデータを消す。
- `register`: Discord へコマンドを登録する CLI。

ingress から worker へは `command.Invocation`（`internal/command/command.go`）を JSON で渡す。

### パッケージ構成

package by feature。`internal/` 直下は機能パッケージ（`ask`, `translate`, `imagegen`, `summarize`, `voicesummary`, `websearch`, `help`）と基盤パッケージ（`command`, `catalog`, `config`, `llm`, `reply`, `ingress`, `worker`, `register`, `bottoken`, `discordapi`）に分かれる。

- `command.Registry` がコマンド名で実行対象を探す。結果は `command.Result` で返す。利用者が直せる状況（権限不足、入力不備など）は `command.Guided(...)` で案内として返し、失敗とは区別する。
- `SecretInput` を持つコマンド（`/summarize-voice` の Craig リンクなど）は、ephemeral（flags=64）で応答し、入力をログに出さない。
- 依存の注入は `catalog.Dependencies`（`internal/catalog/catalog.go`）に集約する。新しいコマンドは機能パッケージを作り、`catalog` に登録する。
- 各機能は interface（`llm.TextGenerator`, `llm.ToolGenerator`, `websearch.Searcher` など）に依存する。実装は AWS（Bedrock Runtime / Bedrock AgentCore Web Search / Amazon Translate / Transcribe / S3 / SSM）。画像生成は `us-west-2` の Bedrock Stability モデルを使う。

### 設定

- 調整値とモデルへの指示文は `internal/config/config.yaml` にまとめる。`//go:embed` でバイナリに同梱されるため、値を変えたらイメージを作り直す必要がある。
- `config.Parse` は未知のキーを拒否する（`KnownFields(true)`）。値の検証は `internal/config/check` が `go-playground/validator` の `validate` タグで行い、エラーには yaml のキー名を付ける。独自タグに `notblank` がある。
- 各機能パッケージが自前の `Config` struct を持ち、`config.Config` がそれらを束ねる。テストでは `internal/config/configtest` を使う。
- 環境変数は Lambda の配線情報だけに使う（`DISCORD_PUBLIC_KEY`, `WORKER_FUNCTION_NAME`, `BOT_TOKEN_PARAMETER_NAME`, `VOICE_BUCKET_NAME`）。bot トークン本体は SSM Parameter Store から読む。`DISCORD_API_BASE_URL` と `CRAIG_BASE_URL` は結合テストで fake へ向けるためのもの。

### `/summarize-voice` のフロー

1. worker が `voicesummary.Command` を実行する。Craig のリンクを解析し、投稿権限・録音の所属サーバ・録音状態・長さを確認する。問題があれば案内を返す。
2. 音声を S3 に置き、話者ごとに Transcribe ジョブを開始して受け付けを返す。
3. Transcribe 完了で `voicereporter` が起動する（`internal/voicesummary/reporter.go`）。manifest を読み、`ClaimReport` で重複投稿を防ぎ、LLM で要約する。要約は全文の `transcript.txt` を添付して投稿し、最後に S3 のデータを消す。
4. 失敗は `stage*` 定数で段階をログに出す。文字起こしと要約の本文はログに出さない。

## テスト

- テスト名は `Test<対象>_<条件>_<期待>` の形式。
- モックライブラリは使わない。インメモリの fake を各機能の `<pkg>test/` サブパッケージ（`llmtest`, `replytest`, `voicesummarytest` など）に置く。fake 自体のテストも書く。
- 非公開識別子のテストには `export_test.go` を使う。
- 結合テスト（`test/integration/`、ビルドタグ `integration`）は、Lambda RIE のコンテナを testcontainers で起動し、ingress と worker をつなぐ。外部サービスは `test/*fake/` の HTTP fake で置き換える。イメージは `IT_IMAGE`（既定 `discord-ai-assistant:it`）で指定する。
