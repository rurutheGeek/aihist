package store

import (
	"database/sql"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Prompt は登録したプロンプト（Skill も同じ形で持つ）。MCP でつないだ AI に配る。
type Prompt struct {
	ID          int64
	Name        string
	Description string
	Body        string
	UpdatedAt   time.Time
}

// SkillFound は履歴から見つけた Skill（Claude Code が読み込んだ SKILL.md）。
type SkillFound struct {
	Name     string
	Body     string
	Uses     int       // 使った会話の数
	LastUsed time.Time // 最後に使った会話
}

const promptSchema = `CREATE TABLE IF NOT EXISTS prompts (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL,
  updated_at INTEGER NOT NULL
)`

func (s *Store) Prompts() ([]Prompt, error) {
	rows, err := s.db.Query(`SELECT id, name, description, body, updated_at FROM prompts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Prompt
	for rows.Next() {
		var p Prompt
		var u int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Body, &u); err != nil {
			return nil, err
		}
		p.UpdatedAt = fromUnix(u)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPrompt(id int64) (Prompt, error) {
	var p Prompt
	var u int64
	err := s.db.QueryRow(`SELECT id, name, description, body, updated_at FROM prompts WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Description, &p.Body, &u)
	p.UpdatedAt = fromUnix(u)
	return p, err
}

// PromptByName は名前でプロンプトを探す（無ければ sql.ErrNoRows）。
func (s *Store) PromptByName(name string) (Prompt, error) {
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM prompts WHERE name=?`, name).Scan(&id); err != nil {
		return Prompt{}, err
	}
	return s.GetPrompt(id)
}

// SavePrompt はプロンプトを作る（ID が 0）か直し、ID を返す。
func (s *Store) SavePrompt(p Prompt) (int64, error) {
	now := time.Now().Unix()
	if p.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO prompts (name, description, body, updated_at) VALUES (?,?,?,?)`, p.Name, p.Description, p.Body, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE prompts SET name=?, description=?, body=?, updated_at=? WHERE id=?`, p.Name, p.Description, p.Body, now, p.ID)
	return p.ID, err
}

func (s *Store) DeletePrompt(id int64) error {
	_, err := s.db.Exec(`DELETE FROM prompts WHERE id=?`, id)
	return err
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
		name, body := parseSkill(text)
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
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
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

// parseSkill は Skill を読み込んだ本文から、名前（場所の最後のフォルダ名）と SKILL.md の本文を取り出す。
func parseSkill(text string) (name, body string) {
	head, rest, ok := strings.Cut(strings.TrimPrefix(text, skillMark), "\n")
	if !ok {
		return "", ""
	}
	dir := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(head), `\`, "/"), "/")
	return path.Base(dir), strings.TrimSpace(rest)
}
