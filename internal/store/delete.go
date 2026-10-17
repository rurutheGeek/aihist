package store

import "time"

// 消した会話は、印（ツール・マシン・会話 ID）だけを残す。マシンからの送信や書き出しの上げ直しで戻らないようにするため。
// 印を消せば、次に同じ元データが来たときにまた取り込む。

// Delete は会話 id と、同じ会話としてまとめた行（分岐・写し・続き・サブエージェント）を消す。
func (s *Store) Delete(ids ...int64) (int, error) {
	targets := map[int64]ConvRow{}
	for _, id := range ids {
		c, _, err := s.Get(id)
		if err != nil {
			continue
		}
		v, ch, err := s.Related(c)
		if err != nil {
			return 0, err
		}
		targets[c.ID] = *c
		for _, r := range append(v, ch...) {
			targets[r.ID] = r
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, r := range targets {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO deleted (tool, host, external_id, deleted_at) VALUES (?,?,?,?)`, r.Tool, r.Host, r.ExternalID, now); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM conversations WHERE id=?`, r.ID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM conv_meta WHERE tool=? AND host=? AND external_id=?`, r.Tool, r.Host, r.ExternalID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM conv_tags WHERE tool=? AND host=? AND external_id=?`, r.Tool, r.Host, r.ExternalID); err != nil {
			return 0, err
		}
	}
	return len(targets), tx.Commit()
}

// IsDeleted は消した会話か。
func (s *Store) IsDeleted(tool, host, externalID string) bool {
	var n int
	s.db.QueryRow(`SELECT count(*) FROM deleted WHERE tool=? AND host=? AND external_id=?`, tool, host, externalID).Scan(&n)
	return n > 0
}

// Deleted は消した会話の印の数。
func (s *Store) Deleted() int {
	var n int
	s.db.QueryRow(`SELECT count(*) FROM deleted`).Scan(&n)
	return n
}

// ForgetDeleted は消した会話の印を全部消す。
func (s *Store) ForgetDeleted() error {
	_, err := s.db.Exec(`DELETE FROM deleted`)
	return err
}
