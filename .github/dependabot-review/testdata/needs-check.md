<!-- dependabot-review -->
## Claude レビュー（Dependabot）

### まとめ

- 判定: **確認が必要**
  - この判定は、影響を受けるパッケージのユニットテストの結果を含みます。CI 全体（integration テストを含む）の結果は含みません。
- 優先度: 中（本番のバイナリに効く major 更新を含む）
- 影響範囲: 本番のバイナリ、テストのみ

#### 人が確認すべき点

- **Client.Do の戻り値が変わった**: エラーの型が変わり、errors.As の判定が外れるおそれがある（internal/discordapi/client.go:42）

#### 依存グラフの変化

なし

### 依存ごとの詳細

#### example.com/alpha v1.4.0 → v2.0.0

**changelog**: https://example.com/alpha/releases/tag/v2.0.0

**破壊的変更**

- Client.Do が *APIError ではなく error を返す

**セキュリティ修正**

なし

**使用箇所**

- internal/discordapi/client.go:42 `alpha.Client.Do`

**テストでの確認**

- internal/discordapi/client_test.go:55 `TestClient_Do_APIError`: Do が *APIError を返すことを確かめている（結果: 失敗）

**ライセンス**: 変更なし

#### example.com/beta v0.3.1 → v0.3.2

**changelog**: https://example.com/beta/releases/tag/v0.3.2

**破壊的変更**

なし

**セキュリティ修正**

- GHSA-2c3m-7q9v-5f8h: 長い入力でパニックする

**使用箇所**

なし

**テストでの確認**

なし

**ライセンス**: MIT から Apache-2.0 に変更
