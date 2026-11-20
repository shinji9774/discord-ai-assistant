package translate

// Config は /translate の調整値。
type Config struct {
	MaxTextLength    int `yaml:"max_text_length" validate:"min=1"`
	MaxFileSizeBytes int `yaml:"max_file_size_bytes" validate:"min=1"`
}
