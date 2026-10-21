package store

import (
	"database/sql"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Skill は AI に渡す手順書（SKILL.md）と、それに付くファイル（規則・資料・スクリプトなど）。
// 本文だけの小さな Skill はプロンプトとして使える。
type Skill struct {
	ID          int64
	Name        string
	Description string
	Body        string // SKILL.md の本文（前書き＝frontmatter は除く）
	Source      string // 出どころ（GitHub の URL・マシン名など）。画面で作ったものは空
	Version     string // 出どころの版（GitHub はコミット）
	Files       []SkillFile
	UpdatedAt   time.Time
}

// SkillFile は Skill に付くファイル（SKILL.md は含まない）。
type SkillFile struct {
	Path string
	Size int
	Data []byte `json:"-"`
}

// SkillFound は履歴から見つけた Skill（Claude Code が読み込んだ SKILL.md）。
type SkillFound struct {
	Name     string
	Body     string
	Uses     int       // 使った会話の数
	LastUsed time.Time // 最後に使った会話
}

const skillSchema = `CREATE TABLE IF NOT EXISTS skills (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS skill_files (
  skill_id INTEGER NOT NULL REFERENCES skills ON DELETE CASCADE,
  path TEXT NOT NULL,
  data BLOB NOT NULL,
  PRIMARY KEY (skill_id, path)
)`

// migrateSkills は前の版のプロンプト（prompts）を Skill へ移す。
func migrateSkills(db *sql.DB) error {
	var n int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='prompts'`).Scan(&n)
	if n == 0 {
		return nil
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO skills (name, description, body, updated_at) SELECT name, description, body, updated_at FROM prompts;
	  DROP TABLE prompts`)
	return err
}

// Skills は Skill の一覧（付くファイルの中身は読まない）。
func (s *Store) Skills() ([]Skill, error) {
	rows, err := s.db.Query(`SELECT id, name, description, body, source, version, updated_at FROM skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		var k Skill
		var u int64
		if err := rows.Scan(&k.ID, &k.Name, &k.Description, &k.Body, &k.Source, &k.Version, &u); err != nil {
			return nil, err
		}
		k.UpdatedAt = fromUnix(u)
		out = append(out, k)
	}
	return out, rows.Err()
}

// GetSkill は Skill を付くファイルの一覧つきで返す（中身は SkillFileData で読む）。
func (s *Store) GetSkill(id int64) (Skill, error) {
	var k Skill
	var u int64
	err := s.db.QueryRow(`SELECT id, name, description, body, source, version, updated_at FROM skills WHERE id=?`, id).
		Scan(&k.ID, &k.Name, &k.Description, &k.Body, &k.Source, &k.Version, &u)
	if err != nil {
		return k, err
	}
	k.UpdatedAt = fromUnix(u)
	rows, err := s.db.Query(`SELECT path, length(data) FROM skill_files WHERE skill_id=? ORDER BY path`, id)
	if err != nil {
		return k, err
	}
	defer rows.Close()
	for rows.Next() {
		var f SkillFile
		if err := rows.Scan(&f.Path, &f.Size); err != nil {
			return k, err
		}
		k.Files = append(k.Files, f)
	}
	return k, rows.Err()
}

// SkillByName は名前で Skill を探す（無ければ sql.ErrNoRows）。
func (s *Store) SkillByName(name string) (Skill, error) {
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM skills WHERE name=?`, name).Scan(&id); err != nil {
		return Skill{}, err
	}
	return s.GetSkill(id)
}

// SkillFileData は Skill に付くファイルの中身。
func (s *Store) SkillFileData(id int64, p string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRow(`SELECT data FROM skill_files WHERE skill_id=? AND path=?`, id, p).Scan(&b)
	return b, err
}

// SaveSkill は Skill を作る（ID が 0）か直し、ID を返す。Files が nil でなければ付くファイルを置き換える。
func (s *Store) SaveSkill(k Skill) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if k.ID == 0 {
		res, err := tx.Exec(`INSERT INTO skills (name, description, body, source, version, updated_at) VALUES (?,?,?,?,?,?)`,
			k.Name, k.Description, k.Body, k.Source, k.Version, now)
		if err != nil {
			return 0, err
		}
		if k.ID, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else if _, err := tx.Exec(`UPDATE skills SET name=?, description=?, body=?, source=?, version=?, updated_at=? WHERE id=?`,
		k.Name, k.Description, k.Body, k.Source, k.Version, now, k.ID); err != nil {
		return 0, err
	}
	if k.Files != nil {
		if _, err := tx.Exec(`DELETE FROM skill_files WHERE skill_id=?`, k.ID); err != nil {
			return 0, err
		}
		for _, f := range k.Files {
			if _, err := tx.Exec(`INSERT INTO skill_files (skill_id, path, data) VALUES (?,?,?)`, k.ID, f.Path, f.Data); err != nil {
				return 0, err
			}
		}
	}
	return k.ID, tx.Commit()
}

func (s *Store) DeleteSkill(id int64) error {
	_, err := s.db.Exec(`DELETE FROM skills WHERE id=?`, id)
	return err
}

// ParseSkillMD は SKILL.md を前書きの name・description と本文に分ける。
func ParseSkillMD(md string) (name, desc, body string) {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(md, "---\n") {
		return "", "", strings.TrimSpace(md)
	}
	front, rest, ok := strings.Cut(md[4:], "\n---")
	if !ok {
		return "", "", strings.TrimSpace(md)
	}
	_, rest, _ = strings.Cut(rest, "\n")
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok || strings.HasPrefix(k, " ") {
			continue
		}
		v = strings.TrimSpace(v)
		// 折り返しの書き方（> や |）は続く字下げの行をつなぐ
		if v == "" || v == ">" || v == "|" || v == ">-" || v == "|-" {
			var parts []string
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || lines[i+1] == "") {
				i++
				parts = append(parts, strings.TrimSpace(lines[i]))
			}
			v = strings.TrimSpace(strings.Join(parts, " "))
		}
		v = strings.Trim(v, `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "description":
			desc = v
		}
	}
	return name, desc, strings.TrimSpace(rest)
}

// SkillMD は Skill を SKILL.md の形（前書きつき）にする。
func (k Skill) SkillMD() string {
	q := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
	return "---\nname: " + k.Name + "\ndescription: " + q(k.Description) + "\n---\n\n" + k.Body + "\n"
}

// skillMark は Claude Code が Skill を読み込んだときの本文の頭。続けて Skill の場所、空行、SKILL.md の本文が来る。
const skillMark = "Base directory for this skill: "

var skillCache struct {
	sync.Mutex
	at  time.Time
	out []SkillFound
}

// SkillsInHistory は履歴の中で使われた Skill を、よく使った順に返す（同じ名前は最後に使ったときの本文）。
// 全部の本文を見るので重い。5 分覚えておく。
func (s *Store) SkillsInHistory() ([]SkillFound, error) {
	skillCache.Lock()
	defer skillCache.Unlock()
	if time.Since(skillCache.at) < 5*time.Minute && skillCache.out != nil {
		return skillCache.out, nil
	}
	rows, err := s.db.Query(`SELECT p.text, p.conversation_id, c.updated_at FROM parts p JOIN conversations c ON c.id = p.conversation_id
	  WHERE p.kind = 'text' AND substr(p.text, 1, ?) = ?`, len(skillMark), skillMark)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		SkillFound
		convs map[int64]bool
	}
	m := map[string]*acc{}
	for rows.Next() {
		var text string
		var conv, up int64
		if err := rows.Scan(&text, &conv, &up); err != nil {
			return nil, err
		}
		name, body := parseSkillUse(text)
		if name == "" || body == "" {
			continue
		}
		a := m[name]
		if a == nil {
			a = &acc{SkillFound: SkillFound{Name: name}, convs: map[int64]bool{}}
			m[name] = a
		}
		a.convs[conv] = true
		if t := fromUnix(up); !t.Before(a.LastUsed) {
			a.LastUsed, a.Body = t, body
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []SkillFound{}
	for _, a := range m {
		a.Uses = len(a.convs)
		out = append(out, a.SkillFound)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Uses != out[j].Uses {
			return out[i].Uses > out[j].Uses
		}
		return out[i].Name < out[j].Name
	})
	skillCache.out, skillCache.at = out, time.Now()
	return out, nil
}

// parseSkillUse は Skill を読み込んだ本文から、名前（場所の最後のフォルダ名）と SKILL.md の本文を取り出す。
func parseSkillUse(text string) (name, body string) {
	head, rest, ok := strings.Cut(strings.TrimPrefix(text, skillMark), "\n")
	if !ok {
		return "", ""
	}
	dir := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(head), `\`, "/"), "/")
	return path.Base(dir), strings.TrimSpace(rest)
}
