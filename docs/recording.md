---
title: "録音（/record-*）"
nav_order: 7
---

# /record-start・/record-stop・/record-optout — bot が録音して要約する

bot がボイスチャンネルを録音し、終わったら文字起こしして要約します。

## コマンド

- `/record-start`: 自分がいるボイスチャンネルを録音します（録音中は bot の名前に `[録音中]`）
- `/record-stop`: 録音を止めます（開始した人か参加者）
- `/record-optout`: 自分の音声を録音から除きます

## 録音の終わりと結果

全員の退出か 180 分で終わり、[/summarize-voice](summarize-voice.md) と同じ形で投稿します。

録音した音声は文字起こしのために Amazon Transcribe へ、文字起こしは要約のために文章生成モデルへ送ります。どちらも日本（東京）の AWS です。要約を投稿したら、音声と文字起こしを削除します。詳しくは[プライバシーポリシー](privacy.md)を見てください。

[目次に戻る](index.md)
