package main

import (
	"gwen/treerings/scanning"
	"gwen/treerings/webserver"

	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: treerings [output json file] [root directory to scan]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	var doBackup bool
  var web bool

	flag.Usage = usage
	flag.BoolVar(&doBackup, "b", false, "copy missing files into first tree.")
	flag.BoolVar(&scanning.IncludeHidden, "h", false, "include hidden files (starting with a '.')")
  flag.BoolVar(&web, "web", false, "launch web server")
  flag.IntVar(&webserver.Port, "port", 8380, "port for webserver")
	flag.Parse()
	args := flag.Args()

  if web {
    webserver.Serve()
    return
  }

	if len(args) < 1 {
		fmt.Println("Missing output filepath.")
		os.Exit(1)
	}
	if len(args) < 2 {
		fmt.Println("Missing root directory path(s).")
		os.Exit(1)
	}

	jsout := args[0]

  cachedTrees := make(map[string]scanning.Tree)
  if data, err := os.ReadFile(jsout); err == nil {
    var cached scanning.Analysis
    if err := json.Unmarshal(data, &cached); err != nil {
      fmt.Fprintf(os.Stdout, "Old json file is malformed: %v\n", err)
    } else {
      for _, tree := range cached.Trees {
        cachedTrees[tree.Root] = tree
      }
    }
  }

	trees := []scanning.Tree{}

	for _, path := range args[1:] {
    path, err := scanning.NormPath(path)
    if err != nil {
			fmt.Fprintf(os.Stderr, "Path normalization error %v: %v\n", path, err)
      return
    }
    old, _ := cachedTrees[path]
		tree, err := scanning.Rescan(path, &old)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning tree %v: %v\n", path, err)
			return
		}
		trees = append(trees, tree)
	}

	analysis, err := scanning.Analyze(trees)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing trees %v\n", err)
		return
	}
	analysis.Print()

	fmt.Printf("Writing out %v... ", jsout)
	encoded, err := json.Marshal(analysis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshalling json: %v\n", err)
		return
	}

	err = ioutil.WriteFile(jsout, encoded, 0664)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing json: %v\n", err)
		return
	}

	fmt.Printf("written.\n")

	if doBackup {
		analysis.BackupMissing()
	}

	fmt.Printf("Done.\n")
}
