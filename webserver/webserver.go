package webserver

import (
  "gwen/treerings/scanning"

  "github.com/google/uuid"

	"encoding/json"
	"fmt"
	"log"
	"net/http"
	//"sync"
	"io/ioutil"
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
}

type Application struct {
  Groups map[string]SyncGroup `json:"sync_groups"`
  Analyses map[string]scanning.Analysis `json:"-"`
}

func (app *Application) NewGroup() string {
  var group SyncGroup
  group.Id = uuid.NewString()
  group.AnalysisFile = filepath.Join(CacheDir, fmt.Sprintf("%v.json", group.Id))
  app.Groups[group.Id] = group
  return group.Id
}

func Serve() error {
  app := Application{
    Groups: make(map[string]SyncGroup),
    Analyses: make(map[string]scanning.Analysis),
  }

	http.HandleFunc("GET /api/groups", func(w http.ResponseWriter, r *http.Request) {
		data, err := json.Marshal(app.Groups)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to marshal response: %q", err), 500)
			return
		}
		w.Write(data)
	})

  http.HandleFunc("POST /api/group", func(w http.ResponseWriter, r *http.Request) {
    groupId := app.NewGroup()
    group := app.Groups[groupId]
    data, err := json.Marshal(group)
    if err != nil {
      http.Error(w, fmt.Sprintf("failed to marshal: %q", err), 500)
      return
    }
    w.Write(data)
  })

  http.HandleFunc("GET /api/group/{group}", func(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("group")
    group, ok := app.Groups[id]
    if !ok {
      http.Error(w, fmt.Sprintf("no such group %v", id), 404)
      return
    }
    data, err := json.Marshal(group)
    if err != nil {
      http.Error(w, fmt.Sprintf("failed to marshal: %q", err), 500)
      return
    }
    w.Write(data)
  })

  http.HandleFunc("POST /api/group/{group}", func(w http.ResponseWriter, r *http.Request) {
      id := r.PathValue("group")
      group, ok := app.Groups[id]
      if !ok {
        http.Error(w, fmt.Sprintf("no such group %v", id), 404)
        return
      }
      body, err := ioutil.ReadAll(r.Body)
      if err != nil {
        http.Error(w, fmt.Sprintf("error reading request body %v", err), 400)
        return
      }
      var postGroup SyncGroup
      err = json.Unmarshal(body, &postGroup)
      if err != nil {
        http.Error(w, fmt.Sprintf("failed to unmarshal: %q", err), 500)
        return
      }
      group.Name = postGroup.Name
      app.Groups[id] = group
      data, err := json.Marshal(group)
      if err != nil {
        http.Error(w, fmt.Sprintf("failed to marshal: %q", err), 500)
        return
      }
      w.Write(data)
  })

	http.Handle("/", http.FileServer(http.Dir("./www")))

	fmt.Println("serving on port:", Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", Port), nil))

	return nil
}
