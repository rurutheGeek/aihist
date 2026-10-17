package store

import (
	"encoding/json"
	"time"
)

// 前の版の分類（category）の列は使わない（空のまま）。

// Trait はユーザーについて分かったこと（プロフィールの1項目）。会話から AI が書き足し、ユーザーが直せる。
type Trait struct {
	ID      int64
	Text    string
	Prev    string // 前はこうだった（新しいことで置き換えたとき）
	Count   int    // 裏付けになった会話の数
	Sources [][3]string
	Edited  bool // ユーザーが直した（AI は書き換えない）
	Updated time.Time
}

const profileSchema = `CREATE TABLE IF NOT EXISTS profile (
  id INTEGER PRIMARY KEY,
  category TEXT NOT NULL DEFAULT '',
  text TEXT NOT NULL,
  prev TEXT NOT NULL DEFAULT '',
  count INTEGER NOT NULL DEFAULT 1,
  sources TEXT NOT NULL DEFAULT '[]',
  edited INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
)`

func (s *Store) Profile() ([]Trait, error) {
	rows, err := s.db.Query(`SELECT id, text, prev, count, sources, edited, updated_at FROM profile ORDER BY count DESC, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trait
	for rows.Next() {
		var t Trait
		var src string
		var u int64
		if err := rows.Scan(&t.ID, &t.Text, &t.Prev, &t.Count, &src, &t.Edited, &u); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(src), &t.Sources)
		t.Updated = fromUnix(u)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) trait(id int64) (Trait, error) {
	var t Trait
	var src string
	err := s.db.QueryRow(`SELECT id, text, prev, count, sources, edited FROM profile WHERE id=?`, id).Scan(&t.ID, &t.Text, &t.Prev, &t.Count, &src, &t.Edited)
	json.Unmarshal([]byte(src), &t.Sources)
	return t, err
}

func addSource(t *Trait, src [3]string) bool {
	for _, x := range t.Sources {
		if x == src {
			return false
		}
	}
	t.Sources = append(t.Sources, src)
	t.Count = len(t.Sources)
	return true
}

// AddTrait は AI が見つけた特徴を書き足す。same が既存の特徴なら裏付けを足し、replaces なら新しい文に置き換える
// （ユーザーが直した特徴は文を変えず、裏付けだけ足す）。
func (s *Store) AddTrait(text string, same, replaces int64, src [3]string) error {
	now := time.Now().Unix()
	for _, id := range []int64{same, replaces} {
		if id == 0 {
			continue
		}
		t, err := s.trait(id)
		if err != nil {
			continue
		}
		addSource(&t, src)
		b, _ := json.Marshal(t.Sources)
		if id == replaces && !t.Edited && t.Text != text {
			_, err = s.db.Exec(`UPDATE profile SET prev=text, text=?, count=?, sources=?, updated_at=? WHERE id=?`, text, t.Count, string(b), now, id)
		} else {
			_, err = s.db.Exec(`UPDATE profile SET count=?, sources=?, updated_at=? WHERE id=?`, t.Count, string(b), now, id)
		}
		return err
	}
	b, _ := json.Marshal([][3]string{src})
	_, err := s.db.Exec(`INSERT INTO profile (text, count, sources, updated_at) VALUES (?,1,?,?)`, text, string(b), now)
	return err
}

// EditTrait はユーザーが特徴を直す（空なら消す）。
func (s *Store) EditTrait(id int64, text string) error {
	if text == "" {
		_, err := s.db.Exec(`DELETE FROM profile WHERE id=?`, id)
		return err
	}
	if id == 0 {
		_, err := s.db.Exec(`INSERT INTO profile (text, count, edited, updated_at) VALUES (?,0,1,?)`, text, time.Now().Unix())
		return err
	}
	_, err := s.db.Exec(`UPDATE profile SET text=?, edited=1, updated_at=? WHERE id=?`, text, time.Now().Unix(), id)
	return err
}
