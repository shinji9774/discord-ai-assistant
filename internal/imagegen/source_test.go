package imagegen

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"testing"

	"discord-ai-assistant/internal/command"
)

const (
	// 総画素数の上限 25,000,000 は 5000×5000 ちょうど
	pixelLimitSide = 5000
	// 縦横比の上限 2.5:1 は 160×64 ちょうど（短辺は下限の 64）
	aspectLimitLongSide  = 160
	aspectLimitShortSide = 64
	pngSignature         = "\x89PNG\r\n\x1a\n"
	pngIHDRLength        = 13
	// 100×100 の GIF のヘッダ。このパッケージは GIF を登録しないので読めない
	gifHeader        = "GIF89a\x64\x00\x64\x00"
	pngBitDepth8     = 8
	pngColorTypeRGBA = 6
	// 登録はするが許可しない架空の形式。マジックは他の入力と衝突しない固有の値にする
	fakeFormatName  = "fakeformat"
	fakeFormatMagic = "FAKEFMT\x00\x01"
	fakeFormatSide  = 100
	// 総画素数の上限 9,437,184 は 3072×3072 ちょうど
	modelLimitSide = 3072
	// スマートフォンの写真 4032×3024 は 4:3 で、上限を超える
	photoWidth             = 4032
	photoHeight            = 3024
	photoAspectNumerator   = 4
	photoAspectDenominator = 3
)

// encodePNG は実際に画素を持つ小さな PNG を作る。
func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngHeaderOnly は署名と IHDR だけの PNG を作る。DecodeConfig は画素を読まないので、
// 5000×5000 のような大きな寸法でもメモリを使わずに判定のテストができる。
func pngHeaderOnly(width, height int) []byte {
	ihdr := make([]byte, pngIHDRLength)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = pngBitDepth8
	ihdr[9] = pngColorTypeRGBA

	var buf bytes.Buffer
	buf.WriteString(pngSignature)
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	buf.Write(chunk)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return buf.Bytes()
}

func TestSourceSizeAcceptable_AtLimit_True(t *testing.T) {
	// 条件: Discord が伝えた大きさがちょうど MaxSourceBytes
	// 内容: 受け付けられる
	attachment := command.Attachment{Size: MaxSourceBytes}

	got := sourceSizeAcceptable(attachment)

	if !got {
		t.Errorf("sourceSizeAcceptable(%d) = false, want true", attachment.Size)
	}
}

func TestSourceSizeAcceptable_OverLimit_False(t *testing.T) {
	// 条件: Discord が伝えた大きさが MaxSourceBytes を 1 バイト超える
	// 内容: 受け付けられない
	attachment := command.Attachment{Size: MaxSourceBytes + 1}

	got := sourceSizeAcceptable(attachment)

	if got {
		t.Errorf("sourceSizeAcceptable(%d) = true, want false", attachment.Size)
	}
}

func TestSourceImageAcceptable_RegisteredButNotAllowedFormat_False(t *testing.T) {
	// 条件: image に登録済みだが PNG・JPEG ではない形式で、寸法の条件はすべて満たす画像
	// 内容: 登録の有無ではなく形式名で判定し、受け付けない（7.3）
	image.RegisterFormat(fakeFormatName, fakeFormatMagic, nil, func(io.Reader) (image.Config, error) {
		return image.Config{Width: fakeFormatSide, Height: fakeFormatSide}, nil
	})
	content := []byte(fakeFormatMagic)

	got := sourceImageAcceptable(content)

	if got {
		t.Errorf("sourceImageAcceptable(%s) = true, want false", fakeFormatName)
	}
}

func TestSourceImageAcceptable_ByFormatAndDimensions_ReturnsExpected(t *testing.T) {
	// 条件: 形式・各辺・総画素数・縦横比が、境界の内側と外側にある画像
	// 内容: 条件をすべて満たすときだけ true になる（7.3）
	cases := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"PNG", encodePNG(t, 100, 100), true},
		{"JPEG", encodeJPEG(t, 100, 100), true},
		{"GIF", []byte(gifHeader), false},
		{"テキスト", []byte("not an image"), false},
		{"各辺が下限未満（63×64）", encodePNG(t, minSourceSide-1, minSourceSide), false},
		{"各辺が下限（64×64）", encodePNG(t, minSourceSide, minSourceSide), true},
		{"総画素数が上限（5000×5000）", pngHeaderOnly(pixelLimitSide, pixelLimitSide), true},
		{"総画素数が上限を 1 辺ぶん超える（5000×5001）", pngHeaderOnly(pixelLimitSide, pixelLimitSide+1), false},
		{"縦横比が上限（2.5:1）", encodePNG(t, aspectLimitLongSide, aspectLimitShortSide), true},
		{"縦横比が上限（1:2.5）", encodePNG(t, aspectLimitShortSide, aspectLimitLongSide), true},
		{"横長で縦横比が上限を超える", encodePNG(t, aspectLimitLongSide+1, aspectLimitShortSide), false},
		{"縦長で縦横比が上限を超える", encodePNG(t, aspectLimitShortSide, aspectLimitLongSide+1), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sourceImageAcceptable(tc.content)

			if got != tc.want {
				t.Errorf("sourceImageAcceptable() = %v, want %v", got, tc.want)
			}
		})
	}
}

// decodeConfigOf は出力の形式名と寸法を読む。
func decodeConfigOf(t *testing.T, content []byte) (format string, width, height int) {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return format, config.Width, config.Height
}

func TestFitSource_OverModelPixels_ResizesToJPEGWithinLimitKeepingAspect(t *testing.T) {
	// 条件: 総画素数が maxModelPixels を超える 4032×3024（4:3）の JPEG
	// 内容: 総画素数が上限以下で、縦横比 4:3 の JPEG になる
	content := encodeJPEG(t, photoWidth, photoHeight)

	got, err := fitSource(content)

	if err != nil {
		t.Fatal(err)
	}
	format, width, height := decodeConfigOf(t, got)
	// 各辺を切り捨てるので、縦横比は短辺の 1 画素ぶんの誤差までずれうる
	aspectError := math.Abs(float64(width)/float64(height) - float64(photoAspectNumerator)/float64(photoAspectDenominator))
	if format != formatJPEG || width*height > maxModelPixels || aspectError > 1/float64(height) {
		t.Errorf("fitSource() = %s %dx%d, want jpeg within %d pixels with aspect %d:%d",
			format, width, height, maxModelPixels, photoAspectNumerator, photoAspectDenominator)
	}
}

func TestFitSource_AtModelPixels_ReturnsContentUnchanged(t *testing.T) {
	// 条件: 総画素数が maxModelPixels ちょうどの 3072×3072 の PNG
	// 内容: 縮小せず、受け取ったバイト列がそのまま返る
	content := encodePNG(t, modelLimitSide, modelLimitSide)

	got, err := fitSource(content)

	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("fitSource() changed the content (len %d -> %d), want unchanged", len(content), len(got))
	}
}
