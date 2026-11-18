package translate

// Language は翻訳先に選べる言語。Name は利用者に見せる日本語名、Code は Amazon Translate の言語コード。
type Language struct {
	Name string
	Code string
}

var (
	English = Language{Name: "英語", Code: "en"}

	// languages は choices に並べる順。Discord の choices の上限（25 件）に一致させている。
	languages = []Language{
		{Name: "日本語", Code: "ja"},
		English,
		{Name: "中国語（簡体字）", Code: "zh"},
		{Name: "中国語（繁体字）", Code: "zh-TW"},
		{Name: "韓国語", Code: "ko"},
		{Name: "スペイン語", Code: "es"},
		{Name: "フランス語", Code: "fr"},
		{Name: "ドイツ語", Code: "de"},
		{Name: "イタリア語", Code: "it"},
		{Name: "ポルトガル語", Code: "pt"},
		{Name: "ロシア語", Code: "ru"},
		{Name: "アラビア語", Code: "ar"},
		{Name: "ヒンディー語", Code: "hi"},
		{Name: "インドネシア語", Code: "id"},
		{Name: "タイ語", Code: "th"},
		{Name: "ベトナム語", Code: "vi"},
		{Name: "トルコ語", Code: "tr"},
		{Name: "オランダ語", Code: "nl"},
		{Name: "ポーランド語", Code: "pl"},
		{Name: "ウクライナ語", Code: "uk"},
		{Name: "スウェーデン語", Code: "sv"},
		{Name: "マレー語", Code: "ms"},
		{Name: "タガログ語", Code: "tl"},
		{Name: "ベンガル語", Code: "bn"},
		{Name: "ペルシア語", Code: "fa"},
	}
)

// languageByCode は言語コードに対応する言語を返す。一覧に無ければ false。
func languageByCode(code string) (Language, bool) {
	for _, language := range languages {
		if language.Code == code {
			return language, true
		}
	}
	return Language{}, false
}
