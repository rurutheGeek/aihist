package push

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/rurutheGeek/aihist/internal/raw"
)

// 添付ファイルを送らないときは、会話に埋め込まれたファイル（base64 の画像・PDF・音声など）を
// 同じ種類のごく小さな中身に置き換えて送る。形は崩さないので、元の AI へ戻しても読める（中身だけが無くなる）。
const (
	tinyPNG  = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	tinyPDF  = "JVBERi0xLjQKMSAwIG9iajw8L1R5cGUvQ2F0YWxvZy9QYWdlcyAyIDAgUj4+ZW5kb2JqIDIgMCBvYmo8PC9UeXBlL1BhZ2VzL0tpZHNbMyAwIFJdL0NvdW50IDE+PmVuZG9iaiAzIDAgb2JqPDwvVHlwZS9QYWdlL1BhcmVudCAyIDAgUi9NZWRpYUJveFswIDAgMyAzXT4+ZW5kb2JqCnRyYWlsZXI8PC9Sb290IDEgMCBSPj4KJSVFT0Y="
	tinyText = "W2FpaGlzdDog5re75LuY44OV44Kh44Kk44Or44Gv6YCB44Gj44Gm44GE44Gq44GEXQ==" // [aihist: 添付ファイルは送っていない]
)

var (
	// Claude Code・claude.ai: {"type":"base64","media_type":"image/png","data":"..."}（document の PDF も同じ形）
	b64Block = regexp.MustCompile(`"media_type":"([A-Za-z0-9.+/-]+)","data":"[A-Za-z0-9+/=]{100,}"`)
	// Codex・OpenCode: data:image/png;base64,...
	dataURL = regexp.MustCompile(`data:([A-Za-z0-9.+/-]+);base64,[A-Za-z0-9+/=]{100,}`)
)

// placeholder は種類 mime の代わりに入れる種類と中身。
func placeholder(mime string) (string, string) {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image/png", tinyPNG
	case mime == "application/pdf":
		return mime, tinyPDF
	}
	return "text/plain", tinyText
}

func stripImages(line []byte) []byte {
	line = b64Block.ReplaceAllFunc(line, func(m []byte) []byte {
		mt, data := placeholder(string(b64Block.FindSubmatch(m)[1]))
		return []byte(`"media_type":"` + mt + `","data":"` + data + `"`)
	})
	return dataURL.ReplaceAllFunc(line, func(m []byte) []byte {
		mt, data := placeholder(string(dataURL.FindSubmatch(m)[1]))
		return []byte("data:" + mt + ";base64," + data)
	})
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
