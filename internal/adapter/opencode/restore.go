package opencode

import (
	"fmt"
	"strings"
)

// Restore は原本の行を OpenCode の DB へ書き戻す。project は無ければ足し、
// session・message・part は同じ ID の行を上書きする。REPLACE は行を消してから入れ、
// 外部キーの ON DELETE CASCADE で子の行まで消えうるので、UPSERT で書く。DB 側に無い列は捨て、
// 原本に無い列は DB の既定値にする（OpenCode の版が少し違っても入る）。
func Restore(path string, r *Rows) error {
	d, err := open(path, "rw")
	if err != nil {
		return err
	}
	defer d.Close()
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	put := func(table string, upsert bool, row Row) error {
		cols, err := columns(d, table)
		if err != nil {
			return err
		}
		var names []string
		var args []any
		for _, c := range cols {
			if v, ok := row[c]; ok {
				names = append(names, "`"+c+"`")
				args = append(args, v)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("%s: 書ける列が無い", table)
		}
		q := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s) ON CONFLICT(`id`) DO ", table, strings.Join(names, ","),
			strings.TrimSuffix(strings.Repeat("?,", len(names)), ","))
		if upsert {
			var set []string
			for _, n := range names {
				set = append(set, n+"=excluded."+n)
			}
			q += "UPDATE SET " + strings.Join(set, ",")
		} else {
			q += "NOTHING"
		}
		_, err = tx.Exec(q, args...)
		return err
	}
	if r.Project != nil {
		if err := put("project", false, r.Project); err != nil {
			return err
		}
	}
	if err := put("session", true, r.Session); err != nil {
		return err
	}
	for _, m := range r.Messages {
		if err := put("message", true, m); err != nil {
			return err
		}
	}
	for _, p := range r.Parts {
		if err := put("part", true, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func columns(d *DB, table string) ([]string, error) {
	rows, err := d.db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
