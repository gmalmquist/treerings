package webserver

import (
  "gwen/treerings/scanning"

  "github.com/google/uuid"

	"encoding/json"
	"fmt"
	"log"
	"net/http"
	//"sync"
	"path/filepath"
  "strconv"
  "net"
  "time"
)

var CacheDir string = ".treeringscache"
var Port int

func isPortBound(port int) bool {
  timeout := time.Second / 2
  conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)), timeout)
  if conn != nil {
    conn.Close()
  }
  return err == nil
}

type SyncGroup struct {
  Id string `json:"id"`
  Name string `json:"name"`
	Roots    []string `json:"root"`
  AnalysisFile string `json:"analysis_file"`
  Analysis scanning.Analysis `json:"-"`
}

type Application struct {
  Groups map[string]SyncGroup `json:"sync_groups"`
}

func (app *Application) NewGroup() SyncGroup {
  var group SyncGroup
  group.Id = uuid.NewString()
  group.AnalysisFile = filepath.Join(CacheDir, fmt.Sprintf("%v.json", group.Id))
  return group
}

func Serve() error {
	http.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
    foo := struct{
      Hi string `json:"hi"`
    }{
      Hi: "hi",
    }
		data, err := json.Marshal(foo)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to marshal response: %q", err), 500)
			return
		}
		w.Write(data)
	})

	http.Handle("/", http.FileServer(http.Dir("./www")))

	fmt.Println("serving on port:", Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", Port), nil))

	return nil
}
