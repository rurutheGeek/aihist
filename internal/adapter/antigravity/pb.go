package antigravity

import (
	"encoding/binary"
	"errors"
)

// msg は .proto 無しで読んだ protobuf の1メッセージ（フィールド番号→値の並び）。
// 値は varint なら uint64、長さ付きなら []byte。
type msg map[uint64][]any

var errBad = errors.New("protobuf として読めない")

func parse(b []byte) (msg, error) {
	m := msg{}
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errBad
		}
		b = b[n:]
		num := key >> 3
		switch key & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, errBad
			}
			m[num] = append(m[num], v)
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return nil, errBad
			}
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return nil, errBad
			}
			m[num] = append(m[num], b[n:n+int(l)])
			b = b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return nil, errBad
			}
			b = b[4:]
		default:
			return nil, errBad
		}
	}
	return m, nil
}

// msg は path をたどった先の最初の入れ子メッセージ（無ければ空）。
func (m msg) msg(path ...uint64) msg {
	cur := m
	for _, f := range path {
		var next msg
		for _, v := range cur[f] {
			if b, ok := v.([]byte); ok {
				if p, err := parse(b); err == nil {
					next = p
					break
				}
			}
		}
		if next == nil {
			return msg{}
		}
		cur = next
	}
	return cur
}

func (m msg) str(path ...uint64) string {
	last := path[len(path)-1]
	for _, v := range m.msg(path[:len(path)-1]...)[last] {
		if b, ok := v.([]byte); ok {
			return string(b)
		}
	}
	return ""
}

func (m msg) int(path ...uint64) uint64 {
	last := path[len(path)-1]
	for _, v := range m.msg(path[:len(path)-1]...)[last] {
		if u, ok := v.(uint64); ok {
			return u
		}
	}
	return 0
}

// all は path の最後のフィールドにある入れ子メッセージをすべて返す。
func (m msg) all(path ...uint64) []msg {
	last := path[len(path)-1]
	var out []msg
	for _, v := range m.msg(path[:len(path)-1]...)[last] {
		if b, ok := v.([]byte); ok {
			if p, err := parse(b); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}
