package main

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

func main() {
	_ = godotenv.Load()

	fmt.Println("=== Syncgo DB Clean ===")
	fmt.Println("Apaga do XUI MySQL: filmes, séries, episódios")
	fmt.Println("Apaga do SQLite: histórico de downloads (para re-baixar tudo)")
	fmt.Println()

	// ── XUI MySQL ──────────────────────────────────────────────────────────────
	// Lê do SQLite primeiro (configurado via /configurar), cai no .env como fallback.
	dbPath := getEnv("DB_PATH", "./syncgo.db")
	sqliteTemp, _ := sql.Open("sqlite", dbPath)
	host := sqliteSetting(sqliteTemp, "xui_host", getEnv("XUI_HOST", ""))
	portStr := sqliteSetting(sqliteTemp, "xui_port", strconv.Itoa(getEnvInt("XUI_PORT", 3306)))
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 3306
	}
	user := sqliteSetting(sqliteTemp, "xui_user", getEnv("XUI_USER", ""))
	pass := sqliteSetting(sqliteTemp, "xui_password", getEnv("XUI_PASSWORD", ""))
	dbName := sqliteSetting(sqliteTemp, "xui_database", getEnv("XUI_DATABASE", "xui"))
	if dbName == "" {
		dbName = "xui"
	}
	sqliteTemp.Close()

	if host == "" {
		fmt.Println("XUI_HOST não configurado — pulando limpeza do MySQL.")
	} else {
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4",
			user, pass, host, port, dbName)
		mysql, err := sql.Open("mysql", dsn)
		if err != nil {
			fmt.Printf("Erro ao abrir MySQL: %v\n", err)
			os.Exit(1)
		}
		defer mysql.Close()
		if err := mysql.Ping(); err != nil {
			fmt.Printf("Erro ao conectar MySQL %s:%d: %v\n", host, port, err)
			os.Exit(1)
		}
		fmt.Printf("Conectado ao MySQL: %s:%d/%s\n\n", host, port, dbName)
		cleanMySQL(mysql)
	}

	// ── SQLite Syncgo ──────────────────────────────────────────────────────────
	sqlite, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Printf("Erro ao abrir SQLite: %v\n", err)
		os.Exit(1)
	}
	defer sqlite.Close()
	fmt.Printf("\nSQLite: %s\n", dbPath)
	cleanSQLite(sqlite)

	fmt.Println("\n✅ Limpeza concluída!")
}

func cleanMySQL(db *sql.DB) {
	// Contagens antes
	var movies, episodes, series int
	db.QueryRow(`SELECT COUNT(*) FROM streams WHERE type = 2`).Scan(&movies)
	db.QueryRow(`SELECT COUNT(*) FROM streams WHERE type = 5`).Scan(&episodes)
	db.QueryRow(`SELECT COUNT(*) FROM streams_series`).Scan(&series)
	fmt.Printf("Antes — Filmes: %d | Episódios: %d | Séries: %d\n", movies, episodes, series)

	steps := []struct {
		desc string
		sql  string
	}{
		{
			"streams_servers (filmes)",
			`DELETE ss FROM streams_servers ss JOIN streams s ON ss.stream_id = s.id WHERE s.type = 2`,
		},
		{
			"streams_servers (episódios)",
			`DELETE ss FROM streams_servers ss JOIN streams s ON ss.stream_id = s.id WHERE s.type = 5`,
		},
		{
			"streams_episodes",
			`DELETE FROM streams_episodes`,
		},
		{
			"streams (filmes type=2)",
			`DELETE FROM streams WHERE type = 2`,
		},
		{
			"streams (episódios type=5)",
			`DELETE FROM streams WHERE type = 5`,
		},
		{
			"streams_series",
			`DELETE FROM streams_series`,
		},
		{
			"bouquets — zerar filmes",
			`UPDATE bouquets SET bouquet_movies = '[]' WHERE bouquet_movies != '[]' AND bouquet_movies != ''`,
		},
		{
			"bouquets — zerar séries",
			`UPDATE bouquets SET bouquet_series = '[]' WHERE bouquet_series != '[]' AND bouquet_series != ''`,
		},
	}

	for _, s := range steps {
		res, err := db.Exec(s.sql)
		if err != nil {
			fmt.Printf("  ⚠ %s: %v\n", s.desc, err)
			continue
		}
		n, _ := res.RowsAffected()
		fmt.Printf("  ✓ %s → %d linhas\n", s.desc, n)
	}
}

func cleanSQLite(db *sql.DB) {
	var before int
	db.QueryRow(`SELECT COUNT(*) FROM xtream_downloads`).Scan(&before)
	fmt.Printf("Antes — xtream_downloads: %d registros\n", before)

	res, err := db.Exec(`DELETE FROM xtream_downloads`)
	if err != nil {
		fmt.Printf("  ⚠ xtream_downloads: %v\n", err)
		return
	}
	n, _ := res.RowsAffected()
	fmt.Printf("  ✓ xtream_downloads → %d removidos\n", n)
}

func sqliteSetting(db *sql.DB, key, def string) string {
	if db == nil {
		return def
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err == nil && v != "" {
		return v
	}
	return def
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
