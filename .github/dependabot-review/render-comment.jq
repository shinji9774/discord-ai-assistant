# Dependabot の PR に付ける Claude のレビューコメントを、構造化出力（review-result.schema.json）から描画する。
# モデルの出力の揺れに関係なく、次の不変条件をここで守る。
# - changelog もソース差分も読めなかった依存があれば、判定を「マージしてよい」にしない（要件 5.12）
# - changelog もソース差分も読めなかった依存に、破壊的変更がないとは書かない（要件 5.4）
# - 判定が「マージしてよい」以外なら、人が確認すべき点が 1 件以上ある（要件 5.15）

def marker: "<!-- dependabot-review -->";
def verdict_merge_ok: "マージしてよい";
def verdict_needs_check: "確認が必要";
def ci_disclaimer: "この判定は、影響を受けるパッケージのユニットテストの結果を含みます。CI 全体（integration テストを含む）の結果は含みません。";
def changelog_missing_text: "changelog・リリースノートを取得できませんでした。破壊的変更の有無は分かりません。";
def changelog_missing_check: {
  point: "changelog・リリースノートを取得できなかった依存がある",
  reason: "破壊的変更の有無を確かめられていない",
  location: "依存ごとの詳細の「破壊的変更」"
};

def changelog_unverified: .changelog_found == false and .source_diff_reviewed == false;

def has_missing_changelog: any(.dependencies[]; changelog_unverified);

def downgrade_when_changelog_missing:
  if has_missing_changelog and .verdict == verdict_merge_ok
  then .verdict = verdict_needs_check | .checks += [changelog_missing_check]
  else .
  end;

def assert_checks_present:
  if .verdict != verdict_merge_ok and (.checks | length) == 0
  then error("verdict '\(.verdict)' requires at least one check")
  else .
  end;

def changelog_text:
  if .changelog_found then .changelog_url
  elif .source_diff_reviewed then "取得できませんでした（ソース差分で確認）"
  else "取得できませんでした"
  end;

def summary_lines:
  [
    "### まとめ",
    "",
    "- 判定: **\(.verdict)**",
    "  - \(ci_disclaimer)",
    "- 優先度: \(.priority)（\(.priority_reason)）",
    "- 影響範囲: \(.impacts | join("、"))",
    "",
    "#### 人が確認すべき点",
    ""
  ]
  + (if (.checks | length) == 0
     then ["- なし"]
     else [.checks[] | "- **\(.point)**: \(.reason)（\(.location)）"]
     end)
  + [
    "",
    "#### 依存グラフの変化",
    "",
    .graph_changes
  ];

def dependency_lines:
  [
    "#### \(.name) \(.from) → \(.to)",
    "",
    "**changelog**: \(changelog_text)",
    "",
    "**破壊的変更**",
    "",
    (if changelog_unverified then changelog_missing_text else .breaking_changes end),
    "",
    "**セキュリティ修正**",
    "",
    .security_fixes,
    "",
    "**使用箇所**",
    "",
    .usages,
    "",
    "**テストでの確認**",
    "",
    .test_coverage,
    "",
    "**ライセンス**: \(.license_change)"
  ];

downgrade_when_changelog_missing
| assert_checks_present
| [marker, "## Claude レビュー（Dependabot）", ""]
  + summary_lines
  + ["", "### 依存ごとの詳細"]
  + ([.dependencies[] | [""] + dependency_lines] | add)
| join("\n")
