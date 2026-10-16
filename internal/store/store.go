// Package store は SQLite への保存と全文検索。
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
  id INTEGER PRIMARY KEY,
  tool TEXT NOT NULL,
  host TEXT NOT NULL,
  external_id TEXT NOT NULL,
  parent_external_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  cwd TEXT NOT NULL DEFAULT '',
  git_branch TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  source_path TEXT NOT NULL DEFAULT '',
  source_stamp TEXT NOT NULL DEFAULT '',
  message_count INTEGER NOT NULL DEFAULT 0,
  UNIQUE (tool, host, external_id)
);
CREATE INDEX IF NOT EXISTS conversations_updated ON conversations (updated_at DESC);

CREATE TABLE IF NOT EXISTS messages (
  id INTEGER PRIMARY KEY,
  conversation_id INTEGER NOT NULL REFERENCES conversations ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  external_id TEXT NOT NULL DEFAULT '',
  parent_external_id TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '',
  sidechain INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS messages_conv ON messages (conversation_id, seq);

CREATE TABLE IF NOT EXISTS parts (
  id INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES messages ON DELETE CASCADE,
  conversation_id INTEGER NOT NULL,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL,
  text TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL DEFAULT '',
  tool_call_id TEXT NOT NULL DEFAULT '',
  is_error INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS parts_msg ON parts (message_id, seq);
CREATE INDEX IF NOT EXISTS parts_conv ON parts (conversation_id);

CREATE VIRTUAL TABLE IF NOT EXISTS parts_fts USING fts5(
  text, content='parts', content_rowid='id', tokenize='trigram'
);
CREATE TRIGGER IF NOT EXISTS parts_ai AFTER INSERT ON parts BEGIN
  INSERT INTO parts_fts(rowid, text) VALUES (new.id, new.text);
END;
CREATE TRIGGER IF NOT EXISTS parts_ad AFTER DELETE ON parts BEGIN
  INSERT INTO parts_fts(parts_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;
`

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate は前の版で作った DB に足りない列を足す。
func migrate(db *sql.DB) error {
	var n int
	db.QueryRow(`SELECT count(*) FROM pragma_table_info('conversations') WHERE name='root_id'`).Scan(&n)
	if n > 0 {
		return nil
	}
	// root_id は最初のメッセージの ID。分岐した会話（履歴を写して別の会話になったもの）は同じ値を持つ。
	_, err := db.Exec(`
	  ALTER TABLE conversations ADD COLUMN root_id TEXT NOT NULL DEFAULT '';
	  UPDATE conversations SET root_id = ifnull((SELECT external_id FROM messages WHERE conversation_id = conversations.id ORDER BY seq LIMIT 1), '');
	  CREATE INDEX IF NOT EXISTS conversations_root ON conversations (tool, host, root_id);
	  CREATE INDEX IF NOT EXISTS conversations_parent ON conversations (tool, host, parent_external_id);`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Stamp は前回取り込んだときの元データの印（サイズ・更新時刻など）。変わっていなければ取り込みを省く。
func (s *Store) Stamp(tool, host, externalID string) string {
	var st string
	s.db.QueryRow(`SELECT source_stamp FROM conversations WHERE tool=? AND host=? AND external_id=?`, tool, host, externalID).Scan(&st)
	return st
}

// Put は会話を丸ごと置き換える。同じ元データを何度流しても結果は同じ。
func (s *Store) Put(c *model.Conversation, stamp string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conversations WHERE tool=? AND host=? AND external_id=?`, c.Tool, c.Host, c.ExternalID); err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO conversations
	  (tool, host, external_id, parent_external_id, title, cwd, git_branch, model, started_at, updated_at, source_path, source_stamp, message_count, root_id)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Tool, c.Host, c.ExternalID, c.ParentID, c.Title, c.Cwd, c.GitBranch, c.Model,
		unix(c.StartedAt), unix(c.UpdatedAt), c.SourcePath, stamp, len(c.Messages), rootID(c))
	if err != nil {
		return err
	}
	convID, _ := res.LastInsertId()
	msgStmt, err := tx.Prepare(`INSERT INTO messages (conversation_id, seq, external_id, parent_external_id, role, created_at, model, sidechain) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer msgStmt.Close()
	partStmt, err := tx.Prepare(`INSERT INTO parts (message_id, conversation_id, seq, kind, text, tool_name, tool_call_id, is_error) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer partStmt.Close()
	for i, m := range c.Messages {
		r, err := msgStmt.Exec(convID, i, m.ExternalID, m.ParentID, m.Role, unix(m.CreatedAt), m.Model, m.Sidechain)
		if err != nil {
			return err
		}
		msgID, _ := r.LastInsertId()
		for j, p := range m.Parts {
			if _, err := partStmt.Exec(msgID, convID, j, string(p.Kind), p.Text, p.ToolName, p.ToolCallID, p.IsError); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func rootID(c *model.Conversation) string {
	if len(c.Messages) == 0 {
		return ""
	}
	return c.Messages[0].ExternalID
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

type ConvRow struct {
	ID           int64
	Tool, Host   string
	ExternalID   string
	ParentID     string
	Title        string
	Cwd          string
	GitBranch    string
	Model        string
	StartedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	Snippet      string
	RootID       string
	Branches     int // 一覧でまとめた分岐の数（自分を含む）
}

type Filter struct {
	Query string
	Tool  string
	Host  string
	Limit int
	All   bool // サブエージェントも出し、分岐もまとめない
}

// List は会話一覧。Query があれば本文の全文検索で絞る。
// trigram は3文字未満を引けないので、短い語は LIKE で探す。
func (s *Store) List(f Filter) ([]ConvRow, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	var (
		q    string
		args []any
	)
	cols := `c.id, c.tool, c.host, c.external_id, c.parent_external_id, c.title, c.cwd, c.git_branch, c.model, c.started_at, c.updated_at, c.message_count, c.root_id`
	query := strings.TrimSpace(f.Query)
	switch {
	case query == "":
		q = `SELECT ` + cols + `, '' FROM conversations c WHERE 1=1`
	case len([]rune(query)) >= 3:
		return s.search(query, f)
	default:
		q = `SELECT ` + cols + `, '' FROM conversations c WHERE c.id IN (
		       SELECT conversation_id FROM parts WHERE text LIKE ? ESCAPE '\') OR c.title LIKE ? ESCAPE '\'`
		like := "%" + escapeLike(query) + "%"
		args = append(args, like, like)
		q = `SELECT * FROM (` + q + `) c WHERE 1=1`
	}
	if !f.All {
		// サブエージェントは親の会話の中から開く。親が無いものだけ一覧に出す。
		q += ` AND NOT EXISTS (SELECT 1 FROM conversations p WHERE p.tool=c.tool AND p.host=c.host AND p.external_id=c.parent_external_id AND c.parent_external_id<>'')`
	}
	if f.Tool != "" {
		q += ` AND c.tool = ?`
		args = append(args, f.Tool)
	}
	if f.Host != "" {
		q += ` AND c.host = ?`
		args = append(args, f.Host)
	}
	q += ` ORDER BY c.updated_at DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConvRow
	for rows.Next() {
		var r ConvRow
		var st, up int64
		if err := rows.Scan(&r.ID, &r.Tool, &r.Host, &r.ExternalID, &r.ParentID, &r.Title, &r.Cwd, &r.GitBranch, &r.Model, &st, &up, &r.MessageCount, &r.RootID, &r.Snippet); err != nil {
			return nil, err
		}
		r.StartedAt, r.UpdatedAt = fromUnix(st), fromUnix(up)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if f.All {
		return out, nil
	}
	return collapse(out), nil
}

// collapse は分岐（最初のメッセージが同じ会話）を、いちばん新しいもの1つにまとめる。
// 並びは更新の新しい順なので、最初に出たものを残す。
func collapse(rows []ConvRow) []ConvRow {
	first := map[string]int{}
	var out []ConvRow
	for _, r := range rows {
		key := r.Tool + "\x00" + r.Host + "\x00" + r.RootID
		if i, ok := first[key]; ok && r.RootID != "" {
			out[i].Branches++
			if out[i].Snippet == "" {
				out[i].Snippet = r.Snippet
			}
			continue
		}
		r.Branches = 1
		first[key] = len(out)
		out = append(out, r)
	}
	return out
}

// search は全文検索。snippet() は集計の中で使えないので、会話ごとの最初の一致を Go 側で拾う。
func (s *Store) search(query string, f Filter) ([]ConvRow, error) {
	rows, err := s.db.Query(`SELECT p.conversation_id, snippet(parts_fts, 0, char(2), char(3), '…', 24)
	  FROM parts_fts JOIN parts p ON p.id = parts_fts.rowid
	  WHERE parts_fts MATCH ? ORDER BY p.conversation_id DESC`, ftsPhrase(query))
	if err != nil {
		return nil, err
	}
	snip := map[int64]string{}
	for rows.Next() {
		var cid int64
		var sn string
		if err := rows.Scan(&cid, &sn); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := snip[cid]; !ok {
			snip[cid] = sn
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// サブエージェントでの一致は親の会話の一致として出す
	if !f.All {
		pr, err := s.db.Query(`SELECT c.id, p.id FROM conversations c JOIN conversations p
		  ON p.tool=c.tool AND p.host=c.host AND p.external_id=c.parent_external_id WHERE c.parent_external_id<>''`)
		if err != nil {
			return nil, err
		}
		for pr.Next() {
			var child, parent int64
			if err := pr.Scan(&child, &parent); err != nil {
				pr.Close()
				return nil, err
			}
			if sn, ok := snip[child]; ok {
				if _, has := snip[parent]; !has {
					snip[parent] = sn
				}
			}
		}
		pr.Close()
	}
	all, err := s.List(Filter{Tool: f.Tool, Host: f.Host, Limit: 1 << 30, All: true})
	if err != nil {
		return nil, err
	}
	var out []ConvRow
	for _, r := range all {
		sn, ok := snip[r.ID]
		if !ok && !strings.Contains(strings.ToLower(r.Title), strings.ToLower(query)) {
			continue
		}
		r.Snippet = sn
		out = append(out, r)
	}
	if !f.All {
		out = collapse(hideChildren(out, all))
	}
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// hideChildren は親が取り込まれているサブエージェントを除く。
func hideChildren(rows, all []ConvRow) []ConvRow {
	have := map[string]bool{}
	for _, r := range all {
		have[r.Tool+"\x00"+r.Host+"\x00"+r.ExternalID] = true
	}
	var out []ConvRow
	for _, r := range rows {
		if r.ParentID != "" && have[r.Tool+"\x00"+r.Host+"\x00"+r.ParentID] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Related は同じ会話の分岐（自分を含む、古い順）と、サブエージェント（古い順）。
func (s *Store) Related(c *ConvRow) (branches, children []ConvRow, err error) {
	q := `SELECT id, tool, host, external_id, parent_external_id, title, cwd, git_branch, model, started_at, updated_at, message_count, root_id, '' FROM conversations `
	if c.RootID != "" {
		if branches, err = s.query(q+`WHERE tool=? AND host=? AND root_id=? ORDER BY updated_at`, c.Tool, c.Host, c.RootID); err != nil {
			return nil, nil, err
		}
	}
	children, err = s.query(q+`WHERE tool=? AND host=? AND parent_external_id=? ORDER BY started_at`, c.Tool, c.Host, c.ExternalID)
	return branches, children, err
}

func (s *Store) query(q string, args ...any) ([]ConvRow, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConvRow
	for rows.Next() {
		var r ConvRow
		var st, up int64
		if err := rows.Scan(&r.ID, &r.Tool, &r.Host, &r.ExternalID, &r.ParentID, &r.Title, &r.Cwd, &r.GitBranch, &r.Model, &st, &up, &r.MessageCount, &r.RootID, &r.Snippet); err != nil {
			return nil, err
		}
		r.StartedAt, r.UpdatedAt = fromUnix(st), fromUnix(up)
		out = append(out, r)
	}
	return out, rows.Err()
}

func ftsPhrase(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func fromUnix(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

// Facet は絞り込みの候補と件数。
type Facet struct {
	Name  string
	Count int
}

// Facets は列 col（tool か host）ごとの会話数。
func (s *Store) Facets(col string) ([]Facet, error) {
	if col != "tool" && col != "host" {
		return nil, fmt.Errorf("facet: %s", col)
	}
	// 一覧と同じ数え方（サブエージェントは除き、分岐は1つと数える）
	rows, err := s.db.Query(`SELECT ` + col + `, count(DISTINCT CASE WHEN c.root_id<>'' THEN c.tool||char(0)||c.host||char(0)||c.root_id ELSE c.id END)
	  FROM conversations c
	  WHERE NOT EXISTS (SELECT 1 FROM conversations p WHERE p.tool=c.tool AND p.host=c.host AND p.external_id=c.parent_external_id AND c.parent_external_id<>'')
	  GROUP BY 1 ORDER BY 2 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Facet
	for rows.Next() {
		var f Facet
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Tools() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT tool FROM conversations ORDER BY tool`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get は会話1件を全メッセージ付きで返す。
func (s *Store) Get(id int64) (*ConvRow, []model.Message, error) {
	var r ConvRow
	var st, up int64
	err := s.db.QueryRow(`SELECT id, tool, host, external_id, parent_external_id, title, cwd, git_branch, model, started_at, updated_at, message_count, root_id FROM conversations WHERE id=?`, id).
		Scan(&r.ID, &r.Tool, &r.Host, &r.ExternalID, &r.ParentID, &r.Title, &r.Cwd, &r.GitBranch, &r.Model, &st, &up, &r.MessageCount, &r.RootID)
	if err != nil {
		return nil, nil, err
	}
	r.StartedAt, r.UpdatedAt = fromUnix(st), fromUnix(up)
	rows, err := s.db.Query(`SELECT m.id, m.external_id, m.parent_external_id, m.role, m.created_at, m.model, m.sidechain,
	    p.kind, p.text, p.tool_name, p.tool_call_id, p.is_error
	  FROM messages m LEFT JOIN parts p ON p.message_id = m.id
	  WHERE m.conversation_id=? ORDER BY m.seq, p.seq`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var msgs []model.Message
	var last int64 = -1
	for rows.Next() {
		var mid, at int64
		var m model.Message
		var kind, text, tool, call sql.NullString
		var isErr sql.NullBool
		if err := rows.Scan(&mid, &m.ExternalID, &m.ParentID, &m.Role, &at, &m.Model, &m.Sidechain, &kind, &text, &tool, &call, &isErr); err != nil {
			return nil, nil, err
		}
		if mid != last {
			m.CreatedAt = fromUnix(at)
			msgs = append(msgs, m)
			last = mid
		}
		if kind.Valid {
			cur := &msgs[len(msgs)-1]
			cur.Parts = append(cur.Parts, model.Part{Kind: model.PartKind(kind.String), Text: text.String, ToolName: tool.String, ToolCallID: call.String, IsError: isErr.Bool})
		}
	}
	return &r, msgs, rows.Err()
}

// FindByExternal は ExternalID の前方一致で会話を探す（CLI 用）。
func (s *Store) FindByExternal(prefix string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM conversations WHERE external_id LIKE ? ESCAPE '\' ORDER BY updated_at DESC LIMIT 1`, escapeLike(prefix)+"%").Scan(&id)
	return id, err
}

// Lookup はツール・ホスト・会話 ID で会話を探す。
func (s *Store) Lookup(tool, host, externalID string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM conversations WHERE tool=? AND host=? AND external_id=?`, tool, host, externalID).Scan(&id)
	return id, err
}
