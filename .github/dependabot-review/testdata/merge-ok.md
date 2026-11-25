<!-- dependabot-review -->
## Claude レビュー（Dependabot）

### まとめ

- 判定: **マージしてよい**
  - この判定は CI の結果を含みません。CI がすべて成功していることを前提にしています。
- 優先度: 低（テストだけで使う依存の patch 更新）
- 影響範囲: テストのみ

#### 人が確認すべき点

- なし

### 依存ごとの詳細

#### example.com/gamma v1.2.3 → v1.2.4

**changelog**: https://example.com/gamma/releases/tag/v1.2.4

**破壊的変更**

なし

**セキュリティ修正**

なし

**使用箇所**

- internal/voicesummary/summary_test.go:18 `gamma.Equal`

**ライセンス**: 変更なし
