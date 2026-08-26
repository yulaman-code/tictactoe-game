# Tic-Tac-Toe

An online tic-tac-toe game built in Go: play offline against a friend, against an AI, or online against another player over the internet — with accounts, a global rating, and switchable visual themes.


**Live demo:** https://tictactoe-game-production-24dd.up.railway.app


![Game](docs/screenshot.png)


## Features


- **Three game modes:**
  - **Offline** — two players sharing one screen
  - **Vs computer** — an AI opponent (see below)
  - **Online** — real-time play against another player, with matchmaking
- **AI opponent** powered by the **minimax algorithm with alpha-beta pruning**, plus a deliberate 25% chance of a random mistake so it stays beatable and fun. It also alternates who moves first between games.
- **Accounts** — registration and login, passwords hashed with bcrypt
- **Global rating** — a running score per player (starts at 100; win +1, loss -1, forfeit +5/-5)
- **Multiple themes** — Space Odyssey, Cyberpunk, Dune, and classic


## Tech stack


- **Language:** Go (standard library net/http)
- **Database:** PostgreSQL (lib/pq)
- **Auth:** bcrypt password hashing
- **Frontend:** a single index.html with vanilla JavaScript, no frameworks
- **Online play:** HTTP polling — the client requests game state once per second; the server keeps state in PostgreSQL. Simple to build, and for a turn-based game a one-second delay is fine (WebSockets would suit something needing instant updates).
- **Hosting:** Railway


## Running locally


Requires Go 1.22+ and a PostgreSQL database.


    git clone https://github.com/yulaman-code/tictactoe-game.git
    cd tictactoe-game
    export DATABASE_URL="postgres://user:password@localhost:5432/dbname?sslmode=disable"
    go run .

Then open http://localhost:8080.
