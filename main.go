package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
)

const SampleHead = 16384
const SampleBody = 1015808
const SampleTail = 16384
const PageSize = 4096

type TreeNode struct {
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	Path        string `json:"path"`
	Fingerprint string `json:"print"`
	Size        int64  `json:"size"`
}

type Tree struct {
	Root         string              `json:"root"`
	Fingerprints map[string][]string `json:"fingerprints_to_paths"`
}

type Analysis struct {
	Trees      []Tree              `json:"trees"`
	Duplicates map[string][]string `json:"duplicates"`
	Unique     []string            `json:"unique"`
	Missing    []string            `json:"missing"`
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: treerings [output json file] [root directory to scan]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func scan(path string) (Tree, error) {
	tree := Tree{
		Root:         "",
		Fingerprints: make(map[string][]string),
	}

	if path == "" {
		return tree, errors.New("Tree root is nil!")
	}

	node, err := scanNode(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning root node: %v\n", path)
		return tree, err
	}

	tree.Root = node.Path

	collectPrint := func(node *TreeNode) {
		if node.Fingerprint == "" {
			return
		}
		if arr, ok := tree.Fingerprints[node.Fingerprint]; ok {
			tree.Fingerprints[node.Fingerprint] = append(arr, node.Path)
		} else {
			tree.Fingerprints[node.Fingerprint] = []string{node.Path}
		}
	}

	if node.IsDir {
		filepath.WalkDir(node.Path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			node, err := scanNode(path)
			if err != nil {
				return err
			}
			collectPrint(&node)
			return nil
		})
	} else {
		collectPrint(&node)
	}

	return tree, nil
}

func analyze(trees []Tree) (Analysis, error) {
	analysis := Analysis{
		Trees:      trees,
		Duplicates: make(map[string][]string),
		Unique:     []string{},
		Missing:    []string{},
	}

	fmt.Printf("Analyzing %v trees ...\n", len(trees))

	unioned := make(map[string][]string)
	for ti, tree := range trees {
		for hash := range tree.Fingerprints {
			arr, ok := unioned[hash]
			if !ok {
				arr = []string{}
			}
			arr2, ok := tree.Fingerprints[hash]
			if !ok {
				// shouldn't happen!
				return analysis, errors.New("Map missing key from key range???")
			}
      if ti > 0 && len(arr) == 0 {
        analysis.Missing = append(analysis.Missing, arr2[0])
      }
			unioned[hash] = append(arr, arr2...)
		}
	}

	for hash := range unioned {
		arr, ok := unioned[hash]
		if !ok {
			// shouldn't happen!
			return analysis, errors.New("Map missing key from key range???")
		}
		if len(arr) == 1 {
			analysis.Unique = append(analysis.Unique, arr...)
		} else if len(arr) > 1 {
			dups, ok := analysis.Duplicates[hash]
			if !ok {
				dups = []string{}
			}
			analysis.Duplicates[hash] = append(dups, arr...)
		}
	}

	return analysis, nil
}

func scanNode(path string) (TreeNode, error) {
	node := TreeNode{
		Name:        "",
		IsDir:       false,
		Path:        "",
		Fingerprint: "",
		Size:        0,
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return node, err
	}

	node.Path = abs
	node.Name = filepath.Base(abs)

	stat, err := os.Stat(abs)
	if err != nil {
		return node, err
	}

	node.IsDir = stat.IsDir()

	if !node.IsDir {
		node.Size = stat.Size()

		fmt.Printf("fingerprinting %v ...\n", filepath.Clean(abs))

		hasher := sha1.New()
		f, err := os.Open(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to fingerprint file %v, using size instead!\n", filepath.Clean(abs))
			node.Fingerprint = fmt.Sprintf("size:%v", node.Size)
			return node, err
		} else {
			defer f.Close()

			hasher.Write([]byte(fmt.Sprintf("filesize:%v\n", node.Size)))
			buf := make([]byte, PageSize)

			var totalRead int64
			var maxBytes int64
			var bodyStart int64

			totalRead = 0
			maxBytes = SampleHead + SampleBody + SampleTail
			bodyStart = node.Size/2 - SampleBody/2
			zone := 0

			for totalRead < maxBytes {
				read, err := f.Read(buf)
				if err != nil {
					break
				}
				if read <= 0 {
					break
				}
				totalRead += int64(read)
				hasher.Write(buf[:read])

				if zone == 0 && totalRead >= SampleHead {
					if bodyStart > totalRead {
						fmt.Printf("  read %v/%v, seek to body %v\n", totalRead, node.Size, bodyStart)
						f.Seek(bodyStart, 0)
					}
					zone = 1
				} else if zone == 1 && totalRead >= SampleHead+SampleBody {
					if maxBytes < node.Size {
						fmt.Printf("  read %v/%v, seek to tail %v\n", totalRead, node.Size, node.Size-SampleTail)
						f.Seek(node.Size-SampleTail, 0)
					}
					zone = 2
				}
			}
			hash := hasher.Sum(nil)
			node.Fingerprint = hex.EncodeToString(hash)
		}
	}

	return node, nil
}

func main() {
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		fmt.Println("Missing output filepath.")
		os.Exit(1)
	}
	if len(args) < 2 {
		fmt.Println("Missing root directory path(s).")
		os.Exit(1)
	}

	trees := []Tree{}

	for _, path := range args[1:] {
		tree, err := scan(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning tree %v: %v\n", path, err)
			return
		}
		trees = append(trees, tree)
	}

	analysis, err := analyze(trees)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing trees %v\n", err)
		return
	}

	jsout := args[0]

	fmt.Printf("Writing out %v\n", jsout)
	encoded, err := json.Marshal(analysis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshalling json: %v\n", err)
		return
	}

	err = ioutil.WriteFile(jsout, encoded, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing json: %v\n", err)
		return
	}

	fmt.Printf("Done.\n")
}
