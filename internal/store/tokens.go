package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"
)

// Token は各マシンが送るときに使う合言葉。DB には sha256 だけを置く。
type Token struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	UsedAt    time.Time
}

func tokenHash(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}

// CreateToken は新しい合言葉を作り、平文を返す（平文はこのときしか分からない）。
func (s *Store) CreateToken(name string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	plain := "aih_" + base64.RawURLEncoding.EncodeToString(b)
	_, err := s.db.Exec(`INSERT INTO tokens (name, hash, created_at) VALUES (?,?,?)`, name, tokenHash(plain), time.Now().Unix())
	return plain, err
}

func (s *Store) Tokens() ([]Token, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, used_at FROM tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var c, u int64
		if err := rows.Scan(&t.ID, &t.Name, &c, &u); err != nil {
			return nil, err
		}
		t.CreatedAt, t.UsedAt = fromUnix(c), fromUnix(u)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteToken(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tokens WHERE id=?`, id)
	return err
}

// CheckToken は合言葉が発行済みかを確かめ、最後に使った時刻を残す（1分に1回まで）。
func (s *Store) CheckToken(plain string) bool {
	if plain == "" {
		return false
	}
	now := time.Now().Unix()
	var id, used int64
	if s.db.QueryRow(`SELECT id, used_at FROM tokens WHERE hash=?`, tokenHash(plain)).Scan(&id, &used) != nil {
		return false
	}
	if now-used > 60 {
		s.db.Exec(`UPDATE tokens SET used_at=? WHERE id=?`, now, id)
	}
	return true
}

// Count は マシン・ツールごとの会話数。
type Count struct {
	Host, Tool string
	N          int
}

func (s *Store) Counts() ([]Count, error) {
	rows, err := s.db.Query(`SELECT host, tool, count(*) FROM conversations GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Host, &c.Tool, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Size は DB の大きさ（バイト）。
func (s *Store) Size() int64 {
	var pages, size int64
	s.db.QueryRow(`PRAGMA page_count`).Scan(&pages)
	s.db.QueryRow(`PRAGMA page_size`).Scan(&size)
	return pages * size
}
