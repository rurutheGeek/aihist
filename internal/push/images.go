package push

import (
	"bufio"
	"io"
	"os"
	"regexp"

	"github.com/rurutheGeek/aihist/internal/raw"
)

// 画像を送らないときは、埋め込まれた画像（base64）を 1 ピクセルの PNG に置き換えて送る。
// 形は崩さないので、元の AI へ戻しても読める（画像だけが無くなる）。
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

var (
	// Claude Code・claude.ai: {"type":"base64","media_type":"image/png","data":"..."}
	imgBlock = regexp.MustCompile(`"media_type":"image/[A-Za-z0-9.+-]+","data":"[A-Za-z0-9+/=]{100,}"`)
	// Codex・OpenCode: data:image/png;base64,...
	imgURL = regexp.MustCompile(`data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=]{100,}`)
)

func stripImages(line []byte) []byte {
	line = imgBlock.ReplaceAll(line, []byte(`"media_type":"image/png","data":"`+tinyPNG+`"`))
	return imgURL.ReplaceAll(line, []byte("data:image/png;base64,"+tinyPNG))
}

// stripped は f から画像を抜いた写しを一時ファイルに作る（大きな会話をメモリに載せないよう行ごとに）。
func stripped(f io.Reader) (*os.File, int64, error) {
	tmp, err := os.CreateTemp("", "aihist-noimg-*")
	if err != nil {
		return nil, 0, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	w := bufio.NewWriterSize(tmp, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(stripImages(line)); werr != nil {
				drop(tmp)
				return nil, 0, werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			drop(tmp)
			return nil, 0, err
		}
	}
	if err := w.Flush(); err != nil {
		drop(tmp)
		return nil, 0, err
	}
	size, _ := tmp.Seek(0, io.SeekCurrent)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		drop(tmp)
		return nil, 0, err
	}
	return tmp, size, nil
}

// drop は一時ファイルを閉じて消す。
func drop(f *os.File) {
	f.Close()
	os.Remove(f.Name())
}

// same はサーバーの原本が手元のファイルと同じか。画像を抜いて送るときは大きさが違うので、更新時刻だけで比べる。
func (c *Client) same(h raw.Info, size int64, mtime int64) bool {
	return h.MTime == mtime && (c.NoImages || h.Size == size)
}
