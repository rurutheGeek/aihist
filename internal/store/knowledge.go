package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Tag は利用者が作ったタグ。説明は AI がタグを当てはめるときの手がかり。
type Tag struct {
	Name, Description string
	Count             int // 付いている会話の数
}

func (s *Store) Tags() ([]Tag, error) {
	rows, err := s.db.Query(`SELECT t.name, t.description, (SELECT count(*) FROM conv_tags c WHERE c.tag=t.name) FROM tags t ORDER BY t.sort, t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.Name, &t.Description, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetTag はタグを足す（あれば説明を変える）。
func (s *Store) SetTag(name, desc string) error {
	_, err := s.db.Exec(`INSERT INTO tags (name, description, sort) VALUES (?, ?, (SELECT ifnull(max(sort),0)+1 FROM tags))
	  ON CONFLICT(name) DO UPDATE SET description=excluded.description`, name, desc)
	return err
}

// RenameTag はタグの名前を変える（付いている会話もそのまま）。
func (s *Store) RenameTag(old, name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tags SET name=? WHERE name=?`, name, old); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE OR IGNORE conv_tags SET tag=? WHERE tag=?`, name, old); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM conv_tags WHERE tag=?`, old); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteTag はタグを消す（会話から外れる）。
func (s *Store) DeleteTag(name string) error {
	if _, err := s.db.Exec(`DELETE FROM conv_tags WHERE tag=?`, name); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM tags WHERE name=?`, name)
	return err
}

func (s *Store) key(id int64) (t, h, e, stamp string, err error) {
	err = s.db.QueryRow(`SELECT tool, host, external_id, source_stamp FROM conversations WHERE id=?`, id).Scan(&t, &h, &e, &stamp)
	return
}

// SetConvTags は会話 id のタグを names に置き換える（無いタグは無視する）。
func (s *Store) SetConvTags(id int64, names []string) error {
	t, h, e, _, err := s.key(id)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conv_tags WHERE tool=? AND host=? AND external_id=?`, t, h, e); err != nil {
		return err
	}
	for _, n := range names {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO conv_tags (tool, host, external_id, tag) SELECT ?,?,?,name FROM tags WHERE name=?`, t, h, e, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetSummary は AI でまとめた要約と、まとめたモデル・会話の版を残す。
func (s *Store) SetSummary(id int64, summary, model string) error {
	t, h, e, stamp, err := s.key(id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO conv_meta (tool, host, external_id) VALUES (?,?,?)`, t, h, e); err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE conv_meta SET summary=?, ai_model=?, ai_at=?, ai_stamp=? WHERE tool=? AND host=? AND external_id=?`,
		summary, model, time.Now().Unix(), stamp, t, h, e)
	return err
}

// Organized は会話 id を、今の版のまま model でまとめたことがあるか。
func (s *Store) Organized(id int64, model string) bool {
	t, h, e, stamp, err := s.key(id)
	if err != nil {
		return false
	}
	var m, st string
	s.db.QueryRow(`SELECT ai_model, ai_stamp FROM conv_meta WHERE tool=? AND host=? AND external_id=?`, t, h, e).Scan(&m, &st)
	return m == model && st == stamp && st != ""
}

// Stale は会話 id にタグ・要約を付けたあと、続きが足されたか（会話の版が変わったか）。
func (s *Store) Stale(id int64) bool {
	t, h, e, stamp, err := s.key(id)
	if err != nil {
		return false
	}
	var st string
	s.db.QueryRow(`SELECT ai_stamp FROM conv_meta WHERE tool=? AND host=? AND external_id=?`, t, h, e).Scan(&st)
	return st != "" && strings.TrimPrefix(st, reparse) != strings.TrimPrefix(stamp, reparse)
}

// AIInfo は会話 id をまとめたモデルと日時（まとめていなければ空）。
func (s *Store) AIInfo(id int64) (model string, at time.Time) {
	t, h, e, _, err := s.key(id)
	if err != nil {
		return "", time.Time{}
	}
	var a int64
	s.db.QueryRow(`SELECT ai_model, ai_at FROM conv_meta WHERE tool=? AND host=? AND external_id=?`, t, h, e).Scan(&model, &a)
	return model, fromUnix(a)
}

// Knowledge は答えを見つけにくい問題の解決の記事。
type Knowledge struct {
	ID                  int64
	Title, Body, Status string // status: draft（AI が書いたまま。まとめ直すと置き換わる）/ registered（人が直した）
	Tool, Host, ExtID   string // 元の会話（記事には出さない付帯情報）
	ConvTitle, Model    string
	Struggle            string
	Score               int
	Warn                []string // 本文に混じっているかもしれない個人の情報
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const knowledgeCols = `id, title, body, status, tool, host, external_id, conv_title, model, struggle, score, warn, created_at, updated_at`

func scanKnowledge(sc interface{ Scan(...any) error }) (Knowledge, error) {
	var k Knowledge
	var warn string
	var c, u int64
	err := sc.Scan(&k.ID, &k.Title, &k.Body, &k.Status, &k.Tool, &k.Host, &k.ExtID, &k.ConvTitle, &k.Model, &k.Struggle, &k.Score, &warn, &c, &u)
	json.Unmarshal([]byte(warn), &k.Warn)
	k.CreatedAt, k.UpdatedAt = fromUnix(c), fromUnix(u)
	return k, err
}

// AddKnowledge は AI が書いた記事を足す（まとめ直すと置き換わる）。
func (s *Store) AddKnowledge(k Knowledge) (int64, error) {
	w, _ := json.Marshal(k.Warn)
	now := time.Now().Unix()
	if k.Status == "" {
		k.Status = "draft"
	}
	r, err := s.db.Exec(`INSERT INTO knowledge (title, body, status, tool, host, external_id, conv_title, model, struggle, score, warn, created_at, updated_at)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, k.Title, k.Body, k.Status, k.Tool, k.Host, k.ExtID, k.ConvTitle, k.Model, k.Struggle, k.Score, string(w), now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// DropDrafts は会話の、AI が書いたまま人が直していない記事を消す。同じ会話をまとめ直すとき。
func (s *Store) DropDrafts(tool, host, ext string) error {
	_, err := s.db.Exec(`DELETE FROM knowledge WHERE status='draft' AND tool=? AND host=? AND external_id=?`, tool, host, ext)
	return err
}

// KnowledgeList は記事（q を題名か本文に含むもの）を新しい順に。
func (s *Store) KnowledgeList(q string) ([]Knowledge, error) {
	query := `SELECT ` + knowledgeCols + ` FROM knowledge WHERE 1`
	var args []any
	if q = strings.TrimSpace(q); q != "" {
		query += ` AND (title LIKE ? ESCAPE '\' OR body LIKE ? ESCAPE '\')`
		like := "%" + escapeLike(q) + "%"
		args = append(args, like, like)
	}
	rows, err := s.db.Query(query+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Knowledge
	for rows.Next() {
		k, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KnowledgeCount は記事の数。
func (s *Store) KnowledgeCount() (n int) {
	s.db.QueryRow(`SELECT count(*) FROM knowledge`).Scan(&n)
	return
}

func (s *Store) GetKnowledge(id int64) (Knowledge, error) {
	return scanKnowledge(s.db.QueryRow(`SELECT `+knowledgeCols+` FROM knowledge WHERE id=?`, id))
}

// UpdateKnowledge は題名・本文・状態・警告を変える。
func (s *Store) UpdateKnowledge(k Knowledge) error {
	w, _ := json.Marshal(k.Warn)
	_, err := s.db.Exec(`UPDATE knowledge SET title=?, body=?, status=?, warn=?, updated_at=? WHERE id=?`, k.Title, k.Body, k.Status, string(w), time.Now().Unix(), k.ID)
	return err
}

// Unreviewed は AI が書いたまま、まだ見直していない記事。
func (s *Store) Unreviewed() ([]Knowledge, error) {
	rows, err := s.db.Query(`SELECT id, title, body FROM knowledge WHERE status='draft' AND reviewed=0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Knowledge
	for rows.Next() {
		var k Knowledge
		if err := rows.Scan(&k.ID, &k.Title, &k.Body); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Review は見直しの結果を記事に付ける。keep なら見直し済みにし、そうでなければ消す（人が直した記事は消さない）。
func (s *Store) Review(id int64, keep bool) error {
	if keep {
		_, err := s.db.Exec(`UPDATE knowledge SET reviewed=1 WHERE id=?`, id)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM knowledge WHERE id=? AND status='draft'`, id)
	return err
}

func (s *Store) DeleteKnowledge(id int64) error {
	_, err := s.db.Exec(`DELETE FROM knowledge WHERE id=?`, id)
	return err
}

// LookupExt は会話の ID（ツール・マシン・会話 ID から）。無ければ 0。
func (s *Store) LookupExt(tool, host, ext string) int64 {
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM conversations WHERE tool=? AND host=? AND external_id=?`, tool, host, ext).Scan(&id); err == sql.ErrNoRows {
		return 0
	}
	return id
}

// GraphConv はグラフに出す会話（タグかナレッジのあるもの）。
type GraphConv struct {
	ID          int64
	Title, Tool string
	Tags        []string
	Knowledge   []int64 // この会話から書いたナレッジ
}

// Graph はナレッジ・タグ・会話のつながり。タグもナレッジも無い会話は入れない。
func (s *Store) Graph() ([]GraphConv, error) {
	rows, err := s.db.Query(`
	  SELECT c.id, c.title, c.tool, ifnull(m.title, ''), t.tag, NULL FROM conv_tags t
	    JOIN conversations c ON c.tool=t.tool AND c.host=t.host AND c.external_id=t.external_id
	    LEFT JOIN conv_meta m ON m.tool=c.tool AND m.host=c.host AND m.external_id=c.external_id
	  UNION ALL
	  SELECT c.id, c.title, c.tool, ifnull(m.title, ''), NULL, k.id FROM knowledge k
	    JOIN conversations c ON c.tool=k.tool AND c.host=k.host AND c.external_id=k.external_id
	    LEFT JOIN conv_meta m ON m.tool=c.tool AND m.host=c.host AND m.external_id=c.external_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	at := map[int64]int{}
	var out []GraphConv
	for rows.Next() {
		var id int64
		var title, tool, mine string
		var tag sql.NullString
		var kid sql.NullInt64
		if err := rows.Scan(&id, &title, &tool, &mine, &tag, &kid); err != nil {
			return nil, err
		}
		i, ok := at[id]
		if !ok {
			if mine != "" {
				title = mine
			}
			i = len(out)
			at[id] = i
			out = append(out, GraphConv{ID: id, Title: title, Tool: tool})
		}
		if tag.Valid {
			out[i].Tags = append(out[i].Tags, tag.String)
		}
		if kid.Valid {
			out[i].Knowledge = append(out[i].Knowledge, kid.Int64)
		}
	}
	return out, rows.Err()
}
