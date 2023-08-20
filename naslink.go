package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/glebarez/go-sqlite"
	"github.com/google/uuid"
)

const (
	MEGABYTE = 1024 * 1024
	WEB_HEAD = `
	<!DOCTYPE html>
	<html>
	<head>
	   <meta http-equiv="Content-Type" content="text/html" charset="UTF-8"/>
       <meta name="viewport" content="width=device-width, initial-scale=1.0">
	</head>
	<body>
	    <style>
	    body {
		    font-family: sans-serif;
		    background-color: #212121;
		    color: white;
		    text-align: center;
		    margin: 2rem;
	    }
	    span {
		    color: red;
	    }
	    img {
		    display: inline;
		    width: 300px;
	    }
	    </style>

		<img src="logo.png"/>
		`
	INDEX = `
	    <p>Welcome to naslink!</p>
	`
	INVALID = `
	    <p>You've Been NasLinked!!</p>
	    <p><span><b>Requested file was changed or removed.</b></span></p>
    `
	WEB_FOOT = `
	</body>
	</html>
	`
)

var (
	handlers = make(map[string]func(w http.ResponseWriter, r *http.Request))
	basePath string
)

type NasLink struct {
	DB *sql.DB
}

func getNasLink(db *sql.DB, id string) (bool, string, string, int64) {
	var path, hash string
	var size int64

	rows, err := db.Query("SELECT filePath, hash, size FROM naslinks WHERE uuid=(?)", id)
	if err != nil {
		log.Print(err)
		return false, path, hash, size
	}

	for rows.Next() {
		if err = rows.Scan(&path, &hash, &size); err != nil {
			log.Print(err)
			return false, path, hash, size
		}
	}

	return path != "", path, hash, size
}

func cleanNaslinks(db *sql.DB) {
	rows, err := db.Query("SELECT filePath, hash, size FROM naslinks")
	if err != nil {
		log.Fatal(err)
	}

	for rows.Next() {
		var path, hash string
		var size int64
		if err = rows.Scan(&path, &hash, &size); err != nil {
			log.Fatal(err)
		}
		if !integrityCheck(path, hash, size) {
			log.Printf("File %s failed integrity check:", path, err)
			deleteLink(db, path)
		}
	}
	if err = rows.Err(); err != nil {
		log.Fatal(err)
	}
}

func start(db *sql.DB, options []string) {
	LISTEN_HOST := "0.0.0.0"
	LISTEN_PORT := "8080"

	switch len(options) {
	case 1:
		LISTEN_PORT = options[0]
	case 2:
		LISTEN_HOST = options[0]
		LISTEN_PORT = options[1]
	}

	addr := LISTEN_HOST + ":" + LISTEN_PORT
	log.Println("Serving on", addr)
	log.Fatal(http.ListenAndServe(addr, &NasLink{db}))
}

func createLink(db *sql.DB, path string) {
	path, err := filepath.Abs(path)
	if err != nil {
		log.Fatal(err)
	}

	if id := linkExists(db, path); id != "" {
		log.Printf("File %s already has a naslink: %s; overwriting this record", path, id)
		deleteLink(db, path)
	}

	id := uuid.New().String()
	hash, err := hashFile(path)
	if err != nil {
		log.Fatal(err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		log.Fatal(err)
	}

	if _, err := db.Exec("INSERT INTO naslinks (filePath, uuid, hash, size) VALUES (?, ?, ?, ?);", path, id, hash, fileInfo.Size()); err != nil {
		log.Fatal(err)
	}
	log.Printf("File %s naslink: %s", path, id)
}

func linkExists(db *sql.DB, path string) string {
	rows, err := db.Query("SELECT uuid FROM naslinks WHERE filePath = (?);", path)
	if err != nil {
		log.Fatal(err)
	}

	var id string
	for rows.Next() {
		if err = rows.Scan(&id); err != nil {
			log.Fatal(err)
		}
	}
	return id
}

func deleteLink(db *sql.DB, path string) {
	path, err := filepath.Abs(path)
	if err != nil {
		log.Fatal(err)
	}
	if id := linkExists(db, path); id != "" {
		_, err := db.Exec("DELETE FROM naslinks WHERE uuid=(?)", id)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Removed %s: %s\n", path, id)
	} else {
		log.Printf("%s (%s) does not have a naslink\n", path, id)
	}
}

func showAll(db *sql.DB) {
	rows, err := db.Query("SELECT uuid, filePath FROM naslinks")
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var id string
		var path string
		if err = rows.Scan(&id, &path); err != nil {
			log.Fatal(err)
		}
		log.Printf("%s: %s\n", id, path)
	}
}

// hashFile will hash the entire file if it's under 16 MB in size, otherwise,
// will hash a MB from the start, middle, and end.
func hashFile(path string) (string, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	var data []byte
	fileSize := fileInfo.Size()

	if fileSize > 16*MEGABYTE {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}

		startChunk := make([]byte, MEGABYTE)
		n, err := file.ReadAt(startChunk, 0)
		if err != nil || n != MEGABYTE {
			return "", err
		}
		data = append(data, startChunk...)

		middleChunk := make([]byte, MEGABYTE)
		n, err = file.ReadAt(middleChunk, fileSize/2)
		if err != nil || n != MEGABYTE {
			return "", err
		}
		data = append(data, middleChunk...)

		endChunk := make([]byte, MEGABYTE)
		n, err = file.ReadAt(endChunk, fileSize-MEGABYTE)
		if err != nil || n != MEGABYTE {
			return "", err
		}
		data = append(data, endChunk...)

	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
	}

	hash_raw := sha256.Sum256(data)
	hash := hex.EncodeToString(hash_raw[:])
	return hash, nil
}

func integrityCheck(path, hash string, size int64) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	if fileInfo.Size() != size {
		return false
	}

	currentHash, err := hashFile(path)
	if err != nil {
		return false
	}
	return currentHash == hash
}

func (n *NasLink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// reload naslinks
	id := strings.TrimLeft(r.URL.Path, "/")
	id = strings.TrimRight(id, "/")
	id = strings.TrimSpace(id)

	if id == "favicon.ico" || id == "logo.png" {
		http.ServeFile(w, r, basePath+"images/"+id)
		return
	}

	if id == "" {
		fmt.Fprint(w, WEB_HEAD+INDEX+WEB_FOOT)
		return
	}

	if valid, path, hash, size := getNasLink(n.DB, id); valid {
		if integrityCheck(path, hash, size) {
			log.Print("Retrieving NasLink file: ", r.Method, ": ", id, ", UA: ", r.UserAgent(), ", IP: ", r.RemoteAddr, " NGINX: ", r.Header["X-Forwarded-For"])
			w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(path))
			http.ServeFile(w, r, path)
			return
		}
		log.Print("Failed integrity check: ", r.Method, ": ", path, ", UA: ", r.UserAgent(), ", IP: ", r.RemoteAddr, " NGINX: ", r.Header["X-Forwarded-For"])
		deleteLink(n.DB, path)
	}

	// 404/failure
	log.Print("404!: ", id, ", UA: ", r.UserAgent(), ", IP: ", r.RemoteAddr, " NGINX: ", r.Header["X-Forwarded-For"])
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, WEB_HEAD+INVALID+WEB_FOOT)
}

func printUsage() {
	fmt.Println("Usage: naslink <command> [options]")
	fmt.Println("  serve [host] [port]")
	fmt.Println("    - start serving the naslinks over http (default 0.0.0.0 over port 8080)")
	fmt.Println("  add [filepath1 filepath2 ... filepathN]")
	fmt.Println("    - adds the specified files to serve as a naslink")
	fmt.Println("  delete [filepath1 filepath2 ... filepathN]")
	fmt.Println("    - removes the naslink for the file specified")
	fmt.Println("  list, ls")
	fmt.Println("    - show all of the existing naslinks")
	fmt.Println("  clean")
	fmt.Println("    - removes all invalid naslinks")
}

func main() {

	execPath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	binPath := filepath.Base(execPath)
	basePath = execPath[:len(execPath)-len(binPath)]
	dbPath := basePath + "naslink.db"

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}

	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS naslinks (id INTEGER PRIMARY KEY, filePath VARCHAR(128), uuid VARCHAR(64), hash VARCHAR(64), size INTEGER);"); err != nil {
		log.Fatal(err)
	}

	args := os.Args
	if len(args) < 2 {
		printUsage()
		return
	}

	switch args[1] {
	case "serve":
		start(db, args[2:])
	case "list", "ls":
		showAll(db)
	case "add":
		if len(args) < 3 {
			printUsage()
			return
		}
		for _, path := range args[2:] {
			createLink(db, path)
		}
	case "delete":
		if len(args) < 3 {
			printUsage()
			return
		}
		for _, path := range args[2:] {
			deleteLink(db, path)
		}
	case "clean":
		cleanNaslinks(db)
	default:
		printUsage()
	}
}
