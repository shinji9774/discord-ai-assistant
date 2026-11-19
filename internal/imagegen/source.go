package imagegen

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // image.Decode に PNG を登録する
	"math"

	"golang.org/x/image/draw"

	"discord-ai-assistant/internal/command"
)

const (
	// image.DecodeConfig が返す形式名。他の形式が登録されても受け付けないために照合する
	formatPNG  = "png"
	formatJPEG = "jpeg"
)

// sourceSizeAcceptable は、取得する前に、Discord が伝えた大きさで判定する。
func sourceSizeAcceptable(cfg SourceConfig, attachment command.Attachment) bool {
	return attachment.Size <= cfg.MaxBytes
}

// sourceImageAcceptable は、取得した後に、形式と寸法を判定する。画素は展開しない。
func sourceImageAcceptable(cfg SourceConfig, content []byte) bool {
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return false
	}
	if format != formatPNG && format != formatJPEG {
		return false
	}
	if config.Width < cfg.MinSide || config.Height < cfg.MinSide {
		return false
	}
	if config.Width*config.Height > cfg.MaxPixels {
		return false
	}
	return aspectWithinLimit(cfg, config.Width, config.Height)
}

func aspectWithinLimit(cfg SourceConfig, width, height int) bool {
	aspect := float64(width) / float64(height)
	return aspect <= cfg.MaxAspect && aspect >= 1/cfg.MaxAspect
}

// fitSource は、総画素数が cfg.MaxModelPixels を超えるとき、縦横比を保って縮小し、JPEG にして返す。
// 超えないときは content をそのまま返す。
func fitSource(cfg SourceConfig, content []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("decode source image config: %w", err)
	}
	if config.Width*config.Height <= cfg.MaxModelPixels {
		return content, nil
	}

	source, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("decode source image: %w", err)
	}
	// 各辺に同じ倍率を掛けて切り捨てるので、丸めで上限を超えない
	scale := math.Sqrt(float64(cfg.MaxModelPixels) / float64(config.Width*config.Height))
	resized := image.NewRGBA(image.Rect(0, 0, int(float64(config.Width)*scale), int(float64(config.Height)*scale)))
	draw.CatmullRom.Scale(resized, resized.Bounds(), source, source.Bounds(), draw.Src, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: cfg.ResizedJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode resized image: %w", err)
	}
	return buf.Bytes(), nil
}
