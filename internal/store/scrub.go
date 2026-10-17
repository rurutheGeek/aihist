package store

// Rewrite は会話の本文と題名を fn で書き換える（集めた履歴から情報を消すとき）。
// 全文検索の索引も書き換えた分だけ入れ直す。変えた本文の数を返す。
func (s *Store) Rewrite(fn func(string) string, progress func(done, total int)) (int, error) {
	var total int
	s.db.QueryRow(`SELECT count(*) FROM parts`).Scan(&total)
	changed, done := 0, 0
	var last int64
	for {
		rows, err := s.db.Query(`SELECT id, text FROM parts WHERE id > ? ORDER BY id LIMIT 2000`, last)
		if err != nil {
			return changed, err
		}
		type upd struct {
			id       int64
			old, new string
		}
		var ups []upd
		n := 0
		for rows.Next() {
			var id int64
			var t string
			if err := rows.Scan(&id, &t); err != nil {
				rows.Close()
				return changed, err
			}
			n++
			last = id
			if nt := fn(t); nt != t {
				ups = append(ups, upd{id, t, nt})
			}
		}
		rows.Close()
		if n == 0 {
			break
		}
		if len(ups) > 0 {
			tx, err := s.db.Begin()
			if err != nil {
				return changed, err
			}
			for _, u := range ups {
				if _, err := tx.Exec(`INSERT INTO parts_fts(parts_fts, rowid, text) VALUES ('delete', ?, ?)`, u.id, u.old); err != nil {
					tx.Rollback()
					return changed, err
				}
				if _, err := tx.Exec(`UPDATE parts SET text=? WHERE id=?`, u.new, u.id); err != nil {
					tx.Rollback()
					return changed, err
				}
				if _, err := tx.Exec(`INSERT INTO parts_fts(rowid, text) VALUES (?, ?)`, u.id, u.new); err != nil {
					tx.Rollback()
					return changed, err
				}
			}
			if err := tx.Commit(); err != nil {
				return changed, err
			}
			changed += len(ups)
		}
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	for _, q := range []string{`SELECT id, title FROM conversations`, `SELECT rowid, title FROM conv_meta`} {
		rows, err := s.db.Query(q)
		if err != nil {
			return changed, err
		}
		type upd struct {
			id int64
			t  string
		}
		var ups []upd
		for rows.Next() {
			var id int64
			var t string
			rows.Scan(&id, &t)
			if nt := fn(t); nt != t {
				ups = append(ups, upd{id, nt})
			}
		}
		rows.Close()
		table := "conversations"
		col := "id"
		if q[len(q)-9:] == "conv_meta" {
			table, col = "conv_meta", "rowid"
		}
		for _, u := range ups {
			s.db.Exec(`UPDATE `+table+` SET title=? WHERE `+col+`=?`, u.t, u.id)
		}
	}
	return changed, nil
}
