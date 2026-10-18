package store

import (
	"encoding/json"
	"time"
)

// Detected はマシンが見つけた AI の記録。
type Detected struct {
	Tool  string `json:"tool"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// Machine は送ってくるマシンと、そのマシンの設定。
type Machine struct {
	Host        string
	SeenAt      time.Time
	Version     string
	Detected    []Detected
	Tools       string // "*" は全部（後で見つかったものも）、それ以外はカンマ区切り
	Attachments bool   // 添付ファイル（画像など）も送る
	Schedule    string // cron の式。空は全体の設定に従う
	Cleanup     int    // 手元から消す日数。-1 は全体の設定、0 は消さない
	Attach      string // 送る添付ファイルの種類（"*" は全部、"*,-video" は動画以外、"-" は送らない）
	Hide        string // 送る前に消す情報の種類（"secret,email"、"-" は消さない）。空は全体の設定に従う
}

const machineCols = `host, seen_at, version, detected, tools, attachments, schedule, cleanup, attach, hide`

func scanMachine(sc interface{ Scan(...any) error }) (Machine, error) {
	var m Machine
	var seen int64
	var det string
	err := sc.Scan(&m.Host, &seen, &m.Version, &det, &m.Tools, &m.Attachments, &m.Schedule, &m.Cleanup, &m.Attach, &m.Hide)
	m.SeenAt = fromUnix(seen)
	json.Unmarshal([]byte(det), &m.Detected)
	return m, err
}

// Machine は1台を返す。無ければ既定の設定で返す（ok は false）。
func (s *Store) Machine(host string) (Machine, bool) {
	m, err := scanMachine(s.db.QueryRow(`SELECT `+machineCols+` FROM machines WHERE host=?`, host))
	if err != nil {
		return Machine{Host: host, Tools: "*", Attachments: true, Cleanup: -1, Attach: "*"}, false
	}
	return m, true
}

func (s *Store) Machines() ([]Machine, error) {
	rows, err := s.db.Query(`SELECT ` + machineCols + ` FROM machines ORDER BY seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Machine
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Report はマシンからの報告（見つけた AI・版）を残す。初めてのマシンは init の設定で作る。
func (s *Store) Report(host, version string, det []Detected, init *Machine) error {
	b, _ := json.Marshal(det)
	m := Machine{Tools: "*", Attachments: true, Cleanup: -1, Attach: "*"}
	if init != nil {
		m = *init
	}
	if m.Attach == "" {
		m.Attach = "*"
		if !m.Attachments {
			m.Attach = "-"
		}
	}
	_, err := s.db.Exec(`INSERT INTO machines (host, seen_at, version, detected, tools, attachments, schedule, cleanup, attach) VALUES (?,?,?,?,?,?,?,?,?)
	  ON CONFLICT(host) DO UPDATE SET seen_at=excluded.seen_at, version=excluded.version, detected=excluded.detected`,
		host, time.Now().Unix(), version, string(b), m.Tools, m.Attachments, m.Schedule, m.Cleanup, m.Attach)
	return err
}

// SetMachine はマシンの設定を変える（画面から）。
func (s *Store) SetMachine(m Machine) error {
	_, err := s.db.Exec(`INSERT INTO machines (host, tools, attachments, schedule, cleanup, attach, hide) VALUES (?,?,?,?,?,?,?)
	  ON CONFLICT(host) DO UPDATE SET tools=excluded.tools, attachments=excluded.attachments, schedule=excluded.schedule, cleanup=excluded.cleanup, attach=excluded.attach, hide=excluded.hide`,
		m.Host, m.Tools, m.Attachments, m.Schedule, m.Cleanup, m.Attach, m.Hide)
	return err
}

// DeleteMachine はマシンを一覧から消し、そのマシンのトークンも使えなくする。
func (s *Store) DeleteMachine(host string) error {
	if _, err := s.db.Exec(`DELETE FROM tokens WHERE machine=?`, host); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM machines WHERE host=?`, host)
	return err
}
