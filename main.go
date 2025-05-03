package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
)

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

func usage() {
	fmt.Fprintf(os.Stderr, "usage: treerings [root directory to scan]\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func scan(tree *Tree, path string) error {
	if path == "" {
		if tree.Root == "" {
			return errors.New("Tree root is nil!")
		}
		return scan(tree, tree.Root)
	}

	node, err := scanNode(path)
	if err != nil {
		return err
	}

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

	return nil
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
			return node, err
		}
		defer f.Close()
		if _, err := io.Copy(hasher, f); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to fingerprint file %v, using size instead!\n", filepath.Clean(abs))
			node.Fingerprint = fmt.Sprintf("size:%v", node.Size)
		} else {
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
		fmt.Println("Missing root directory path.")
		os.Exit(1)
	}

  root, err := scanNode(args[0]) 
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning root node: %v\n", root)
		return
	}

	tree := Tree{
		Root:         root.Path,
		Fingerprints: make(map[string][]string),
	}

	if err := scan(&tree, ""); err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning tree: %v\n", err)
		return
	}

	jsout := "treerings.json"

	fmt.Printf("Writing out %v\n", jsout)
	encoded, err := json.Marshal(tree)
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
