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
	"strings"
)

const SampleHead = 16384
const SampleBody = 1015808
const SampleTail = 16384
const PageSize = 4096

var includeHidden bool

type TreeNode struct {
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	Path        string `json:"path"`
	Fingerprint string `json:"print"`
	Size        int64  `json:"size"`
	WasSymlink  bool   `json:"-"`
	Skip        bool   `json:"-"`
}

type Tree struct {
	Root         string              `json:"root"`
	Fingerprints map[string][]string `json:"fingerprints_to_paths"`
}

type Analysis struct {
	Trees      []Tree              `json:"trees"`
	Duplicates map[string][]string `json:"duplicates"`
	Unique     []string            `json:"unique"`
	Missing    map[string][]string `json:"missing"`
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
	scanSubtree(&tree, &node)
	return tree, nil
}

func scanSubtree(tree *Tree, node *TreeNode) error {
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
			if node.Skip {
				return filepath.SkipDir
			}
			collectPrint(&node)
			if node.WasSymlink {
				scanSubtree(tree, &node)
			}
			return nil
		})
	} else {
		collectPrint(node)
	}

	return nil
}

func analyze(trees []Tree) (Analysis, error) {
	analysis := Analysis{
		Trees:      trees,
		Duplicates: make(map[string][]string),
		Unique:     []string{},
		Missing:    make(map[string][]string),
	}

	fmt.Printf("Analyzing %v trees ...\n", len(trees))

	unioned := make(map[string][]string)
	for ti, tree := range trees {
		missing := []string{}
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
				rel, err := filepath.Rel(tree.Root, arr2[0])
				if err != nil {
					rel = arr2[0]
					fmt.Fprintf(os.Stderr, "Error relativizing %v against %v: %v\n", arr2[0], tree.Root, err)
				}
				missing = append(missing, rel)
			}
			unioned[hash] = append(arr, arr2...)
		}
		if len(missing) > 0 {
			analysis.Missing[tree.Root] = missing
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

  fmt.Printf("\n\n======== ANALYSIS ========\n\n")
  if includeHidden {
    fmt.Print("  Hidden files were included in this analysis.\n")
  } else {
    fmt.Print("  Hidden files were NOT included in this analysis.\n")
  }
  fmt.Printf("  Unique files: %v\n", len(analysis.Unique))
  fmt.Printf("  Duplicated files: %v\n", len(analysis.Duplicates))

  missingCount := 0
  for _, files := range analysis.Missing {
    missingCount += len(files)
  }

  fmt.Printf("  Missing* files: %v\n", missingCount)
  fmt.Printf("\n  *files not found in first tree, but present in one or more subsequent trees.\n")
  fmt.Printf("\n==========================\n\n")

	return analysis, nil
}

func scanNode(path string) (TreeNode, error) {
	node := TreeNode{
		Name:        "",
		IsDir:       false,
		Path:        "",
		Fingerprint: "",
		Size:        0,
		WasSymlink:  false,
	}

	link, err := os.Readlink(path)
	if err == nil {
		path = link
		node.WasSymlink = true
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return node, err
	}

	node.Path = path
	node.Name = filepath.Base(path)

	if !includeHidden && strings.HasPrefix(node.Name, ".") {
		node.Skip = true
		return node, nil
	}

	stat, err := os.Stat(path)
	if err != nil {
		return node, err
	}

	node.IsDir = stat.IsDir()

	if !node.IsDir {
		node.Size = stat.Size()

		fmt.Printf("fingerprinting %v ...\n", filepath.Clean(path))

		hasher := sha1.New()
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to fingerprint file %v, using size instead!\n", filepath.Clean(path))
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

func exists(path string) (bool, error) {
  _, err := os.Stat(path)
  if errors.Is(err, os.ErrNotExist) {
    return false, nil
  }
  if err != nil {
    return false, err
  }
  return true, nil
}

func safecopy(dst *Tree, root string, path string) error {
  srcPath := filepath.Join(root, path)
  dstPath := filepath.Join(dst.Root, path)

  index := 0
  for {
    exists, err := exists(dstPath)
    if err != nil {
      fmt.Fprintf(os.Stderr, "Unexpected error backing up file %v: %v\n", srcPath, err)
      return err
    }
    if !exists {
      break
    }
    index++
    ext := filepath.Ext(path)
    base := path[:len(path)-len(ext)]
    dstPath = filepath.Join(dst.Root, fmt.Sprintf("%v-%v%v", base, index, ext))
  }

  fmt.Printf("cp %v\n  to: %v ...", srcPath, dstPath)

  parent := filepath.Dir(dstPath)
  if exists, _ := exists(parent); !exists {
    if err := os.MkdirAll(parent, 0644); err != nil {
      fmt.Fprintf(os.Stderr, "Couldn't create parent directory %v: %v\n", parent, err)
      return err
    }
  }

  in, err := os.Open(srcPath)
  if err != nil {
    return err
  }
  defer in.Close()

  out, err := os.Create(dstPath)
  if err != nil {
    return err
  }
  defer out.Close()
  if _, err = io.Copy(out, in); err != nil {
    return err
  }

  fmt.Printf("...done.\n")

  return nil
}

func main() {
	var doBackup bool

	flag.Usage = usage
	flag.BoolVar(&doBackup, "b", false, "copy missing files into first tree.")
	flag.BoolVar(&includeHidden, "h", false, "include hidden files (starting with a '.')")
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

	fmt.Printf("Writing out %v... ", jsout)
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

  fmt.Printf("written.\n")

  if doBackup {
    fmt.Printf("Backing up %v missing files to %v\n", len(analysis.Missing), trees[0].Root)
    for root, paths := range analysis.Missing {
      for _, path := range paths {
        if err = safecopy(&trees[0], root, path); err != nil {
          fmt.Fprintf(os.Stderr, "Couldn't backup %v: %v.\n", path, err)
        }
      }
    }
  }

	fmt.Printf("Done.\n")
}
