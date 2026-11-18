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
	MaxSourceBytes  = 10_000_000
	minSourceSide   = 64
	maxSourcePixels = 25_000_000 // 縮小のために展開してよい総画素数
	maxModelPixels  = 9_437_184  // Control Sketch の入力条件
	maxSourceAspect = 2.5

	// image.DecodeConfig が返す形式名。他の形式が登録されても受け付けないために照合する
	formatPNG  = "png"
	formatJPEG = "jpeg"

	resizedJPEGQuality = 90
)

// sourceSizeAcceptable は、取得する前に、Discord が伝えた大きさで判定する。
func sourceSizeAcceptable(attachment command.Attachment) bool {
	return attachment.Size <= MaxSourceBytes
}

// sourceImageAcceptable は、取得した後に、形式と寸法を判定する。画素は展開しない。
func sourceImageAcceptable(content []byte) bool {
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return false
	}
	if format != formatPNG && format != formatJPEG {
		return false
	}
	if config.Width < minSourceSide || config.Height < minSourceSide {
		return false
	}
	if config.Width*config.Height > maxSourcePixels {
		return false
	}
	return aspectWithinLimit(config.Width, config.Height)
}

func aspectWithinLimit(width, height int) bool {
	aspect := float64(width) / float64(height)
	return aspect <= maxSourceAspect && aspect >= 1/maxSourceAspect
}

// fitSource は、総画素数が maxModelPixels を超えるとき、縦横比を保って縮小し、JPEG にして返す。
// 超えないときは content をそのまま返す。
func fitSource(content []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("decode source image config: %w", err)
	}
	if config.Width*config.Height <= maxModelPixels {
		return content, nil
	}

	source, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("decode source image: %w", err)
	}
	// 各辺に同じ倍率を掛けて切り捨てるので、丸めで上限を超えない
	scale := math.Sqrt(float64(maxModelPixels) / float64(config.Width*config.Height))
	resized := image.NewRGBA(image.Rect(0, 0, int(float64(config.Width)*scale), int(float64(config.Height)*scale)))
	draw.CatmullRom.Scale(resized, resized.Bounds(), source, source.Bounds(), draw.Src, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: resizedJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode resized image: %w", err)
	}
	return buf.Bytes(), nil
}
