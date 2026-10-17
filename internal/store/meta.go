package store

// 会話ごとに画面で付けたもの（名前・ピン留め・アーカイブ）。取り込み直しで消えないよう、会話とは別に持つ。

type meta struct {
	title            string
	pinned, archived bool
}

func metaKey(tool, host, ext string) string { return tool + "\x00" + host + "\x00" + ext }

func (s *Store) applyMeta(rows []ConvRow) error {
	q, err := s.db.Query(`SELECT tool, host, external_id, title, pinned, archived FROM conv_meta`)
	if err != nil {
		return err
	}
	defer q.Close()
	m := map[string]meta{}
	for q.Next() {
		var t, h, e string
		var v meta
		if err := q.Scan(&t, &h, &e, &v.title, &v.pinned, &v.archived); err != nil {
			return err
		}
		m[metaKey(t, h, e)] = v
	}
	if len(m) == 0 {
		return q.Err()
	}
	for i := range rows {
		if v, ok := m[metaKey(rows[i].Tool, rows[i].Host, rows[i].ExternalID)]; ok {
			if v.title != "" {
				rows[i].Title = v.title
			}
			rows[i].Pinned, rows[i].Archived = v.pinned, v.archived
		}
	}
	return q.Err()
}

// SetMeta は会話 id に名前（空なら元の題名）・ピン留め・アーカイブを付ける。nil の項目は変えない。
func (s *Store) SetMeta(id int64, title *string, pinned, archived *bool) error {
	var t, h, e string
	if err := s.db.QueryRow(`SELECT tool, host, external_id FROM conversations WHERE id=?`, id).Scan(&t, &h, &e); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO conv_meta (tool, host, external_id) VALUES (?,?,?)`, t, h, e); err != nil {
		return err
	}
	if title != nil {
		if _, err := s.db.Exec(`UPDATE conv_meta SET title=? WHERE tool=? AND host=? AND external_id=?`, *title, t, h, e); err != nil {
			return err
		}
	}
	if pinned != nil {
		if _, err := s.db.Exec(`UPDATE conv_meta SET pinned=? WHERE tool=? AND host=? AND external_id=?`, *pinned, t, h, e); err != nil {
			return err
		}
	}
	if archived != nil {
		if _, err := s.db.Exec(`UPDATE conv_meta SET archived=? WHERE tool=? AND host=? AND external_id=?`, *archived, t, h, e); err != nil {
			return err
		}
	}
	return nil
}
