# discord-ai-assistant

Discord の slash command から Amazon Translate と AWS Bedrock（Amazon Nova 2 Lite）を呼び出す bot。AWS Lambda で動き、常時稼働するサーバはない。

この文書は管理者向けの設定手順書。章の順に進めると、bot を使える状態になる。

コマンドの使い方は [internal/help/USAGE.md](internal/help/USAGE.md) を参照してください。

## 概要

### できること

| コマンド | 内容 |
|---|---|
| `/translate` | 入力した文章、または添付した `.txt` / `.html` / `.docx` ファイルを翻訳して返信する |
| `/ask` | 質問に AI が答えて返信する |
| `/image` | 文章から図を 1 枚生成して投稿する。引数は、文章（`prompt`、必須、最大 1000 文字）、縦横比（`aspect_ratio`）、元にする画像（`source`）、公開範囲（`visibility`）。`source` を指定すると、その画像の線と構図を元に図として描き直す |
| `/summarize` | 実行したチャンネルの会話を要約する（期間と bot の投稿を含めるかを指定できる） |
| `/summarize-voice` | Craig で録音したボイスチャンネルの会話を、話者名と経過時刻つきで文字起こしし、要約をチャンネルに投稿する |

### 構成

- Discord とは Interactions Endpoint（HTTP）だけで連携する。Gateway には接続しない。
- `ingress`（Lambda 関数、Function URL）が署名を検証し、3 秒以内に応答して `worker` を非同期に呼び出す。
- `worker`（Lambda 関数）が `/translate` では Amazon Translate（翻訳元の自動判定に Amazon Comprehend）を、`/ask` では Bedrock を呼び出し、結果を Discord に返信する。`/image` では、依頼の審査に Bedrock（ap-northeast-1）の Amazon Nova 2 Lite を、図の生成に `us-west-2` の Bedrock の画像生成モデル（Stability Stable Image Core、元にする画像があるときは Stability Stable Image Control Sketch）を呼び出す。bot トークンは SSM Parameter Store（SecureString）から起動時に読む。
- `/summarize-voice` では、`worker` が Craig から録音を取得して S3 に置き、話者ごとに Amazon Transcribe の文字起こしを始めたところで受け付けを返す。文字起こしが終わると、EventBridge が `voicereporter`（Lambda 関数）を呼び出す。`voicereporter` は文字起こしをまとめて Bedrock で要約し、チャンネルに投稿してから、S3 に置いたデータを消す。
- リリースは Git タグ `vX.Y.Z` の push で起動する GitHub Actions が行う。イメージを ECR に push し、CloudFormation の change set を作る。change set は管理者が確認して実行する。
- AWS リージョンは `ap-northeast-1`。ただし `/image` の画像生成モデルは `us-west-2` を使う。
- `/image` に渡した文章と画像は米国リージョン（`us-west-2`、`us-east-1`、`us-east-2`）へ送られる。Stable Image Core のデータは `us-west-2`、Control Sketch のデータは `us-east-1`、`us-east-2`、`us-west-2` のいずれかへ送られる。

### `/image` の注意点

- 生成できるのは図（図解・模式図、フローチャート・構成図、アイコン・ピクトグラム・ロゴ）だけ。写真、イラスト、グラフ・チャートなど、図以外を求める依頼は断られる。
- 図中の文字や内容の正確さは保証しない。
- 元にする画像（`source`）として受け付けるのは、次の条件を満たす画像。満たさない画像を指定すると、画像は生成されず、この条件が通知される。
  - 形式は PNG または JPEG
  - ファイルの大きさは 10,000,000 バイト以下
  - 各辺は 64 ピクセル以上
  - 総画素数は 25,000,000 以下（例: 5712×4284）。9,437,184 を超える画像は、縦横比を保って 9,437,184 以下に縮小してから送る
  - 縦横比は 1:2.5 から 2.5:1
- 元にする画像（`source`）を指定すると、縦横比（`aspect_ratio`）の指定は使われない。

### `/ask` の Web 検索

`/ask` は、回答に Web の情報が必要だとモデルが判断したときに、Web を検索する。検索結果を使った回答には、根拠にしたページのタイトルとリンクが、出典として付く。

- 検索には、東京リージョンの Amazon Bedrock AgentCore Web Search を使う。回答の生成は、これまでどおり国内の推論プロファイルで行う。
- 追加の設定作業は要らない。必要な権限（`bedrock-agentcore:InvokeWebSearch`）は `cfn/app.yaml` に含まれており、change set を実行すれば使える。
- 検索の回数に応じた費用がかかる。執筆時点では 1,000 回あたり 7 USD。1 回の `/ask` で行う検索は、最大で `ask.web_search.max_searches` 回（既定は 3 回）。最新の料金は <https://aws.amazon.com/bedrock/agentcore/pricing/> を参照する。
- 検索結果は、その質問への回答を作る間だけ使い、保存しない。

### `/summarize-voice` の注意点

- 録音の音声を Amazon Transcribe へ、文字起こしの本文を Bedrock の文章生成モデルへ送る。どちらも国内リージョン（`ap-northeast-1`）で処理する。
- 文字起こしの費用は「話者の人数 × 録音の長さ」に比例する。東京リージョンでは 1 分あたり約 0.006 USD で、話していない時間も含む。例: 5 人 × 60 分 = 300 分で約 1.8 USD。最新の料金は <https://aws.amazon.com/transcribe/pricing/> を参照する。
- 録音を停止してから実行する。録音中のリンクを渡すと、停止してから実行するよう案内される。
- bot が扱える録音の長さは 180 分まで（`summarize_voice.max_recording_minutes`）。
- Craig の無料枠では、1 回の録音は最大 6 時間、録音の保存は 7 日間。保存期間を過ぎた録音は要約できない。
- bot（音楽 bot、読み上げ bot など）の音声は、文字起こしの対象にならない。
- 文字起こしの時刻は、録音の最初の音声からの経過。
- 結果が届かないときは、もう一度実行する（完了の通知が失われることがある）。もう一度実行した分の費用もかかる。
- 置いた音声と途中結果は、投稿の直後に bot が消す。消せなかったものも、S3 のライフサイクルで 2 日以内に消える。

### 設定ファイル

調整値（入力と出力の上限、画像の大きさの上限、検索の回数の上限、利用するモデルなど）と、モデルへの指示文は、`internal/config/config.yaml` にまとめてある。

- 値を変えたら、イメージを作りなおして change set を実行する。設定ファイルはバイナリに同梱されるので、配備をせずに値を変えることはできない。
- 値に問題がある（読めない、必要な値が欠けている、範囲の外にある）と、worker は起動に失敗し、問題のあるキーがログに出る。
- テキスト生成のモデルは `text_model_id` で指定する。CloudFormation のパラメータ `BedrockModelId` は廃止した。既存のスタックを更新するときは、このパラメータを渡さない。
- `summarize`: /summarize の既定の期間（`default_period_hours`、24 時間）、要約に含める投稿の上限（`max_messages`、1000 件）、出力トークンの上限（`max_output_tokens`）、要約の指示文（`system_prompt`）
- `summarize_voice`: /summarize-voice の Craig の URL（`craig_base_url`）、扱える録音の長さの上限（`max_recording_minutes`、180 分）、出力トークンの上限（`max_output_tokens`）、要約の指示文（`system_prompt`）

## 前提

次を用意する。

- AWS アカウント（リージョンは `ap-northeast-1`）
- GitHub リポジトリ（このリポジトリを push 済みで、Actions が使えること）
- Docker
- AWS CLI（管理者の権限で認証済みであること）

### Lambda の同時実行数の上限の確認

ingress に reserved concurrency 10 を設定する。このとき、未予約の同時実行数として 100 以上を残す必要があるので、アカウントの上限が 110 以上であることを確認する。新しいアカウントは上限が低いことがある。

1. 上限を確認する。

   ```sh
   aws service-quotas get-service-quota \
     --region ap-northeast-1 \
     --service-code lambda \
     --quota-code L-B99A9384 \
     --query 'Quota.Value'
   ```

2. 値が 110 未満なら、引き上げを申請する。

   ```sh
   aws service-quotas request-service-quota-increase \
     --region ap-northeast-1 \
     --service-code lambda \
     --quota-code L-B99A9384 \
     --desired-value 1000
   ```

   AWS マネジメントコンソールの Service Quotas から申請してもよい。承認されるまで、後の章の「4. リリース」の change set の実行は待つ。

## 1. Discord アプリの作成

1. ブラウザで [Discord Developer Portal](https://discord.com/developers/applications) を開き、Discord アカウントでログインする。
2. 右上の「Create App」を押し、名前を入力してアプリを作成する。
3. 左のメニューの「General Information」を開き、「Application ID」と「Public Key」を控える（「3. 公開鍵とトークンの設定」と「6. コマンドの登録」で使う）。
4. 左のメニューの「Bot」を開く。bot ユーザはアプリに自動で作られる。
5. 「Public Bot」を無効（オフ）にする。無効にすると、アプリの所有者だけが bot をサーバへ追加できる。管理者以外が追加できないようにするための設定。
6. 「Bot」の画面で bot トークンを発行し（「Reset Token」）、控える。トークンは 1 度しか表示されない。チャット、ソースコード、ログに貼らない。
7. この bot に必要な権限（スコープ）は `applications.commands` と `bot` の 2 つ。招待 URL はこの 2 つで作る（「7. サーバへの招待と利用制限」）。
8. 「Bot」画面の「Privileged Gateway Intents」で「Message Content Intent」を有効にする。/summarize が会話の本文を読むために要る。無効のままだと、Discord は bot に本文を空で返し、要約できない。

## 2. AWS の初期設定

次の順に行う。

### 2-1. Bedrock で Amazon Nova 2 Lite の利用を始める

一般の商用リージョンには、「Model access」ページでの申請はない（GovCloud 専用）。初回の呼び出しで自動的にサブスクリプションが始まる。Amazon Nova 2 Lite は Anthropic のモデルではないので、Anthropic の初回利用時の用途フォームの提出は要らない。

1. 初回の呼び出しを行う IAM 主体に、`aws-marketplace:Subscribe`、`aws-marketplace:Unsubscribe`、`aws-marketplace:ViewSubscriptions` の権限があることを確認する。AWS Marketplace で使える支払い方法も必要。
2. AWS マネジメントコンソールでリージョンを「アジアパシフィック（東京）」`ap-northeast-1` にし、Amazon Bedrock を開く。
3. ナビゲーションで「Model catalog」を選び、Amazon Nova 2 Lite を選んで playground で開き、1 回呼び出す。この初回の呼び出しでサブスクリプションが始まる。
4. 状態を確認する。`agreementAvailability.status` が `AVAILABLE` なら利用できる。

   ```sh
   aws bedrock get-foundation-model-availability \
     --region ap-northeast-1 \
     --model-id amazon.nova-2-lite-v1:0 \
     --query 'agreementAvailability.status'
   ```

5. 本 bot は推論プロファイル `jp.amazon.nova-2-lite-v1:0` を使う。次のコマンドで一覧に出ることを確認する。

   ```sh
   aws bedrock list-inference-profiles \
     --region ap-northeast-1 \
     --query "inferenceProfileSummaries[?inferenceProfileId=='jp.amazon.nova-2-lite-v1:0'].inferenceProfileId"
   ```

### 2-2. Bedrock で Stability のモデルの利用を始める

`/image` が使う Stability Stable Image Core（`stability.stable-image-core-v1:1`）と Stability Stable Image Control Sketch（`us.stability.stable-image-control-sketch-v1:0`）の利用を始める。どちらも `us-west-2` で行う。

1. 2-1 と同じ権限と支払い方法（`aws-marketplace:Subscribe`、`aws-marketplace:Unsubscribe`、`aws-marketplace:ViewSubscriptions` の権限と、AWS Marketplace で使える支払い方法）が、初回の呼び出しを行う IAM 主体に必要。
2. Control Sketch の入力に使う画像として、各辺 64 ピクセル以上の PNG を 1 つ用意し、作業ディレクトリに `sketch.png` という名前で置く。
3. CLI で各モデルを 1 回ずつ呼び出す。Control Sketch は推論プロファイル経由でしか呼び出せないため、どちらも CLI で行う。この初回の呼び出しでサブスクリプションが始まり、完了まで最大 15 分かかる。呼び出しには料金がかかる（Control Sketch は 1 回 0.07 USD）。

   ```sh
   # Stability Stable Image Core
   printf '{"prompt":"a simple flowchart"}' > core-body.json
   aws bedrock-runtime invoke-model \
     --region us-west-2 \
     --model-id stability.stable-image-core-v1:1 \
     --content-type application/json \
     --body fileb://core-body.json \
     core-out.json

   # Stability Stable Image Control Sketch
   printf '{"prompt":"a simple flowchart","image":"%s"}' "$(base64 < sketch.png | tr -d '\n')" > sketch-body.json
   aws bedrock-runtime invoke-model \
     --region us-west-2 \
     --model-id us.stability.stable-image-control-sketch-v1:0 \
     --content-type application/json \
     --body fileb://sketch-body.json \
     sketch-out.json
   ```
4. 状態を確認する。各モデルで `agreementAvailability.status` が `AVAILABLE` なら利用できる。

   ```sh
   aws bedrock get-foundation-model-availability \
     --region us-west-2 \
     --model-id <モデル ID>
   ```

### 2-3. `cfn/bootstrap.yaml` のスタックを作る

ECR リポジトリ、リリース用ロールを作る。管理者が最初に 1 回だけ行う。

パラメータ（`cfn/bootstrap.yaml`）:

| パラメータ | 内容 | 既定値 |
|---|---|---|
| `GitHubRepository` | リリースを実行する GitHub リポジトリ（`owner@owner_id/repo@repo_id` 形式）。必須 | なし |
| `RepositoryName` | ECR リポジトリ名 | `lambda-container/discord-ai-assistant` |
| `AppStackName` | アプリ用スタックの名前（`cfn/app.yaml`） | `discord-ai-assistant` |
| `GitHubOidcProviderArn` | GitHub OIDC プロバイダの ARN。必須 | なし |

1. Immutable Subject Claim を有効にする。2026 年 7 月 15 日より前に作成したリポジトリでは、リポジトリまたは organization の Actions OIDC 設定で有効にする。
2. GitHub CLI で owner ID と repository ID を調べる。

   ```sh
   gh api repos/<owner>/<repo> --jq '"\(.owner.login)@\(.owner.id)/\(.name)@\(.id)"'
   ```

3. スタックを作る。`<owner>@<owner_id>/<repo>@<repo_id>` は直前のコマンドの出力に置き換える。既存のスタックを更新する場合も同じコマンドで `GitHubRepository` の値を更新する。

   ```sh
   aws cloudformation deploy \
     --region ap-northeast-1 \
     --stack-name discord-ai-assistant-bootstrap \
     --template-file cfn/bootstrap.yaml \
     --capabilities CAPABILITY_IAM \
     --parameter-overrides 'GitHubRepository=<owner>@<owner_id>/<repo>@<repo_id>' 'GitHubOidcProviderArn=<GitHub OIDC プロバイダの ARN>'
   ```

4. 出力を取得する。`RepositoryUri` と `ReleaseRoleArn` を控える。

   ```sh
   aws cloudformation describe-stacks \
     --region ap-northeast-1 \
     --stack-name discord-ai-assistant-bootstrap \
     --query 'Stacks[0].Outputs'
   ```

### 2-4. GitHub のリポジトリ変数とシークレットを設定する

1. GitHub のリポジトリで「Settings」→「Secrets and variables」→「Actions」→「Variables」タブを開く。
2. 「New repository variable」で次を作る。ここでは「Secrets」ではなく「Variables」に入れる。

   | 変数名 | 値 |
   |---|---|
   | `APP_STACK_NAME` | アプリ用スタックの名前。`AppStackName` の値と同じにする（既定は `discord-ai-assistant`） |
   | `IMAGE_NAME` | ECR リポジトリ名。`RepositoryName` の値と同じにする（既定は `lambda-container/discord-ai-assistant`） |

3. GitHub のリポジトリで「Settings」→「Secrets and variables」→「Actions」→「Secrets」タブを開く。
4. 「New repository secret」で次を作る。

   | シークレット名 | 値 |
   |---|---|
   | `AWS_RELEASE_ROLE_ARN` | 2-3 の出力 `ReleaseRoleArn` |
   | `DISCORD_PUBLIC_KEY` | 「3. 公開鍵とトークンの設定」で入れる（この時点では作らなくてよい） |


## 3. 公開鍵とトークンの設定

bot トークンは worker が起動時に読む。**リリース（「4. リリース」）より前に行うこと。** 保存していないと worker が起動できず、すべてのコマンドが返信しない。

1. 「1. Discord アプリの作成」で控えた Public Key を、リポジトリのシークレット `DISCORD_PUBLIC_KEY` に入れる。

   GitHub の画面から入れる場合は、「2-4」と同じ「Secrets」タブに作る。
2. bot トークンを SSM Parameter Store に SecureString で保存する。`--key-id` は付けない（既定の `aws/ssm` キーを使う）。

   ```sh
   aws ssm put-parameter \
     --region ap-northeast-1 \
     --name /discord-ai-assistant/bot-token \
     --type SecureString \
     --value '<bot トークン>'
   ```

   シェルの履歴にトークンを残したくないときは、`--value` を入力プロンプトや環境変数から渡す。

## 4. リリース

タグの push で GitHub Actions の `Release` ワークフローが動く。テスト、イメージのビルドと ECR への push、change set の作成までを行う。スタックの更新はしない。change set は公式の `aws-actions/aws-cloudformation-github-deploy` で作る。スタックが無ければ CREATE、あれば UPDATE の change set になる。

1. タグを作って push する。

   ```sh
   git tag v1.0.0 && git push origin v1.0.0
   ```

2. GitHub の「Actions」タブで `Release` ワークフローの結果を開く。成功していれば、最後のステップ「Write change set summary」の出力とジョブのサマリに、change set の名前に加えて、変更されるリソースの一覧（値は出さない）と AWS コンソールのスタック画面へのリンクが出る。タグ `v1.0.0` の change set 名は `release-v1-0-0`（バージョンの `.` が `-` に変わる）。

   ```
   ## CloudFormation change set

   - Stack: `discord-ai-assistant`
   - Change set: `release-v1-0-0`
   ```

3. change set の内容を確認する。`<スタック名>` は `APP_STACK_NAME` の値。

   ```sh
   aws cloudformation describe-change-set \
     --region ap-northeast-1 \
     --stack-name <スタック名> \
     --change-set-name release-v1-0-0
   ```

4. 内容に問題がなければ実行する。

   ```sh
   aws cloudformation execute-change-set \
     --region ap-northeast-1 \
     --stack-name <スタック名> \
     --change-set-name release-v1-0-0
   ```

5. スタックの作成または更新が終わるまで待つ。

   ```sh
   aws cloudformation wait stack-create-complete --region ap-northeast-1 --stack-name <スタック名>
   ```

   2 回目以降のリリース（スタックの更新）では `stack-update-complete` を使う。
6. 同じタグ名は再利用できない（ECR のタグは変更不可）。やり直すときは新しいバージョン（例: `v1.0.1`）のタグを切る。
   次の 2 つの場合は、ワークフローが失敗する。
   - スタックが `ROLLBACK_COMPLETE` のとき: リリース用ロールにスタックの削除権限が無いため、失敗する。管理者が `aws cloudformation delete-stack --stack-name <スタック名>` でスタックを削除してから、新しいバージョンのタグでやり直す。
   - 初回の CREATE の change set を実行しないまま次のタグを push したとき: スタックが `REVIEW_IN_PROGRESS` のまま UPDATE の change set を作ろうとして失敗する。先の change set を実行するか、スタックを削除してからやり直す。

## 5. 受け口の URL の設定

1. スタックの出力 `FunctionUrl` を取得する。

   ```sh
   aws cloudformation describe-stacks \
     --region ap-northeast-1 \
     --stack-name <スタック名> \
     --query "Stacks[0].Outputs[?OutputKey=='FunctionUrl'].OutputValue" \
     --output text
   ```

2. Discord Developer Portal で対象のアプリを開き、「General Information」を開く。
3. 「Interactions Endpoint URL」の欄に、1 の URL を貼って保存する。Discord が署名つきの PING を送り、ingress が正しく応答すると保存できる。
4. 保存に失敗したときは、`DISCORD_PUBLIC_KEY` の値（「3」）と、ingress のログ（CloudWatch Logs の `/aws/lambda/<スタック名>-ingress`）を確認する。

## 6. コマンドの登録

`/translate`、`/ask`、`/image`、`/summarize`、`/summarize-voice`、`/help` を Discord に登録する。ローカルでビルドしたイメージの中にある `/register` を使う。

1. デプロイ済みのリリースタグを checkout する。`vX.Y.Z` は「4. リリース」で push したタグ。登録されるのはビルドしたソースのコマンド定義なので、デプロイ済みと同じタグでビルドしないと、Discord の画面の選択肢と実際の動作がずれる。

   ```sh
   git checkout vX.Y.Z
   ```

2. 登録用イメージをビルドして登録を実行する。

   ```sh
   make image-register register
   ```

   AWS のプロファイルを変えるときは `AWS_PROFILE` を付ける。

   ```sh
   make image register AWS_PROFILE=<プロファイル名>
   ```

   成功すると登録したコマンドの名前が 1 行ずつ出る。終わったら作業ブランチに戻す。

   ```sh
   git switch -
   ```

使う AWS の認証情報には、`/discord-ai-assistant/bot-token` への `ssm:GetParameter` と、その SecureString を復号する権限（パラメータの KMS キーへの `kms:Decrypt`）が必要。

## 7. サーバへの招待と利用制限

### 7-1. bot をサーバへ招待する

1. 次の URL の `<Application ID>` を置き換えて、ブラウザで開く。スコープは `bot` と `applications.commands` の 2 つ。

   ```
   https://discord.com/oauth2/authorize?client_id=<Application ID>&scope=bot%20applications.commands&permissions=274879056896
   ```

2. 招待先のサーバを選んで認可する。「Public Bot」が無効なので、アプリの所有者だけが追加できる。サーバの「サーバーを管理」権限が必要。
3. bot には、次の権限を付ける（上の URL の `permissions=274879056896`）。ほかの権限は要らない。招待済みの bot には、サーバの設定でこれらをロールに付ける。
   - /summarize のために「チャンネルを見る」と「メッセージ履歴を読む」。権限が無いチャンネルで /summarize を実行すると、bot は権限が無いことを案内する。
   - /summarize-voice の結果を投稿するために「メッセージを送信」「スレッドでメッセージを送信」「ファイルを添付」と、ボイスチャンネルのテキストチャットのために「接続」。権限が無いチャンネルで /summarize-voice を実行すると、bot は処理を始めず、結果を投稿できないことを案内する。

/summarize は、チャンネルの会話の本文と投稿者の表示名を Amazon Bedrock の文章生成モデルに送る。会話の本文と要約は、成功時も失敗時もログに残さない。実行者がそのチャンネルのメッセージ履歴を読めない場合は要約しない。

### 7-2. コマンドを使える人とチャンネルを制限する

サーバの管理者が行う。

1. Discord のサーバ名のメニューから「サーバー設定」を開く。
2. 「連携サービス」を開き、この bot のアプリを選んで「管理」を押す。
3. コマンドごと、またはアプリ全体で、使えるロール・メンバー・チャンネルを設定する。既定の `@everyone` の許可を外し、許可するロールとチャンネルだけを追加する。

### 7-3. Craig を導入して録音を要約する

`/summarize-voice` は、録音 bot「Craig」の録音を要約する。Craig は別の開発者が提供する bot で、この bot とは別に導入する。手順 1 はサーバの管理者が 1 回だけ行い、手順 2〜4 は録音のたびに行う。

1. Craig の公式サイト（<https://craig.chat>）から、Craig をサーバに招待する。
2. ボイスチャンネルに参加し、Craig の `/join` を実行して録音を始める。録音を始めた人に、Craig から DM で録音リンク（`https://craig.horse/rec/...?key=...`）が届く。
3. 会話が終わったら、Craig の `/stop` を実行して録音を止める。
4. 結果を投稿したいチャンネルで `/summarize-voice link:<録音リンク>` を実行する。受け付けの返事は、実行した本人にだけ見える。文字起こしと要約が終わると、そのチャンネルに結果と `transcript.txt` が投稿され、実行した本人に通知が届く。

- 録音リンクには、録音を取り出すための鍵が含まれる。リンクはチャンネルに貼らず、`/summarize-voice` の `link` に直接渡す。`link` の値は、ほかの人には見えない。
- 録音したサーバと同じサーバで実行する。ほかのサーバの録音は断られる。

## 開発

自動テストをローカルで実行する。

1. Go を入れる。バージョンは `go.mod` で指定している（Go 1.27.4）。

2. 静的解析と単体テストを実行する。

   ```sh
   make vet test
   ```

3. 結合テスト用のイメージ（`it` ステージ。Lambda のエミュレータ入り）をビルドする（Docker が必要）。

   ```sh
   make image-it
   ```

4. 結合テストを実行する。実際の Discord と AWS には接続しない。3 と 4 は `make it` でまとめて実行できる。

   ```sh
   make test-it
   ```

5. Go を入れずに golang コンテナで実行する場合（Docker が必要）。`make docker-test-it` は Linux の Docker Engine で動かすこと。Makefile がソケットの gid を GNU の `stat -c %g` で取っており（macOS の `stat` では動かない）、コンテナを `--network host` で動かしているため（Docker Desktop ではホストのネットワークにならず、兄弟コンテナの公開ポートに localhost で届かない）。macOS の Docker Desktop などは対象外。イメージは `go.mod` の `go` 行から決まる。モジュールとビルドのキャッシュは名前付きボリュームに残る。

   ```sh
   make docker-test
   make image-it docker-test-it
   ```