package webserver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"sync"
  "strconv"
  "syscall"
  "strings"
  "net"
  "time"
)

type Service struct {
	Name        string   `json:"name"`
	Ports       []int    `json:"ports"`
	Command     []string `json:"command"`
	User        string   `json:"user"`
	Root        string   `json:"root"`
	Daemon      bool     `json:"daemon"`
	StopCommand []string `json:"stop"`
  RestartInterval time.Duration `json:"restart_interval"`
}

type App struct {
	Name     string    `json:"name"`
	Services []Service `json:"services"`
	Root     string    `json:"root"`
}

type Config struct {
	Apps []App `json:"apps"`
	Port int   `json:"port"`
}

type ServiceState struct {
	Conf         Service  `json:"conf"`
	StdoutBuffer []string `json:"stdout"`
	StderrBuffer []string `json:"stderr"`
	Running      bool     `json:"running"`
	ExitCode     int      `json:"exitcode"`
}

type AppState struct {
	Name     string                  `json:"name"`
	Services map[string]ServiceState `json:"services"`
}

type State struct {
	Apps map[string]AppState `json:"apps"`
}

type ServiceAction int

const (
	Stop ServiceAction = iota + 1
	Start
)

type ActionChannels struct {
	Apps map[string]map[string]chan ServiceAction
}

type UpdateKind int

const (
	Init UpdateKind = iota + 1
	RunState
	Stdout
	Stderr
)

type ServiceUpdate struct {
	App      string       `json:"app"`
	Service  string       `json:"service"`
	Kind     UpdateKind   `json:"kind"`
	State    ServiceState `json:"state"`
	Running  bool         `json:"running"`
	ExitCode int          `json:"exitcode"`
	Stdout   string       `json:"stdout"`
	Stderr   string       `json:"stderr"`
}

func loadConfig() (Config, error) {
	conf := Config{}

	data, err := ioutil.ReadFile("config.json")
	if err != nil {
		return conf, err
	}

	if err := json.Unmarshal(data, &conf); err != nil {
		return conf, err
	}

	return conf, nil
}

func isPortBound(port int) bool {
  timeout := time.Second / 2
  conn, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", strconv.Itoa(port)), timeout)
  if conn != nil {
    conn.Close()
  }
  return err == nil
}

func startSubprocessFor(
	app string,
	service string,
	daemon bool,
	cmd *exec.Cmd,
	updates chan ServiceUpdate,
) error {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		println(fmt.Sprintf("Could not open stdout pipe for %v.%v: %q", app, service, err))
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		println(fmt.Sprintf("Could not open stderr pipe for %v.%v: %q", app, service, err))
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 80)
		pipe := stdoutPipe
		for {
			read, err := pipe.Read(buf)
			if read > 0 {
				updates <- ServiceUpdate{
					App:     app,
					Service: service,
					Kind:    Stdout,
					Stdout:  string(buf[:read]),
				}
			}
			if err != nil {
        cmd.Wait()
				if !daemon {
					updates <- ServiceUpdate{
						App:      app,
						Service:  service,
						Kind:     RunState,
						Running:  false,
						ExitCode: cmd.ProcessState.ExitCode(),
					}
				}
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 80)
		pipe := stderrPipe
		for {
			read, err := pipe.Read(buf)
			if read > 0 {
				updates <- ServiceUpdate{
					App:     app,
					Service: service,
					Kind:    Stderr,
					Stderr:  string(buf[:read]),
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return nil
}

func userPath(user string) (string, error) {
  cmd := exec.Command(
    "sudo", "su", user, "-c", "echo $PATH",
  )
  out, err := cmd.Output()
  if err != nil {
    return "", err
  }
  return strings.TrimSpace(string(out)), nil
}

func userCommand(user string, command []string) (*exec.Cmd) {
  cmd := exec.Command(command[0], command[1:]...)
  if user != "" {
    var uid int64
    var gid int64
    ruid, err := exec.Command("id", "-u", user).Output()
    if err == nil {
      uid, err = strconv.ParseInt(strings.TrimSpace(string(ruid)), 10, 32)
    }
    if err != nil {
      println(fmt.Sprintf("user (%v) uid (%v) parse error: %q", user, string(ruid), err))
      uid = 0
    }
    rgid, err := exec.Command("id", "-g", user).Output()
    if err == nil {
      gid, err = strconv.ParseInt(strings.TrimSpace(string(rgid)), 10, 32)
    }
    if err != nil {
      println(fmt.Sprintf("user (%v) gid (%v) parse error: %q", user, string(rgid), err))
      gid = 0
    }
    path, err := userPath(user)
    if err == nil {
      cmd.Env = append(cmd.Env, fmt.Sprintf("PATH=%v", path))
    }
    cmd.SysProcAttr = &syscall.SysProcAttr{}
    cmd.SysProcAttr.Credential = &syscall.Credential{
      Uid: uint32(uid),
      Gid: uint32(gid),
    }
  }
  return cmd
}

func startup(conf *Config) (chan ServiceUpdate, ActionChannels, error) {
	channels := ActionChannels{
		Apps: make(map[string]map[string]chan ServiceAction),
	}
	updates := make(chan ServiceUpdate)
	for i := 0; i < len(conf.Apps); i++ {
		app := &conf.Apps[i]
		channels.Apps[app.Name] = make(map[string]chan ServiceAction)
		for i := 0; i < len(app.Services); i++ {
			service := &app.Services[i]

			lock := sync.Mutex{}

			serviceState := ServiceState{}
			actions := make(chan ServiceAction)
			channels.Apps[app.Name][service.Name] = actions

			process := struct{ cmd *exec.Cmd }{}

			go func() {
				for {
					action := <-actions

					switch action {
					case Stop:
						if service.Daemon {
							cmd := userCommand(service.User, service.StopCommand)
							if app.Root != "" {
								cmd.Dir = fmt.Sprintf("%v/%v", app.Root, service.Root)
							}
							err := startSubprocessFor(app.Name, service.Name, false, cmd, updates)
              if err !=  nil {
                println(fmt.Sprintf("error stopping daemon %v.%v: %q", app.Name, service.Name, err))
              }
							lock.Lock()
							serviceState.Running = false
							updates <- ServiceUpdate{
								App:     app.Name,
								Service: service.Name,
								Kind:    RunState,
                Running: false,
                ExitCode: cmd.ProcessState.ExitCode(),
							}
							lock.Unlock()
						} else {
              if process.cmd == nil {
                println("cant stop a nil process")
              }
              if process.cmd != nil && process.cmd.Process != nil && process.cmd.Process.Pid > 0 {
                err := startSubprocessFor(
                  app.Name, service.Name, false, exec.Command(
                    "kill", "-9", strconv.Itoa(process.cmd.Process.Pid),
                  ), updates,
                )
                if err !=  nil {
                  println(fmt.Sprintf("error stopping daemon %v.%v: %q", app.Name, service.Name, err))
                }
                lock.Lock()
                serviceState.Running = false
                updates <- ServiceUpdate{
                  App:     app.Name,
                  Service: service.Name,
                  Kind:    RunState,
                  Running: false,
                  ExitCode: process.cmd.ProcessState.ExitCode(),
                }
                lock.Unlock()
              }
						}
					case Start:
            if serviceState.Running {
              return // already running
            }
						if service.Daemon {
							cmd := userCommand(service.User, service.Command)
							if app.Root != "" {
								cmd.Dir = fmt.Sprintf("%v/%v", app.Root, service.Root)
							}
							err := startSubprocessFor(app.Name, service.Name, true, cmd, updates)
              cmd.Wait()
							if err != nil {
								fmt.Println(fmt.Sprintf("Could not start service %v: %q", service.Name, err))
								updates <- ServiceUpdate{
									App:     app.Name,
									Service: service.Name,
									Kind:    RunState,
									Running: false,
								}
								return
							}
							lock.Lock()
							serviceState.Running = true
							updates <- ServiceUpdate{
								App:     app.Name,
								Service: service.Name,
                Kind:    RunState,
                Running: true,
                ExitCode: 0,
							}
							lock.Unlock()
						} else {
							cmd := userCommand(service.User, service.Command)
							if app.Root != "" {
								cmd.Dir = fmt.Sprintf("%v/%v", app.Root, service.Root)
							}
							err := startSubprocessFor(app.Name, service.Name, false, cmd, updates)
							if err != nil {
								fmt.Println("Could not start service", service.Name, err)
							} else if cmd.Process != nil {
								lock.Lock()
								process.cmd = cmd
								serviceState.Running = cmd.ProcessState != nil && !cmd.ProcessState.Exited()
								updates <- ServiceUpdate{
									App:     app.Name,
									Service: service.Name,
                  Kind: RunState,
                  Running: true,
                  ExitCode: 0,
								}
								lock.Unlock()
							}
						}
					}
				}
			}()
		}
	}
	return updates, channels, nil
}

func Serve() error {
	conf, err := loadConfig()
	if err != nil {
		return err
	}

	updates, actionChannels, err := startup(&conf)

	if err != nil {
		return err
	}

	state := State{
		Apps: make(map[string]AppState),
	}

	for i := 0; i < len(conf.Apps); i++ {
		app := conf.Apps[i]
		appState := AppState{
			Name:     app.Name,
			Services: make(map[string]ServiceState),
		}
		for j := 0; j < len(app.Services); j++ {
			service := app.Services[j]
			serviceState := ServiceState{
				Conf: service,
			}
			appState.Services[service.Name] = serviceState

      actionChannels.Apps[app.Name][service.Name] <- Start

      if service.RestartInterval > 0 {
        go func() {
          for {
            time.Sleep(service.RestartInterval * time.Millisecond)
            healthy := true
            for _, port := range(service.Ports) {
                if !isPortBound(port) {
                  healthy = false
                }
            }
            if !healthy {
              actionChannels.Apps[app.Name][service.Name] <- Stop
              time.Sleep(3 * time.Second)
              actionChannels.Apps[app.Name][service.Name] <- Start
            }
          }
        }()
      }
		}
		state.Apps[app.Name] = appState
	}

	var lock sync.RWMutex

	go func() {
		for {
			update := <-updates
			lock.Lock()
			mutate := state.Apps[update.App].Services[update.Service]
			switch update.Kind {
			case Init:
				mutate = update.State
			case RunState:
				mutate.Running = update.Running
				mutate.ExitCode = update.ExitCode
			case Stdout:
				mutate.StdoutBuffer = append(mutate.StdoutBuffer, update.Stdout)
			case Stderr:
				mutate.StderrBuffer = append(mutate.StderrBuffer, update.Stderr)
			}
			state.Apps[update.App].Services[update.Service] = mutate
			lock.Unlock()
		}
	}()

	http.HandleFunc("/api/apps", func(w http.ResponseWriter, r *http.Request) {
		data, err := json.Marshal(conf)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to marshal response: %q", err), 500)
			return
		}
		w.Write(data)
	})

	http.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		lock.RLock()
		data, err := json.Marshal(state)
		lock.RUnlock()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to marshal response: %q", err), 500)
			return
		}
		w.Write(data)
	})

	http.HandleFunc("/api/service/start", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			println("error reading request body", err)
			http.Error(w, fmt.Sprintf("error reading request body %q", err), 400)
			return
		}
		var msg struct {
			App     string `json:"app"`
			Service string `json:"service"`
		}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			http.Error(w, fmt.Sprintf("error json parsing request %q", err), 400)
			return
		}
		actionChannels.Apps[msg.App][msg.Service] <- Start
		fmt.Fprintf(w, "Started service %v.%v", msg.App, msg.Service)
	})

	http.HandleFunc("/api/service/stop", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			println("error reading request body", err)
			http.Error(w, fmt.Sprintf("error reading request body %q", err), 400)
			return
		}
		var msg struct {
			App     string `json:"app"`
			Service string `json:"service"`
		}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			http.Error(w, fmt.Sprintf("error json parsing request %q", err), 400)
			return
		}
		actionChannels.Apps[msg.App][msg.Service] <- Stop
		fmt.Fprintf(w, "Stopped Service %v.%v", msg.App, msg.Service)
	})

	http.HandleFunc("/api/app/start", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error reading request body %q", err), 400)
			return
		}
		var msg struct {
			App     string `json:"app"`
		}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			http.Error(w, fmt.Sprintf("error json parsing request %q", err), 400)
			return
		}
    for _, ch := range actionChannels.Apps[msg.App] {
      ch <- Start
    }
	})

	http.HandleFunc("/api/app/stop", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error reading request body %q", err), 400)
			return
		}
		var msg struct {
			App     string `json:"app"`
		}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			http.Error(w, fmt.Sprintf("error json parsing request %q", err), 400)
			return
		}
    for _, ch := range actionChannels.Apps[msg.App] {
      ch <- Stop
    }
	})

  http.HandleFunc("/api/port", func(w http.ResponseWriter, r *http.Request) {
    sport := strings.TrimSpace(r.URL.RawQuery)
    port, err := strconv.Atoi(sport)
    if err != nil {
      http.Error(w, fmt.Sprintf("%v is not a number", sport), 400)
      return
    }
    fmt.Fprintf(w, "%v", isPortBound(port))
  })

  http.HandleFunc("/api/ports", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("error reading request body %q", err), 400)
			return
		}
    var ports []int
		err = json.Unmarshal(body, &ports)

		if err != nil {
			http.Error(w, fmt.Sprintf("error unmarshalling %v: %q", string(body), err), 400)
			return
		}
    result := make(map[string]bool)

    checks := make(chan struct{int; bool})

    for _, port := range(ports) {
      go func(port int) {
        checks <- struct{int;bool}{port, isPortBound(port)}
      }(port)
    }

    for _ = range(ports) {
      portState := <-checks
      result[strconv.Itoa(portState.int)] = portState.bool
    }

    out, err := json.Marshal(result)
		if err != nil {
			http.Error(w, fmt.Sprintf("error marshalling map: %q", err), 500)
			return
		}
    w.Write(out)
  })

	http.Handle("/", http.FileServer(http.Dir("./www")))

	fmt.Println("serving on port:", conf.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", conf.Port), nil))

	return nil
}
