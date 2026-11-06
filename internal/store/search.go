package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"modernc.org/sqlite"
)

// 本文の検索の索引。本文を小文字にし、空白（改行も）の続きを1つの空白にしてから、
// 3文字ずつの切れ端（と末尾の1・2文字）を語として持ち、どの部分に出てくるかだけを覚える
// （位置も本文の写しも持たないので、本文より小さく済む）。3文字以下の語は索引だけで決まり、
// 4文字以上は切れ端をすべて含む部分を索引で絞ってから本文で確かめる。
// 語は UTF-8 の16進にする（FTS5 の区切り方に左右されないように）。
const searchSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS parts_search USING fts5(t, content='', contentless_delete=1, detail='none');
CREATE TRIGGER IF NOT EXISTS parts_si AFTER INSERT ON parts BEGIN
  INSERT INTO parts_search(rowid, t) VALUES (new.id, search_tokens(new.text));
END;
CREATE TRIGGER IF NOT EXISTS parts_sd AFTER DELETE ON parts BEGIN
  DELETE FROM parts_search WHERE rowid = old.id;
END;
CREATE TRIGGER IF NOT EXISTS parts_su AFTER UPDATE OF text ON parts BEGIN
  DELETE FROM parts_search WHERE rowid = old.id;
  INSERT INTO parts_search(rowid, t) VALUES (new.id, search_tokens(new.text));
END;
`

// searchVersion は索引の作り方の版。変えたら作り直す。
const searchVersion = "1"

const gram = 3

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("search_tokens", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return searchTokens(v), nil
		case []byte:
			return searchTokens(string(v)), nil
		}
		return "", nil
	})
}

// fold は空白の続きを1つの空白にし、前後の空白を除く。lower なら小文字にもする。
// 小文字にしても文字の数は変わらないので、lower の有無で同じ位置が同じ文字を指す。
func fold(s string, lower bool) []rune {
	out := make([]rune, 0, len(s))
	sp := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			sp = len(out) > 0
			continue
		}
		if sp {
			out = append(out, ' ')
			sp = false
		}
		if lower {
			r = unicode.ToLower(r)
		}
		out = append(out, r)
	}
	return out
}

func gramHex(rs []rune) string {
	var b strings.Builder
	var buf [utf8.UTFMax]byte
	for _, r := range rs {
		n := utf8.EncodeRune(buf[:], r)
		b.WriteString(hex.EncodeToString(buf[:n]))
	}
	return b.String()
}

// searchTokens は本文を索引の語にする。同じ語は1度だけ（位置を持たないので要らない）。
func searchTokens(text string) string {
	rs := fold(text, true)
	seen := make(map[string]struct{}, len(rs)/2)
	var out strings.Builder
	for i := range rs {
		t := gramHex(rs[i:min(i+gram, len(rs))])
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(t)
	}
	return out.String()
}

// searchQuery は検索語を索引への問い合わせにする。exact なら、当たった部分は確かめなくても語を含む。
func searchQuery(q string) (match string, exact bool) {
	rs := fold(q, true)
	switch {
	case len(rs) == 0:
		return "", false
	case len(rs) < gram:
		// その文字で始まる切れ端（末尾の短いものも）
		return gramHex(rs) + "*", true
	}
	var terms []string
	for i := 0; i+gram <= len(rs); i++ {
		terms = append(terms, `"`+gramHex(rs[i:i+gram])+`"`)
	}
	return strings.Join(terms, " AND "), len(rs) == gram
}

// searchReady は索引が出来上がっているか。
func (s *Store) searchReady() bool { return s.Setting("search_index", "") == searchVersion }

// hits は本文に q を含む会話と、その中で q を含む部分の番号（索引を作っている間は 0）。
func (s *Store) hits(ctx context.Context, q string) (map[int64]int64, error) {
	out := map[int64]int64{}
	match, exact := searchQuery(q)
	if match == "" {
		return out, nil
	}
	if !s.searchReady() {
		// 索引を作っている間だけ
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT conversation_id FROM parts WHERE text LIKE ? ESCAPE '\'`, "%"+escapeLike(q)+"%")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out[id] = 0
		}
		return out, rows.Err()
	}
	// 会話の番号は parts_conv の索引だけから引く（本文の行を読まない）
	rows, err := s.db.QueryContext(ctx, `SELECT conversation_id, id FROM parts INDEXED BY parts_conv
	  WHERE id IN (SELECT rowid FROM parts_search WHERE parts_search MATCH ?) ORDER BY conversation_id, id`, match)
	if err != nil {
		return nil, err
	}
	cand := map[int64][]int64{}
	var order []int64
	for rows.Next() {
		var c, id int64
		if err := rows.Scan(&c, &id); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := cand[c]; !ok {
			order = append(order, c)
		}
		cand[c] = append(cand[c], id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if exact {
		for _, c := range order {
			out[c] = cand[c][0]
		}
		return out, nil
	}
	lq := string(fold(q, true))
	for _, c := range order {
		for _, id := range cand[c] {
			var t string
			if err := s.db.QueryRowContext(ctx, `SELECT text FROM parts WHERE id=?`, id).Scan(&t); err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				return nil, err
			}
			if strings.Contains(string(fold(t, true)), lq) {
				out[c] = id
				break
			}
		}
	}
	return out, nil
}

// snippet は q が最初に出るあたり（一致を \x02 \x03 で囲む）。part が 0 なら会話 conv の本文から探す。
func (s *Store) snippet(ctx context.Context, conv, part int64, q string) string {
	lq := fold(q, true)
	if len(lq) == 0 {
		return ""
	}
	var rows *sql.Rows
	var err error
	if part != 0 {
		rows, err = s.db.QueryContext(ctx, `SELECT text FROM parts WHERE id=?`, part)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT text FROM parts WHERE conversation_id=? AND text LIKE ? ESCAPE '\' ORDER BY id`, conv, "%"+escapeLike(q)+"%")
	}
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if rows.Scan(&t) != nil {
			return ""
		}
		if sn := cut(t, lq); sn != "" {
			return sn
		}
	}
	return ""
}

// cut は t の中で lq（fold した語）が最初に出るところの前後を切り出す。
func cut(t string, lq []rune) string {
	rs, lr := fold(t, false), fold(t, true)
	at := -1
	for i := 0; i+len(lq) <= len(lr); i++ {
		if slices.Equal(lr[i:i+len(lq)], lq) {
			at = i
			break
		}
	}
	if at < 0 {
		return ""
	}
	const around = 40
	end := at + len(lq)
	from, to := max(at-around, 0), min(end+around, len(rs))
	var b strings.Builder
	if from > 0 {
		b.WriteString("…")
	}
	b.WriteString(string(rs[from:at]) + "\x02" + string(rs[at:end]) + "\x03" + string(rs[end:to]))
	if to < len(rs) {
		b.WriteString("…")
	}
	return b.String()
}

// BuildSearch は本文の検索の索引を作る（前の版の索引からの移り変わりも）。作っている間も
// 画面は使え、検索は索引を使わずに探す。止まっても次の起動で続きから作る。
func (s *Store) BuildSearch(ctx context.Context, logf func(string, ...any)) error {
	// 前の版の索引（3文字ずつの全文検索）はもう更新しない。消すのは新しい索引が出来てから。
	for _, q := range []string{`DROP TRIGGER IF EXISTS parts_ai`, `DROP TRIGGER IF EXISTS parts_ad`} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if !s.searchReady() {
		if err := s.fillSearch(ctx, logf); err != nil {
			return err
		}
	}
	return s.dropOldSearch(ctx, logf)
}

// dropOldSearch は前の版の索引を消して DB を詰める。大きいと何分もかかるので、別のつなぎで行い、
// その間も画面の読み出しは止めない（書き込みは終わるまで待たされることがある）。
func (s *Store) dropOldSearch(ctx context.Context, logf func(string, ...any)) error {
	var old int
	s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='parts_fts'`).Scan(&old)
	if old == 0 || s.path == "" {
		return nil
	}
	db, err := sql.Open("sqlite", dsn(s.path))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	logf("検索の索引: 前の索引を消す")
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS parts_fts`); err != nil {
		return err
	}
	if n, err := compact(ctx, db); err != nil {
		logf("検索の索引: DB を詰められなかった: %v", err)
	} else if n > 0 {
		logf("検索の索引: DB を %.1f MB 小さくした", float64(n)/1e6)
	}
	return nil
}

func (s *Store) fillSearch(ctx context.Context, logf func(string, ...any)) error {
	// 索引の作り始めより前にある本文だけを入れる。後から入る本文はトリガーが入れる。
	until := s.Setting("search_index_until", "")
	if until == "" {
		var m int64
		s.db.QueryRowContext(ctx, `SELECT ifnull(max(id), 0) FROM parts`).Scan(&m)
		until = fmtInt(m)
		if err := s.SetSetting("search_index_until", until); err != nil {
			return err
		}
	}
	last := parseInt(s.Setting("search_index_done", "0"))
	end := parseInt(until)
	var total int
	s.db.QueryRowContext(ctx, `SELECT count(*) FROM parts WHERE id > ? AND id <= ?`, last, end).Scan(&total)
	if total > 0 {
		logf("検索の索引: %d 件の本文から作る", total)
	}
	done := 0
	for last < end {
		type part struct {
			id   int64
			text string
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id, text FROM parts WHERE id > ? AND id <= ? ORDER BY id LIMIT 500`, last, end)
		if err != nil {
			return err
		}
		var ps []part
		for rows.Next() {
			var p part
			if err := rows.Scan(&p.id, &p.text); err != nil {
				rows.Close()
				return err
			}
			ps = append(ps, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ps) == 0 {
			break
		}
		toks := make([]string, len(ps))
		for i, p := range ps {
			toks[i] = searchTokens(p.text)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for i, p := range ps {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO parts_search(rowid, t) VALUES (?, ?)`, p.id, toks[i]); err != nil {
				tx.Rollback()
				return err
			}
		}
		last = ps[len(ps)-1].id
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES ('search_index_done', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmtInt(last)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		done += len(ps)
		if done%50000 < len(ps) {
			logf("検索の索引: %d / %d", done, total)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO parts_search(parts_search) VALUES ('optimize')`); err != nil {
		return err
	}
	if err := s.SetSetting("search_index", searchVersion); err != nil {
		return err
	}
	s.db.ExecContext(ctx, `DELETE FROM settings WHERE key IN ('search_index_until', 'search_index_done')`)
	logf("検索の索引: 出来た")
	return nil
}

// compact は空いた場所が大きい（64MB 以上で、全体の 1/4 以上）ときだけ DB を詰める。
// 作業用のファイルは DB と同じ場所に置く（/tmp がメモリのことがあるため）。
func compact(ctx context.Context, db *sql.DB) (int64, error) {
	var free, pages, size int64
	db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free)
	db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages)
	db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size)
	if free*size < 64<<20 || free*4 < pages {
		return 0, nil
	}
	var seq int
	var name, file string
	if err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return 0, err
	}
	if file != "" {
		if _, err := db.ExecContext(ctx, `PRAGMA temp_store_directory = '`+strings.ReplaceAll(filepath.Dir(file), "'", "''")+`'`); err != nil {
			return 0, err
		}
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return 0, err
	}
	db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return free * size, nil
}

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

func parseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
