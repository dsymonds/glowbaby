package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	dbFlag    = flag.String("db", "baby.db", "`filename` of SQLite3 database file")
	credsFlag = flag.String("creds", filepath.Join(os.Getenv("HOME"), ".glowbabyrc"), "`filename` containing Glow Baby credentials")
)

const domain = "baby.glowing.com"

const usage = `
usage: glowbaby [options] <command>

Commands:
	init			initialise the database file (specified by -db)
	login			log in to Glow Baby (using credentials ~/.glowbabyrc)
	sync			synchronise all data from remote
	plot <type> <dst>	plot data to PNG (type is "sleep")

Options:
`

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "%s", usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	db, err := sql.Open("sqlite3", *dbFlag)
	if err != nil {
		log.Fatalf("Opening DB %s: %v", *dbFlag, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}
	switch cmd := flag.Arg(0); cmd {
	default:
		log.Fatalf("Unknown command %q", cmd)
	case "init":
		// TODO: refuse if the DB file already exists?
		_, err := db.Exec(initDB)
		if err != nil {
			log.Fatalf("Initialising DB: %v", err)
		}
		log.Printf("DB init OK")
	case "login":
		if err := login(context.Background(), db); err != nil {
			log.Fatalf("Logging in: %v", err)
		}
		log.Printf("Logged in OK")
	case "sync":
		start := time.Now()
		if err := sync(context.Background(), db); err != nil {
			log.Fatalf("Syncing data: %v", err)
		}
		log.Printf("Synced data OK in %v", time.Since(start).Truncate(100*time.Millisecond))
	case "plot":
		if flag.NArg() != 3 {
			flag.Usage()
			os.Exit(1)
		}
		typ, dst := flag.Arg(1), flag.Arg(2)
		var data []byte
		switch typ {
		default:
			flag.Usage()
			os.Exit(1)
		case "sleep":
			b, err := plot(context.Background(), db, typ)
			if err != nil {
				log.Fatalf("Plotting data: %v", err)
			}
			data = b
		}
		if err := ioutil.WriteFile(dst, data, 0644); err != nil {
			log.Fatalf("Writing plot to %s: %v", dst, err)
		}
		log.Printf("OK; wrote %q plot to %s (%d bytes)", typ, dst, len(data))
	}
}

const initDB = `
CREATE TABLE Auth (
	Domain TEXT NOT NULL PRIMARY KEY,  -- always "baby.glowing.com"
	Token TEXT NOT NULL
) STRICT;

CREATE TABLE Babies (
	BabyID INTEGER NOT NULL PRIMARY KEY,

	FirstName TEXT NOT NULL,
	LastName TEXT NOT NULL,
	Birthday TEXT NOT NULL,  -- YYYY-MM-DD

	-- Sync status.
	SyncTime INTEGER,
	SyncToken TEXT
) STRICT;

CREATE TABLE BabyData (
	ID INTEGER NOT NULL PRIMARY KEY,
	BabyID INTEGER NOT NULL,

	StartTimestamp INTEGER NOT NULL,
	EndTimestamp INTEGER,

	Key TEXT,

	ValInt INTEGER,
	ValFloat REAL,
	ValStr TEXT
) STRICT;

CREATE TABLE BabyFeedData (
	ID INTEGER NOT NULL PRIMARY KEY,
	BabyID INTEGER NOT NULL,

	StartTimestamp INTEGER NOT NULL,
	EndTimestamp INTEGER,

	FeedType INTEGER,

	BreastUsed TEXT,
	BreastLeft INTEGER,
	BreastRight INTEGER,

	BottleML REAL
) STRICT;
`

func login(ctx context.Context, db *sql.DB) error {
	// Load credentials.
	var creds struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	rawCreds, err := ioutil.ReadFile(*credsFlag)
	if err != nil {
		return fmt.Errorf("loading creds from %s: %w", *credsFlag, err)
	}
	if err := json.Unmarshal(rawCreds, &creds); err != nil {
		return fmt.Errorf("parsing creds from %s: %w", *credsFlag, err)
	}
	// Re-serialise to tidy up, compact, and remove any extraneous keys.
	rawCreds, err = json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("re-marshaling creds: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+domain+"/android/user/sign_in", bytes.NewReader(rawCreds))
	if err != nil {
		return fmt.Errorf("internal error: constructing HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("making HTTP login request: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP login request gave non-200 status %q", resp.Status)
	}
	var loginResp LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("decoding JSON login response: %w", err)
	}

	// Start transaction.
	// Any failures after this point should roll back the transaction.
	txCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	tx, err := db.BeginTx(txCtx, nil)
	if err != nil {
		return fmt.Errorf("starting DB transaction: %w", err)
	}

	user := loginResp.Data.User
	log.Printf("Logging in as %s %s ...", user.FirstName, user.LastName)
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO Auth(Domain, Token) VALUES (?, ?)`, domain, user.AuthToken)
	if err != nil {
		return fmt.Errorf("recording auth info in DB: %w", err)
	}

	for _, babyRec := range loginResp.Data.Babies {
		baby := babyRec.Baby
		log.Printf("Setting up sync info for baby %s %s (baby ID %d) ...", baby.FirstName, baby.LastName, baby.BabyID)

		// Transform birthday format into ISO 8601.
		t, err := time.Parse("2006/01/02", baby.Birthday)
		if err != nil {
			return fmt.Errorf("baby has malformed birthday %q: %w", baby.Birthday, err)
		}
		tStr := t.Format("2006-01-02")

		// TODO: automatic conflict resolution?
		_, err = tx.ExecContext(ctx, `INSERT INTO Babies(BabyID, FirstName, LastName, Birthday) VALUES (?, ?, ?, ?)`,
			baby.BabyID, baby.FirstName, baby.LastName, tStr)
		if err != nil {
			return fmt.Errorf("recording baby sync info in DB: %w", err)
		}
	}

	// Finalise transaction.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing DB transaction: %w", err)
	}

	return nil
}

func sync(ctx context.Context, db *sql.DB) error {
	// Load auth token.
	var authToken string
	row := db.QueryRowContext(ctx, `SELECT Token FROM Auth WHERE Domain = ?`, domain)
	if err := row.Scan(&authToken); err == sql.ErrNoRows {
		return fmt.Errorf("no auth token; have you logged in?")
	} else if err != nil {
		return fmt.Errorf("loading auth token from DB: %w", err)
	}

	// Find all babies to synchronise.
	type babyReq struct {
		BabyID    int64  `json:"baby_id"`
		SyncToken string `json:"sync_token,omitempty"`

		first, last string
	}
	var pullReq struct {
		Data struct {
			Babies []babyReq `json:"babies"`
			User   struct {
				// TODO: anything needed? seems not.
			} `json:"user"`
		} `json:"data"`
	}
	rows, err := db.QueryContext(ctx, `SELECT BabyID, FirstName, LastName, SyncToken FROM Babies`)
	if err != nil {
		return fmt.Errorf("determining list of babies to sync: %w", err)
	}
	for rows.Next() {
		var br babyReq
		var st sql.NullString
		if err := rows.Scan(&br.BabyID, &br.first, &br.last, &st); err != nil {
			return fmt.Errorf("parsing list of babies to sync: %w", err)
		}
		if st.Valid {
			br.SyncToken = st.String
		}
		pullReq.Data.Babies = append(pullReq.Data.Babies, br)
		log.Printf("Going to sync data for baby %s %s (baby ID %d)", br.first, br.last, br.BabyID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("querying list of babies to sync: %w", err)
	}

	rawPullReq, err := json.Marshal(pullReq)
	if err != nil {
		return fmt.Errorf("internal error: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+domain+"/android/user/pull", bytes.NewReader(rawPullReq))
	if err != nil {
		return fmt.Errorf("internal error: constructing HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", authToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("making HTTP pull request: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP pull request gave non-200 status %q", resp.Status)
	}
	var pullResp PullResponse
	if err := json.NewDecoder(resp.Body).Decode(&pullResp); err != nil {
		return fmt.Errorf("decoding JSON pull response: %w", err)
	}

	// Start big transaction.
	// Any failures after this point should roll back the transaction.
	txCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	tx, err := db.BeginTx(txCtx, nil)
	if err != nil {
		return fmt.Errorf("starting DB transaction: %w", err)
	}

	// Update sync token and time.
	for _, baby := range pullResp.Data.Babies {
		_, err = tx.ExecContext(ctx, `UPDATE Babies SET SyncTime = ?, SyncToken = ? WHERE BabyID = ?`,
			baby.SyncTime, baby.SyncToken, baby.BabyID)
		if err != nil {
			return fmt.Errorf("updating baby sync status in DB: %w", err)
		}

		for _, bd := range baby.BabyData.Remove {
			_, err := tx.ExecContext(ctx, `DELETE FROM BabyData WHERE ID = ?`, bd.ID)
			if err != nil {
				return fmt.Errorf("deleting baby data from DB: %w", err)
			}
		}
		if n := len(baby.BabyData.Remove); n > 0 {
			log.Printf("Removed %d old baby data events", n)
		}
		for _, bd := range baby.BabyData.Update {
			_, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO BabyData(ID, BabyID, StartTimestamp, EndTimestamp, Key, ValInt, ValFloat, ValStr)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
				bd.ID, bd.BabyID, bd.StartTimestamp, sqlNullInt64(bd.EndTimestamp), bd.Key, bd.ValInt, bd.ValFloat, bd.ValStr)
			if err != nil {
				return fmt.Errorf("applying baby data update in DB: %w", err)
			}
		}
		log.Printf("Applied %d baby data updates", len(baby.BabyData.Update))

		for _, bd := range baby.BabyFeedData.Remove {
			_, err := tx.ExecContext(ctx, `DELETE FROM BabyFeedData WHERE ID = ?`, bd.ID)
			if err != nil {
				return fmt.Errorf("deleting baby data from DB: %w", err)
			}
		}
		if n := len(baby.BabyFeedData.Remove); n > 0 {
			log.Printf("Removed %d old baby feed data events", n)
		}
		for _, bfd := range baby.BabyFeedData.Update {
			_, err = tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO BabyFeedData(ID, BabyID, StartTimestamp, FeedType, BreastUsed, BreastLeft, BreastRight, BottleML)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
				bfd.ID, bfd.BabyID, bfd.StartTimestamp, bfd.FeedType, bfd.BreastUsed, bfd.BreastLeft, bfd.BreastRight, bfd.BottleML)
			if err != nil {
				return fmt.Errorf("applying baby feed data update in DB: %w", err)
			}
		}
		log.Printf("Applied %d baby feed data updates", len(baby.BabyFeedData.Update))
	}

	// Finalise transaction.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing DB transaction: %w", err)
	}

	return nil
}

func sqlNullInt64(x *int64) (ret sql.NullInt64) {
	if x != nil {
		ret.Int64, ret.Valid = *x, true
	}
	return
}
