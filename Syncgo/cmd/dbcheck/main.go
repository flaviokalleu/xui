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

	fmt.Println("=== ERRORS ===")
	rows, err := db.Query(`SELECT name, stream_url, error_msg FROM xtream_downloads WHERE status='error' ORDER BY updated_at DESC LIMIT 10`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var name, url, errMsg string
		rows.Scan(&name, &url, &errMsg)
		fmt.Printf("[%d] NAME: %s\n    URL:  %s\n    ERR:  %s\n", i+1, name, url, errMsg)
		i++
	}
	if i == 0 {
		fmt.Println("(nenhum erro registrado ainda)")
	}

	fmt.Println("\n=== STATS ===")
	var done, errCount, pending int
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads WHERE status='done'`).Scan(&done)
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads WHERE status='error'`).Scan(&errCount)
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads WHERE status NOT IN ('done','error')`).Scan(&pending)
	fmt.Printf("done=%d  error=%d  other=%d\n", done, errCount, pending)
}
