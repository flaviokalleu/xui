package main

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "syncgo.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE xtream_downloads SET status='pending', error_msg='' WHERE status='error'`)
	if err != nil {
		panic(err)
	}
	n, _ := res.RowsAffected()
	fmt.Printf("Resetados %d itens de 'error' → 'pending'\n", n)

	var done, pending, other int
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads WHERE status='done'`).Scan(&done)
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads WHERE status='pending'`).Scan(&pending)
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads WHERE status NOT IN ('done','error','pending')`).Scan(&other)
	fmt.Printf("done=%d  pending=%d  other=%d\n", done, pending, other)
}
