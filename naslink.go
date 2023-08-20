package main

import (
    "fmt"
    "log"
    "crypto/sha256"
    "encoding/hex"
	"os"
    "path/filepath"
	"net/http"
    "database/sql"
	"strings"
    "github.com/google/uuid"
    _ "github.com/glebarez/go-sqlite"

)
 
var (
	handlers = make(map[string]func(w http.ResponseWriter, r *http.Request))
)

func naslinkHandlers (db *sql.DB) {
    rows, err := db.Query("SELECT filePath, uuid, hash FROM naslinks;"); 
    if err != nil {
        log.Fatal(err)
    }

    for rows.Next() {
        var path string
        var id string
        var hash string
        if err = rows.Scan(&path, &id, &hash); err != nil {
            log.Fatal(err)
        }
        
        if integrityCheck(hash, path) == true {
            addHandler(id, fileHandler(path), path)
        } else {
            log.Panicf("File %s changed since creation. Please regenerate naslink for that file", path)
        }
    }
    if err = rows.Err(); err != nil {
        log.Fatal(err) 
    }
}

func start (db *sql.DB, options []string){
	LISTEN_HOST := "0.0.0.0"
    LISTEN_PORT := "8080"

    switch len(options) {
        case 1:
            LISTEN_PORT = options[0]
        case 2:
            LISTEN_HOST = options[0]
            LISTEN_PORT = options[1]
    }

    naslinkHandlers(db)
    
    log.Fatal(http.ListenAndServe(LISTEN_HOST+":"+LISTEN_PORT, &trash{}))
}

func createLink(db *sql.DB, path string) {
    hash := hashFile(path)
    id := uuid.New().String()

    path,err := filepath.Abs(path)
    if err != nil {
        log.Fatal(err)
    }

    rows, err := db.Query("SELECT uuid FROM naslinks WHERE filePath = (?);", path)
    if err != nil {
        log.Fatal(err)
    }
    for rows.Next() {
        if err = rows.Scan(&id); err != nil {
            log.Fatal(err)
        }
        log.Printf("File %s already has a naslink: %s", path, id)
        return
    }

    if _, err := db.Exec("INSERT INTO naslinks (filePath, uuid, hash) VALUES (?, ?, ?);", path, id, hash); err != nil {
        log.Fatal(err)
    }
    addHandler(id, fileHandler(path), path)
}

func deleteLink(db *sql.DB, path string) {
    path,err := filepath.Abs(path)
    if err != nil {
        log.Fatal(err)
    }

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

    _, err = db.Exec("DELETE FROM naslinks WHERE filePath=(?);", path)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Removed %s: %s\n", path, id)
}

func showAll(db *sql.DB) {
    rows, err := db.Query("SELECT uuid, filePath FROM naslinks;")
    if err != nil {
        log.Fatal(err)
    }
    for rows.Next() {
        var id string
        var path string
        if err = rows.Scan(&id, &path); err != nil {
            log.Fatal(err)
        }
        fmt.Printf("%s: %s\n", id, path)
    }
}

func hashFile(path string) (string) {
    data, err := os.ReadFile(path)
    if err != nil {
        log.Fatal(err)
    }

    hash_raw := sha256.Sum256(data)
    hash := hex.EncodeToString(hash_raw[:])

    return hash
}

func integrityCheck (orig_hash string, path string) (bool) {
    cur_hash := hashFile(path)
    if cur_hash == orig_hash {
        return true
    } else {
        return false
    }
}

type trash struct {
}

func (t *trash) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // reload naslinks
	path := strings.TrimLeft(r.URL.Path, "/")
	path = strings.TrimRight(path, "/")
	log.Print(r.Method, ": ", path, ", UA: ", r.UserAgent(), ", IP: ", r.RemoteAddr, " NGINX: ", r.Header["X-Forwarded-For"])
	if h, ok := handlers[path]; ok {
		h(w, r)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

func addHandler(path string, f func(w http.ResponseWriter, r *http.Request), filePath string) {
	path = strings.TrimRight(path, "/")
	if _, ok := handlers[path]; !ok {
        log.Print("Adding naslink for ", filePath, ":", path)
	fmt.Println()
		handlers[path] = f
	} else {
		log.Print("Not adding path: ", path, " (handler already exists)")
	}
}

func fileHandler(name string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Hello", "are you finding this site?")
		http.ServeFile(w, r, name)
	}
}

func printUsage() {
            fmt.Println("Usage: naslink <command> [options]")
            fmt.Println("\t serve [host] [port]")
            fmt.Println("\t\t - start serving the naslinks over http (default 0.0.0.0 over port 8080)")
            fmt.Println("\t add [filepath1 filepath2 ... filepathN]")
            fmt.Println("\t\t - adds the specified files to serve as a naslink")
            fmt.Println("\t delete [filepath1 filepath2 ... filepathN]")
            fmt.Println("\t\t - removes the naslink for the file specified")
            fmt.Println("\t list")
            fmt.Println("\t\t - show all of the existing naslinks")
}


func main() {
    db, err := sql.Open("sqlite", "/opt/naslink/naslink.db")
    if err != nil {
        log.Fatal(err)
    }

    if _, err = db.Exec("CREATE TABLE IF NOT EXISTS naslinks (id INTEGER PRIMARY KEY, filePath VARCHAR(128), uuid VARCHAR(64), hash VARCHAR(64));"); err != nil {
        log.Fatal(err)
    }

    args := os.Args
    if len(os.Args) < 2 {
        printUsage()
        return
    }

    switch args[1] {
        case "serve":
			start(db, args[2:])
        case "list":
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
        default:
            printUsage()
    }
} 
