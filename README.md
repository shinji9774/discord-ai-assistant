# discord-ai-assistant

Discord の slash command から AWS Bedrock（Claude Haiku 5.5）を呼び出す bot。AWS Lambda で動き、常時稼働するサーバはない。

この文書は管理者向けの設定手順書。章の順に進めると、bot を使える状態になる。

## 概要

### できること

| コマンド | 内容 |
|---|---|
| `/translate` | 入力した文章を翻訳して返信する |
| `/ask` | 質問に AI が答えて返信する |

### 構成

- Discord とは Interactions Endpoint（HTTP）だけで連携する。Gateway には接続しない。
- `ingress`（Lambda 関数、Function URL）が署名を検証し、3 秒以内に応答して `worker` を非同期に呼び出す。
- `worker`（Lambda 関数）が Bedrock を呼び出し、結果を Discord に返信する。bot トークンは SSM Parameter Store（SecureString）から起動時に読む。
- リリースは Git タグ `vX.Y.Z` の push で起動する GitHub Actions が行う。イメージを ECR に push し、CloudFormation の change set を作る。change set は管理者が確認して実行する。
- AWS リージョンは `ap-northeast-1`。

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

## 2. AWS の初期設定

次の順に行う。

### 2-1. Bedrock で Claude Haiku 5.5 の利用を始める

一般の商用リージョンには、「Model access」ページでの申請はない（GovCloud 専用）。初回の呼び出しで自動的にサブスクリプションが始まる。Claude Haiku 5.5 は Anthropic の初回利用時の用途フォームの対象外なので、フォームの提出は要らない。

1. 初回の呼び出しを行う IAM 主体に、`aws-marketplace:Subscribe`、`aws-marketplace:Unsubscribe`、`aws-marketplace:ViewSubscriptions` の権限があることを確認する。AWS Marketplace で使える支払い方法も必要。
2. AWS マネジメントコンソールでリージョンを「アジアパシフィック（東京）」`ap-northeast-1` にし、Amazon Bedrock を開く。
3. ナビゲーションで「Model catalog」を選び、Claude Haiku 5.5 を選んで playground で開き、1 回呼び出す。この初回の呼び出しでサブスクリプションが始まる。
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

### 2-2. `cfn/bootstrap.yaml` のスタックを作る

ECR リポジトリ、リリース用ロールを作る。管理者が最初に 1 回だけ行う。

パラメータ（`cfn/bootstrap.yaml`）:

| パラメータ | 内容 | 既定値 |
|---|---|---|
| `GitHubRepository` | リリースを実行する GitHub リポジトリ（`owner@owner_id/repo@repo_id` 形式）。必須 | なし |
| `RepositoryName` | ECR リポジトリ名 | `discord-ai-assistant` |
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

### 2-3. GitHub のリポジトリ変数を設定する

1. GitHub のリポジトリで「Settings」→「Secrets and variables」→「Actions」→「Variables」タブを開く。
2. 「New repository variable」で次を作る。ここでは「Secrets」ではなく「Variables」に入れる。

   | 変数名 | 値 |
   |---|---|
   | `APP_STACK_NAME` | アプリ用スタックの名前。`AppStackName` の値と同じにする（既定は `discord-ai-assistant`） |

3. GitHub のリポジトリで「Settings」→「Secrets and variables」→「Actions」→「Secrets」タブを開く。
4. 「New repository variable」で次を作る。

   | 変数名 | 値 |
   |---|---|
   | `AWS_RELEASE_ROLE_ARN` | 2-2 の出力 `ReleaseRoleArn` |
   | `DISCORD_PUBLIC_KEY` | 「3. 公開鍵とトークンの設定」で入れる（この時点では作らなくてよい） |


## 3. 公開鍵とトークンの設定

bot トークンは worker が起動時に読む。**リリース（「4. リリース」）より前に行うこと。** 保存していないと worker が起動できず、すべてのコマンドが返信しない。

1. 「1. Discord アプリの作成」で控えた Public Key を、リポジトリ変数 `DISCORD_PUBLIC_KEY` に入れる。

   GitHub の画面から入れる場合は、「2-3」と同じ「Secrets」タブに作る。
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

2. GitHub の「Actions」タブで `Release` ワークフローの結果を開く。成功していれば、最後のステップ「Show change set name」の出力とジョブのサマリに、change set の名前が出る。タグ `v1.0.0` の change set 名は `release-v1-0-0`（バージョンの `.` が `-` に変わる）。

   ```
   Change set created: release-v1-0-0 (stack: discord-ai-assistant)
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

`/translate` と `/ask` を Discord に登録する。リリースで作ったイメージの中に `/register` がある。

1. bot トークンを SSM から取り出す。

   ```sh
   export DISCORD_BOT_TOKEN=$(aws ssm get-parameter \
     --region ap-northeast-1 \
     --name /discord-ai-assistant/bot-token \
     --with-decryption \
     --query Parameter.Value \
     --output text)
   export DISCORD_APPLICATION_ID=<Application ID>
   ```

2. 登録を実行する。`<image>` はリリースで push したイメージ（`<RepositoryUri の値>:v1.0.0`）。ECR から pull するため、先に `aws ecr get-login-password --region ap-northeast-1 | docker login --username AWS --password-stdin <ECR のレジストリ>` でログインしておく。

   ```sh
   make register IMAGE=<image>
   ```

   成功すると登録したコマンドの名前が 1 行ずつ出る。
3. 作業が終わったら、シェルからトークンを消す。

   ```sh
   unset DISCORD_BOT_TOKEN
   ```

## 7. サーバへの招待と利用制限

### 7-1. bot をサーバへ招待する

1. 次の URL の `<Application ID>` を置き換えて、ブラウザで開く。スコープは `bot` と `applications.commands` の 2 つ。

   ```
   https://discord.com/oauth2/authorize?client_id=<Application ID>&scope=bot%20applications.commands
   ```

2. 招待先のサーバを選んで認可する。「Public Bot」が無効なので、アプリの所有者だけが追加できる。サーバの「サーバーを管理」権限が必要。
3. この bot は slash command への応答だけをするので、メッセージの読み取りなどの追加の権限は要らない。

### 7-2. コマンドを使える人とチャンネルを制限する

サーバの管理者が行う。

1. Discord のサーバ名のメニューから「サーバー設定」を開く。
2. 「連携サービス」を開き、この bot のアプリを選んで「管理」を押す。
3. コマンドごと、またはアプリ全体で、使えるロール・メンバー・チャンネルを設定する。既定の `@everyone` の許可を外し、許可するロールとチャンネルだけを追加する。

## 開発

自動テストをローカルで実行する。

1. Go を入れる。バージョンは `.tool-versions` で固定している（Go 1.27.4）。asdf を使う場合:

   ```sh
   asdf install
   ```

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
