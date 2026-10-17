package push

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/rurutheGeek/aihist/internal/raw"
	"github.com/rurutheGeek/aihist/internal/scrub"
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

// 添付ファイルの種類（設定の画面に並べる順）。
var AttachKinds = []string{"image", "pdf", "doc", "audio", "video", "other"}

// Kind は mime の種類を、設定で選ぶ種類に分ける。
func Kind(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case mime == "application/pdf":
		return "pdf"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "text/"), mime == "application/json", mime == "application/rtf",
		mime == "application/msword", strings.HasPrefix(mime, "application/vnd.openxmlformats"),
		strings.HasPrefix(mime, "application/vnd.ms-"), strings.HasPrefix(mime, "application/vnd.oasis.opendocument"):
		return "doc"
	}
	return "other"
}

// Keeps は残す種類の並び（"*" は全部、"*,-video" は動画以外、"-" は残さない）で、その種類を残すか。
func Keeps(list, mime string) bool {
	if list == "" {
		return true
	}
	return Selected(list, Kind(mime))
}

// KeepsAll は並びが全部の種類を残すか（抜く必要が無いか）。
func KeepsAll(list string) bool {
	for _, k := range AttachKinds {
		if !Selected(list, k) && list != "" {
			return false
		}
	}
	return true
}

func stripImages(line []byte, keep string) []byte {
	line = b64Block.ReplaceAllFunc(line, func(m []byte) []byte {
		mime := string(b64Block.FindSubmatch(m)[1])
		if Keeps(keep, mime) {
			return m
		}
		mt, data := placeholder(mime)
		return []byte(`"media_type":"` + mt + `","data":"` + data + `"`)
	})
	return dataURL.ReplaceAllFunc(line, func(m []byte) []byte {
		mime := string(dataURL.FindSubmatch(m)[1])
		if Keeps(keep, mime) {
			return m
		}
		mt, data := placeholder(mime)
		return []byte("data:" + mt + ";base64," + data)
	})
}

// stripped は f から、keep に無い種類の添付ファイルを抜き、hide の種類の情報を消した写しを一時ファイルに作る
// （大きな会話をメモリに載せないよう行ごとに）。
func stripped(f io.Reader, keep, hide string) (*os.File, int64, error) {
	tmp, err := os.CreateTemp("", "aihist-noimg-*")
	if err != nil {
		return nil, 0, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	w := bufio.NewWriterSize(tmp, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(scrub.Line(stripImages(line, keep), hide)); werr != nil {
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

// rewrites は送る前に中身を書き換えるか（添付ファイルを抜く・情報を消す）。
func (c *Client) rewrites() bool { return !KeepsAll(c.Attach) || scrub.Any(c.Hide) }

// same はサーバーの原本が手元のファイルと同じか。書き換えて送るときは大きさが違うので、更新時刻だけで比べる。
func (c *Client) same(h raw.Info, size int64, mtime int64) bool {
	return h.MTime == mtime && (c.rewrites() || h.Size == size)
}

// Strip は r から keep に無い種類の添付ファイルを抜き、hide の種類の情報を消した写しを返す（アップロードでも使う）。
// 使い終わったら done を呼ぶ。
func Strip(r io.Reader, keep, hide string) (f *os.File, size int64, done func(), err error) {
	f, size, err = stripped(r, keep, hide)
	if err != nil {
		return nil, 0, func() {}, err
	}
	return f, size, func() { drop(f) }, nil
}
