package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

var db *sql.DB

const jwtSecret = "tictactoe-kubrick-odyssey-2026-secret-key"

// ─── DB Setup ───

func initDB() {
	var err error
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL not set")
	}
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(5)

	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password TEXT NOT NULL,
		points INTEGER DEFAULT 100,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS games (
		id TEXT PRIMARY KEY,
		board TEXT DEFAULT '["","","","","","","","",""]',
		turn TEXT DEFAULT 'X',
		player_x TEXT,
		player_o TEXT,
		status TEXT DEFAULT 'waiting',
		winner TEXT DEFAULT '',
		points_won INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS game_players (
		game_id TEXT,
		user_id INTEGER
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS moves (
		id SERIAL PRIMARY KEY,
		game_id TEXT,
		position INTEGER,
		symbol TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_games_status ON games(status)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_gp_user ON game_players(user_id)`)

	var cnt int
	db.QueryRow("SELECT COUNT(*) FROM users").Scan(&cnt)
	log.Printf("Users in DB: %d", cnt)
}

// ─── DB Helpers ───

func createUser(email, pass string) (int64, error) {
	h, _ := bcrypt.GenerateFromPassword([]byte(pass), 10)
	var id int64
	err := db.QueryRow("INSERT INTO users (email, password) VALUES ($1, $2) RETURNING id", email, string(h)).Scan(&id)
	if err != nil {
		log.Printf("createUser error: %v", err)
		return 0, err
	}
	return id, nil
}

func authUser(email, pass string) (*User, error) {
	u := &User{}
	err := db.QueryRow("SELECT id, email, password, points FROM users WHERE email=$1", email).
		Scan(&u.ID, &u.Email, &u.Password, &u.Points)
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(pass)) != nil {
		return nil, fmt.Errorf("bad password")
	}
	return u, nil
}

func getUserByID(id int64) (*User, error) {
	u := &User{}
	err := db.QueryRow("SELECT id, email, points FROM users WHERE id=$1", id).
		Scan(&u.ID, &u.Email, &u.Points)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func getUserByEmail(email string) (*User, error) {
	u := &User{}
	err := db.QueryRow("SELECT id, email, points FROM users WHERE email=$1", email).
		Scan(&u.ID, &u.Email, &u.Points)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func addPoints(id int64, pts int) {
	db.Exec("UPDATE users SET points=GREATEST(0, points+$1) WHERE id=$2", pts, id)
}

func createGame() string {
	b := make([]byte, 8)
	rand.Read(b)
	id := hex.EncodeToString(b)
	db.Exec("INSERT INTO games (id) VALUES ($1)", id)
	return id
}

func getGame(id string) (*GameState, error) {
	g := &GameState{ID: id}
	var board, px, po, st, wi string
	var pw int
	err := db.QueryRow(
		"SELECT COALESCE(board,'[]'), COALESCE(turn,'X'), COALESCE(player_x,''), COALESCE(player_o,''), COALESCE(status,'waiting'), COALESCE(winner,''), COALESCE(points_won,0) FROM games WHERE id=$1", id,
	).Scan(&board, &g.Turn, &px, &po, &st, &wi, &pw)
	if err != nil {
		return nil, err
	}
	g.PlayerX = px
	g.PlayerO = po
	g.Status = st
	g.Winner = wi
	g.Points = pw
	json.Unmarshal([]byte(board), &g.Board)
	db.QueryRow("SELECT COUNT(*) FROM games WHERE status='finished'").Scan(&g.TotalGames)
	db.QueryRow("SELECT COUNT(*) FROM games WHERE status='finished' AND winner='X'").Scan(&g.WinsX)
	db.QueryRow("SELECT COUNT(*) FROM games WHERE status='finished' AND winner='O'").Scan(&g.WinsO)
	db.QueryRow("SELECT COUNT(*) FROM games WHERE status='finished' AND winner=''").Scan(&g.Draws)
	return g, nil
}

func updateGame(g *GameState) {
	b, _ := json.Marshal(g.Board)
	db.Exec("UPDATE games SET board=$1, turn=$2, status=$3, winner=$4, points_won=$5 WHERE id=$6",
		string(b), g.Turn, g.Status, g.Winner, g.Points, g.ID)
}

func setGamePlayers(gid, px, po string) {
	if px != "" {
		db.Exec("UPDATE games SET player_x=$1 WHERE id=$2", px, gid)
	}
	if po != "" {
		db.Exec("UPDATE games SET player_o=$1 WHERE id=$2", po, gid)
	}
}

func recordMove(gid, pos, sym string) {
	_, err := db.Exec("INSERT INTO moves (game_id, position, symbol) VALUES ($1, $2, $3)", gid, pos, sym)
	if err != nil {
		log.Printf("recordMove error: %v", err)
	}
}

func getMoveCount(gid string) int {
	var c int
	err := db.QueryRow("SELECT COUNT(*) FROM moves WHERE game_id=$1", gid).Scan(&c)
	if err != nil {
		log.Printf("getMoveCount error: %v", err)
	}
	log.Printf("getMoveCount game=%s count=%d", gid, c)
	return c
}

func hasActiveGame(uid int64) bool {
	var c int
	db.QueryRow(`SELECT COUNT(*) FROM game_players gp
		JOIN games g ON gp.game_id=g.id
		WHERE gp.user_id=$1 AND g.status IN ('waiting','active')`, uid).Scan(&c)
	return c > 0
}

func getActiveGameID(uid int64) string {
	var gid string
	err := db.QueryRow(`SELECT g.id FROM game_players gp
		JOIN games g ON gp.game_id=g.id
		WHERE gp.user_id=$1 AND g.status IN ('waiting','active')
		ORDER BY g.created_at DESC LIMIT 1`, uid).Scan(&gid)
	if err != nil {
		return ""
	}
	return gid
}

func getWaitingGame() string {
	var gid string
	err := db.QueryRow("SELECT id FROM games WHERE status='waiting' ORDER BY created_at ASC LIMIT 1").Scan(&gid)
	if err != nil {
		return ""
	}
	return gid
}

func joinGame(gid, uid string) {
	db.Exec("INSERT INTO game_players (game_id, user_id) VALUES ($1, $2)", gid, uid)
}

func cleanQueue() {
	db.Exec(`DELETE FROM games WHERE status='waiting' AND created_at < NOW() - INTERVAL '5 minutes'`)
}

// ─── Models ───

type User struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Password string `json:"-"`
	Points   int    `json:"points"`
}

type GameState struct {
	ID         string   `json:"id"`
	Board      []string `json:"board"`
	Turn       string   `json:"turn"`
	PlayerX    string   `json:"player_x"`
	PlayerO    string   `json:"player_o"`
	Status     string   `json:"status"`
	Winner     string   `json:"winner"`
	Points     int      `json:"points"`
	TotalGames int      `json:"total_games"`
	WinsX      int      `json:"wins_x"`
	WinsO      int      `json:"wins_o"`
	Draws      int      `json:"draws"`
}

type Claims struct {
	UserID int64
	Email  string
}

// ─── JWT ───

func makeToken(c Claims) string {
	payload := fmt.Sprintf("%d:%s:%d", c.UserID, c.Email, time.Now().Add(72*time.Hour).Unix())
	sig := hexEncode([]byte(payload + jwtSecret))
	return hexEncode([]byte(payload)) + "." + sig
}

func parseToken(tok string) *Claims {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return nil
	}
	pl, err := hexDecode(parts[0])
	if err != nil {
		return nil
	}
	sig := hexEncode(append(pl, []byte(jwtSecret)...))
	if sig != parts[1] {
		return nil
	}
	sp := strings.SplitN(string(pl), ":", 3)
	if len(sp) != 3 {
		return nil
	}
	var uid int64
	fmt.Sscanf(sp[0], "%d", &uid)
	return &Claims{UserID: uid, Email: sp[1]}
}

func hexEncode(d []byte) string { return hex.EncodeToString(d) }
func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }

// ─── Middleware ───

func withAuth(fn func(http.ResponseWriter, *http.Request, *Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			jsonErr(w, "unauthorized", 401)
			return
		}
		c := parseToken(strings.TrimPrefix(h, "Bearer "))
		if c == nil {
			jsonErr(w, "bad token", 401)
			return
		}
		fn(w, r, c)
	}
}

// ─── HTTP Helpers ───

func jsonResp(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── Handlers ───

func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonErr(w, "method not allowed", 405)
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Email == "" || body.Password == "" {
		jsonErr(w, "никнейм и пароль обязательны", 400)
		return
	}
	if len(body.Password) < 4 {
		jsonErr(w, "пароль слишком короткий", 400)
		return
	}
	id, err := createUser(body.Email, body.Password)
	if err != nil {
		log.Printf("Register error: %v", err)
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "UNIQUE") {
			jsonErr(w, "этот никнейм уже занят", 409)
		} else {
			jsonErr(w, "server error", 500)
		}
		return
	}
	tok := makeToken(Claims{id, body.Email})
	jsonResp(w, map[string]interface{}{
		"token":  tok,
		"user":   map[string]interface{}{"id": id, "email": body.Email, "points": 100},
		"points": 100,
	})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonErr(w, "method not allowed", 405)
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Email == "" || body.Password == "" {
		jsonErr(w, "никнейм и пароль обязательны", 400)
		return
	}
	u, err := authUser(body.Email, body.Password)
	if err != nil {
		jsonErr(w, "неверный никнейм или пароль", 401)
		return
	}
	tok := makeToken(Claims{u.ID, u.Email})
	jsonResp(w, map[string]interface{}{
		"token":  tok,
		"user":   map[string]interface{}{"id": u.ID, "email": u.Email, "points": u.Points},
		"points": u.Points,
	})
}

func handleMe(w http.ResponseWriter, r *http.Request, c *Claims) {
	u, err := getUserByID(c.UserID)
	if err != nil {
		jsonErr(w, "not found", 404)
		return
	}
	jsonResp(w, map[string]interface{}{
		"user":   map[string]interface{}{"id": u.ID, "email": u.Email, "points": u.Points},
		"points": u.Points,
	})
}

func handleCreateGame(w http.ResponseWriter, r *http.Request, c *Claims) {
	if r.Method != "POST" {
		jsonErr(w, "method not allowed", 405)
		return
	}
	cleanQueue()
	log.Printf("Create game request by %s (id=%d)", c.Email, c.UserID)

	gid := getActiveGameID(c.UserID)
	if gid != "" {
		log.Printf("Player %s already in game %s", c.Email, gid)
		g, err := getGame(gid)
		if err == nil && g.Status == "finished" {
			log.Printf("Old game %s finished, allowing new game", gid)
		} else {
			jsonResp(w, map[string]interface{}{"game_id": gid, "status": "joined"})
			return
		}
	}

	waiting := getWaitingGame()
	if waiting != "" {
		log.Printf("Found waiting game %s for player %s", waiting, c.Email)
		g := &GameState{ID: waiting, Board: make([]string, 9), Status: "active"}
		var firstPlayer string
		db.QueryRow("SELECT player_x FROM games WHERE id=$1", waiting).Scan(&firstPlayer)
		n, _ := rand.Int(rand.Reader, big.NewInt(2))
		if n.Int64() == 0 {
			g.PlayerX = firstPlayer
			g.PlayerO = c.Email
		} else {
			g.PlayerX = c.Email
			g.PlayerO = firstPlayer
		}
		g.Turn = "X"
		g.Board = make([]string, 9)
		updateGame(g)
		setGamePlayers(waiting, g.PlayerX, g.PlayerO)
		joinGame(waiting, fmt.Sprintf("%d", c.UserID))
		jsonResp(w, map[string]interface{}{"game_id": waiting, "status": "started"})
		return
	}

	gid = createGame()
	db.Exec("UPDATE games SET player_x=$1 WHERE id=$2", c.Email, gid)
	joinGame(gid, fmt.Sprintf("%d", c.UserID))
	log.Printf("Game created: %s by %s", gid, c.Email)
	jsonResp(w, map[string]interface{}{"game_id": gid, "status": "waiting"})
}

func handleJoinGame(w http.ResponseWriter, r *http.Request, c *Claims) {
	gid := r.URL.Query().Get("id")
	if gid == "" {
		jsonErr(w, "game id required", 400)
		return
	}
	log.Printf("Game state request: %s by %s", gid, c.Email)
	g, err := getGame(gid)
	if err != nil {
		log.Printf("Game not found: %s error: %v", gid, err)
		jsonErr(w, "game not found", 404)
		return
	}
	log.Printf("Game %s: player_x=%s player_o=%s request_by=%s", gid, g.PlayerX, g.PlayerO, c.Email)
	if g.PlayerX != c.Email && g.PlayerO != c.Email {
		jsonErr(w, "not your game", 403)
		return
	}
	jsonResp(w, g)
}

func handleMove(w http.ResponseWriter, r *http.Request, c *Claims) {
	if r.Method != "POST" {
		jsonErr(w, "method not allowed", 405)
		return
	}
	var body struct {
		GameID string `json:"game_id"`
		Pos    int    `json:"pos"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		jsonErr(w, "bad json", 400)
		return
	}
	g, err := getGame(body.GameID)
	if err != nil {
		jsonErr(w, "game not found", 404)
		return
	}
	if g.Status == "finished" {
		jsonErr(w, "game over", 400)
		return
	}
	if g.Status == "waiting" {
		jsonErr(w, "waiting for opponent", 400)
		return
	}

_email := c.Email
	var sym string
	if g.PlayerX == _email {
		sym = "X"
	} else if g.PlayerO == _email {
		sym = "O"
	} else {
		jsonErr(w, "not your game", 403)
		return
	}
	if g.Turn != sym {
		jsonErr(w, "not your turn", 400)
		return
	}
	if body.Pos < 0 || body.Pos > 8 || g.Board[body.Pos] != "" {
		jsonErr(w, "invalid move", 400)
		return
	}

	g.Board[body.Pos] = sym
	recordMove(body.GameID, fmt.Sprintf("%d", body.Pos), sym)

	lines := [][3]int{{0,1,2},{3,4,5},{6,7,8},{0,3,6},{1,4,7},{2,5,8},{0,4,8},{2,4,6}}
	for _, l := range lines {
		if g.Board[l[0]] != "" && g.Board[l[0]] == g.Board[l[1]] && g.Board[l[1]] == g.Board[l[2]] {
			g.Winner = sym
			g.Status = "finished"
			g.Points = 1
			if sym == "X" && g.PlayerX != "" {
				if u, e := getUserByEmail(g.PlayerX); e == nil { addPoints(u.ID, g.Points) }
			} else if sym == "O" && g.PlayerO != "" {
				if u, e := getUserByEmail(g.PlayerO); e == nil { addPoints(u.ID, g.Points) }
			}
			loser := "O"
			if sym == "O" { loser = "X" }
			if loser == "X" && g.PlayerX != "" {
				if u, e := getUserByEmail(g.PlayerX); e == nil { addPoints(u.ID, -1) }
			} else if loser == "O" && g.PlayerO != "" {
				if u, e := getUserByEmail(g.PlayerO); e == nil { addPoints(u.ID, -1) }
			}
		}
	}

	if g.Status != "finished" {
		full := true
		for _, v := range g.Board {
			if v == "" {
				full = false
				break
			}
		}
		if full {
			g.Status = "finished"
		} else {
			if sym == "X" {
				g.Turn = "O"
			} else {
				g.Turn = "X"
			}
		}
	}

	updateGame(g)
	jsonResp(w, g)
}

func handleQueue(w http.ResponseWriter, r *http.Request, c *Claims) {
	if r.Method != "GET" {
		jsonErr(w, "method not allowed", 405)
		return
	}
	cleanQueue()
	gid := getActiveGameID(c.UserID)
	if gid != "" {
		g, _ := getGame(gid)
		if g != nil && g.Status == "active" {
			jsonResp(w, map[string]interface{}{"status": "found", "game_id": gid, "game": g})
			return
		}
	}
	jsonResp(w, map[string]interface{}{"status": "waiting"})
}

func handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT email, points FROM users ORDER BY points DESC LIMIT 50")
	if err != nil {
		jsonErr(w, "db error", 500)
		return
	}
	defer rows.Close()
	type entry struct {
		Email  string `json:"email"`
		Points int    `json:"points"`
	}
	var list []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Email, &e.Points); err != nil {
			continue
		}
		list = append(list, e)
	}
	if list == nil {
		list = []entry{}
	}
	jsonResp(w, map[string]interface{}{
		"leaderboard": list,
	})
}

// ─── Main ───

func main() {
	initDB()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile("index.html")
		if err != nil {
			http.Error(w, "index.html not found", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		w.Write(data)
	})

	http.HandleFunc("/api/register", handleRegister)
	http.HandleFunc("/api/login", handleLogin)
	http.HandleFunc("/api/me", withAuth(handleMe))
	http.HandleFunc("/api/game/create", withAuth(handleCreateGame))
	http.HandleFunc("/api/game/join", withAuth(handleJoinGame))
	http.HandleFunc("/api/game/move", withAuth(handleMove))
	http.HandleFunc("/api/game/state", withAuth(handleJoinGame))
	http.HandleFunc("/api/queue", withAuth(handleQueue))
	http.HandleFunc("/api/leaderboard", handleLeaderboard)

	fmt.Printf("Server running on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func init() {
	_ = math.MaxInt64
}
