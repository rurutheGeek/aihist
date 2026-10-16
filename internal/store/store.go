// Package store は SQLite への保存と全文検索。
package store

import (
	"database/sql"
	"fmt"
	"slices"
	"sort"
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
	if err := migrateRoot(db); err != nil {
		return err
	}
	// v0.4.0 は Antigravity にも root_id を入れていた（番号が会話ごとに 0 から始まり、全部が1つにまとまる）
	if _, err := db.Exec(`UPDATE conversations SET root_id='' WHERE tool='antigravity' AND root_id<>''`); err != nil {
		return err
	}
	if err := migrateContinues(db); err != nil {
		return err
	}
	return migrateModels(db)
}

// migrateModels は models（会話の中で使ったモデル、改行区切り）を足し、メッセージから埋める。
func migrateModels(db *sql.DB) error {
	if hasColumn(db, "models") {
		return nil
	}
	_, err := db.Exec(`
	  ALTER TABLE conversations ADD COLUMN models TEXT NOT NULL DEFAULT '';
	  UPDATE conversations SET models = ifnull((SELECT group_concat(model, char(10)) FROM
	    (SELECT DISTINCT model FROM messages WHERE conversation_id = conversations.id AND model NOT IN ('', '<synthetic>'))), '');
	  UPDATE conversations SET models = model WHERE models = '' AND model NOT IN ('', '<synthetic>');`)
	return err
}

func hasColumn(db *sql.DB, col string) bool {
	var n int
	db.QueryRow(`SELECT count(*) FROM pragma_table_info('conversations') WHERE name=?`, col).Scan(&n)
	return n > 0
}

// migrateContinues は continues_from を足し、最初のメッセージの本文から埋める。
func migrateContinues(db *sql.DB) error {
	if hasColumn(db, "continues_from") {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN continues_from TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT c.id, ifnull((SELECT p.text FROM messages m JOIN parts p ON p.message_id=m.id
	    WHERE m.conversation_id=c.id ORDER BY m.seq, p.seq LIMIT 1), '') FROM conversations c`)
	if err != nil {
		return err
	}
	set := map[int64]string{}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			rows.Close()
			return err
		}
		if v := model.ContinuesFrom(text); v != "" {
			set[id] = v
		}
	}
	rows.Close()
	for id, v := range set {
		if _, err := db.Exec(`UPDATE conversations SET continues_from=? WHERE id=?`, v, id); err != nil {
			return err
		}
	}
	return nil
}

func migrateRoot(db *sql.DB) error {
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
	  (tool, host, external_id, parent_external_id, title, cwd, git_branch, model, started_at, updated_at, source_path, source_stamp, message_count, root_id, continues_from, models)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Tool, c.Host, c.ExternalID, c.ParentID, c.Title, c.Cwd, c.GitBranch, c.Model,
		unix(c.StartedAt), unix(c.UpdatedAt), c.SourcePath, stamp, len(c.Messages), rootID(c), continuesFrom(c), strings.Join(models(c), "\n"))
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

// models は会話の中で使ったモデル（出てきた順）。
func models(c *model.Conversation) []string {
	var out []string
	seen := map[string]bool{}
	add := func(m string) {
		if m != "" && m != "<synthetic>" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range c.Messages {
		add(m.Model)
	}
	if len(out) == 0 {
		add(c.Model)
	}
	return out
}

func continuesFrom(c *model.Conversation) string {
	if len(c.Messages) == 0 || len(c.Messages[0].Parts) == 0 {
		return ""
	}
	return model.ContinuesFrom(c.Messages[0].Parts[0].Text)
}

// rootID は分岐の判定に使う最初のメッセージの ID。会話をまたいで一意な ID を振るツールだけ。
// Antigravity はステップの番号（0 から）なので使えない。
func rootID(c *model.Conversation) string {
	if len(c.Messages) == 0 || !uniqueMessageIDs(c.Tool) {
		return ""
	}
	return c.Messages[0].ExternalID
}

func uniqueMessageIDs(tool string) bool { return tool != "antigravity" }

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

type ConvRow struct {
	ID            int64
	Tool, Host    string
	ExternalID    string
	ParentID      string
	Title         string
	Cwd           string
	GitBranch     string
	Model         string
	Models        []string // 会話の中で使ったモデル
	StartedAt     time.Time
	UpdatedAt     time.Time
	MessageCount  int
	Snippet       string
	RootID        string
	ContinuesFrom string // 続きのとき、前の会話の ID
	Branches      int    // 一覧でまとめた数（自分を含む）
	orphan        bool   // 親の会話が取り込まれていないサブエージェント
}

type Filter struct {
	Query    string
	Tools    []string
	Hosts    []string
	Models   []string
	Projects []string // 作業ディレクトリ
	Since    time.Time
	Limit    int
	All      bool // まとめずに全部出す
}

// Facet は絞り込みの候補と件数。
type Facet struct {
	Name  string
	Count int
}

// 絞り込みの軸。
const (
	DimTool    = "tool"
	DimModel   = "model"
	DimHost    = "host"
	DimProject = "project"
)

var Dims = []string{DimTool, DimModel, DimHost, DimProject}

func keys(r ConvRow, dim string) []string {
	switch dim {
	case DimTool:
		return []string{r.Tool}
	case DimHost:
		return []string{r.Host}
	case DimModel:
		return r.Models
	case DimProject:
		if r.Cwd == "" {
			return nil
		}
		return []string{r.Cwd}
	}
	return nil
}

func (f Filter) Values(dim string) []string {
	switch dim {
	case DimTool:
		return f.Tools
	case DimHost:
		return f.Hosts
	case DimModel:
		return f.Models
	case DimProject:
		return f.Projects
	}
	return nil
}

// match は r が skip 以外の条件をすべて満たすか。同じ軸の中はどれか1つ、軸どうしはすべて。
func (f Filter) match(r ConvRow, skip string) bool {
	if !f.Since.IsZero() && r.UpdatedAt.Before(f.Since) {
		return false
	}
	for _, d := range Dims {
		want := f.Values(d)
		if d == skip || len(want) == 0 {
			continue
		}
		ok := false
		for _, k := range keys(r, d) {
			if slices.Contains(want, k) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// List は会話一覧。同じ会話（分岐・続き・別のマシンの写し・サブエージェント）は1行にまとめ、
// 一番新しいものを出す。Query があれば本文の全文検索で絞る。
// trigram は3文字未満を引けないので、短い語は LIKE で探す。
func (s *Store) List(f Filter) ([]ConvRow, error) {
	out, _, err := s.Browse(f)
	return out, err
}

// Browse は List と、軸ごとの候補と件数。件数はその軸以外の条件で絞った中で、まとめた会話を1つと数える。
func (s *Store) Browse(f Filter) ([]ConvRow, map[string][]Facet, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	all, err := s.rowsWhere("", "")
	if err != nil {
		return nil, nil, err
	}
	var hits map[int64]string
	if q := strings.TrimSpace(f.Query); q != "" {
		if hits, err = s.hits(q); err != nil {
			return nil, nil, err
		}
		for _, r := range all {
			if _, ok := hits[r.ID]; !ok && strings.Contains(strings.ToLower(r.Title), strings.ToLower(q)) {
				hits[r.ID] = ""
			}
		}
	}
	view := func(skip string) ([]ConvRow, []group) {
		var rows []ConvRow
		for _, r := range all {
			if f.match(r, skip) {
				rows = append(rows, r)
			}
		}
		var gs []group
		for _, g := range groups(rows) {
			if hits != nil && !slices.ContainsFunc(g.members, func(i int) bool { _, ok := hits[rows[i].ID]; return ok }) {
				continue
			}
			gs = append(gs, g)
		}
		return rows, gs
	}
	facets := map[string][]Facet{}
	for _, d := range Dims {
		rows, gs := view(d)
		n := map[string]int{}
		for _, g := range gs {
			seen := map[string]bool{}
			for _, i := range g.members {
				for _, k := range keys(rows[i], d) {
					if !seen[k] {
						seen[k] = true
						n[k]++
					}
				}
			}
		}
		var fs []Facet
		for k, v := range n {
			fs = append(fs, Facet{k, v})
		}
		sort.Slice(fs, func(i, j int) bool {
			if fs[i].Count != fs[j].Count {
				return fs[i].Count > fs[j].Count
			}
			return fs[i].Name < fs[j].Name
		})
		facets[d] = fs
	}
	rows, gs := view("")
	var out []ConvRow
	if f.All {
		for _, r := range rows {
			if sn, ok := hits[r.ID]; hits == nil || ok {
				r.Snippet, r.Branches = sn, 1
				out = append(out, r)
			}
		}
	} else {
		for _, g := range gs {
			rep := g.rep(rows)
			for _, i := range g.members {
				if sn, ok := hits[rows[i].ID]; ok && rep.Snippet == "" {
					rep.Snippet = sn
				}
			}
			out = append(out, rep)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, facets, nil
}

func splitModels(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

const rowCols = `id, tool, host, external_id, parent_external_id, title, cwd, git_branch, model, started_at, updated_at, message_count, root_id, continues_from, models`

func (s *Store) rowsWhere(tool, host string) ([]ConvRow, error) {
	q := `SELECT ` + rowCols + ` FROM conversations WHERE 1=1`
	var args []any
	if tool != "" {
		q += ` AND tool=?`
		args = append(args, tool)
	}
	if host != "" {
		q += ` AND host=?`
		args = append(args, host)
	}
	return s.query(q+` ORDER BY updated_at DESC`, args...)
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
		var ms string
		if err := rows.Scan(&r.ID, &r.Tool, &r.Host, &r.ExternalID, &r.ParentID, &r.Title, &r.Cwd, &r.GitBranch, &r.Model, &st, &up, &r.MessageCount, &r.RootID, &r.ContinuesFrom, &ms); err != nil {
			return nil, err
		}
		r.Models = splitModels(ms)
		r.StartedAt, r.UpdatedAt = fromUnix(st), fromUnix(up)
		out = append(out, r)
	}
	return out, rows.Err()
}

// hits は本文に q を含む会話と、その抜粋（一致を \x02 \x03 で囲む）。
func (s *Store) hits(q string) (map[int64]string, error) {
	out := map[int64]string{}
	if len([]rune(q)) < 3 {
		rows, err := s.db.Query(`SELECT DISTINCT conversation_id FROM parts WHERE text LIKE ? ESCAPE '\'`, "%"+escapeLike(q)+"%")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out[id] = ""
		}
		return out, rows.Err()
	}
	// snippet() は集計の中で使えないので、会話ごとの最初の一致を Go 側で拾う。
	rows, err := s.db.Query(`SELECT p.conversation_id, snippet(parts_fts, 0, char(2), char(3), '…', 24)
	  FROM parts_fts JOIN parts p ON p.id = parts_fts.rowid
	  WHERE parts_fts MATCH ?`, ftsPhrase(q))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var sn string
		if err := rows.Scan(&id, &sn); err != nil {
			return nil, err
		}
		if _, ok := out[id]; !ok {
			out[id] = sn
		}
	}
	return out, rows.Err()
}

// group は同じ会話とみなす行の集まり（rows の添字）。
type group struct{ members []int }

// rep は一覧に出す1行。サブエージェントでない中で一番新しいもの（並びが新しい順なので最初）。
func (g group) rep(rows []ConvRow) ConvRow {
	best, n := -1, 0
	for _, i := range g.members {
		if rows[i].ParentID == "" || rows[i].orphan {
			n++
			if best < 0 {
				best = i
			}
		}
	}
	if best < 0 {
		best, n = g.members[0], len(g.members)
	}
	r := rows[best]
	r.Branches = n
	return r
}

// groups は同じツールの中で、次のどれかでつながる会話を1つにまとめる。
//   - 同じ会話 ID（別のマシンにある写し。デスクトップアプリはリモートの会話を手元にも残す）
//   - 同じ最初のメッセージ（分岐。履歴を写して別の会話になったもの）
//   - 続き（文脈があふれて、前の会話の要約から始めた会話）
//   - サブエージェントと親の会話（親が無ければ、同じ親のサブエージェントどうし）
func groups(rows []ConvRow) []group {
	parent := make([]int, len(rows))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		if ra, rb := find(a), find(b); ra != rb {
			if ra < rb {
				parent[rb] = ra
			} else {
				parent[ra] = rb
			}
		}
	}
	byKey := map[string]int{}
	link := func(i int, key string) {
		if j, ok := byKey[key]; ok {
			union(i, j)
		} else {
			byKey[key] = i
		}
	}
	ext := map[string]bool{}
	for _, r := range rows {
		ext[r.Tool+"\x00"+r.ExternalID] = true
	}
	for i, r := range rows {
		t := r.Tool + "\x00"
		link(i, "id\x00"+t+r.ExternalID)
		if r.RootID != "" {
			link(i, "root\x00"+t+r.RootID)
		}
		if r.ContinuesFrom != "" {
			link(i, "id\x00"+t+r.ContinuesFrom)
		}
		if r.ParentID != "" {
			if ext[t+r.ParentID] {
				link(i, "id\x00"+t+r.ParentID)
			} else {
				rows[i].orphan = true
				link(i, "orphan\x00"+t+r.ParentID)
			}
		}
	}
	idx := map[int]int{}
	var out []group
	for i := range rows {
		root := find(i)
		k, ok := idx[root]
		if !ok {
			k = len(out)
			idx[root] = k
			out = append(out, group{})
		}
		out[k].members = append(out[k].members, i)
	}
	return out
}

// Related は同じ会話としてまとめた行（サブエージェントを除く、古い順）と、サブエージェント（古い順）。
func (s *Store) Related(c *ConvRow) (versions, children []ConvRow, err error) {
	rows, err := s.rowsWhere(c.Tool, "")
	if err != nil {
		return nil, nil, err
	}
	for _, g := range groups(rows) {
		in := false
		for _, i := range g.members {
			if rows[i].ID == c.ID {
				in = true
			}
		}
		if !in {
			continue
		}
		for _, i := range g.members {
			if rows[i].ParentID != "" && !rows[i].orphan {
				children = append(children, rows[i])
			} else {
				versions = append(versions, rows[i])
			}
		}
	}
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].UpdatedAt.Before(versions[j].UpdatedAt) })
	sort.SliceStable(children, func(i, j int) bool { return children[i].StartedAt.Before(children[j].StartedAt) })
	return versions, children, nil
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
	var ms string
	err := s.db.QueryRow(`SELECT `+rowCols+` FROM conversations WHERE id=?`, id).
		Scan(&r.ID, &r.Tool, &r.Host, &r.ExternalID, &r.ParentID, &r.Title, &r.Cwd, &r.GitBranch, &r.Model, &st, &up, &r.MessageCount, &r.RootID, &r.ContinuesFrom, &ms)
	if err != nil {
		return nil, nil, err
	}
	r.StartedAt, r.UpdatedAt, r.Models = fromUnix(st), fromUnix(up), splitModels(ms)
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
