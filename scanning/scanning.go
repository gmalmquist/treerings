package scanning

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var IncludeHidden bool

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
	Modified    int64  `json:"modified"`
	WasSymlink  bool   `json:"-"`
	Skip        bool   `json:"-"`
}

type Tree struct {
	Root         string              `json:"root"`
	Fingerprints map[string][]string `json:"fingerprints_to_paths"`
	Nodes        map[string]TreeNode `json:"paths_to_nodes"`
}

type Analysis struct {
	Trees      []Tree              `json:"trees"`
	Duplicates map[string][]string `json:"duplicates"`
	Unique     []string            `json:"unique"`
	Missing    map[string][]string `json:"missing"`
}

func (t *TreeNode) ModTime() time.Time {
	return time.UnixMilli(t.Modified)
}

func Scan(path string) (Tree, error) {
	tree := Tree{
		Root:         "",
		Fingerprints: make(map[string][]string),
		Nodes:        make(map[string]TreeNode),
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
		tree.Nodes[node.Path] = *node
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
				if d.IsDir() {
					return filepath.SkipDir
				} else {
					return nil
				}
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

func Analyze(trees []Tree) (Analysis, error) {
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
		Modified:    0,
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

	if !IncludeHidden && strings.HasPrefix(node.Name, ".") && node.Name != "." {
		node.Skip = true
		return node, nil
	}

	stat, err := os.Stat(path)
	if err != nil {
		return node, err
	}

	node.IsDir = stat.IsDir()
	node.Modified = stat.ModTime().UnixMilli()

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
		if err := os.MkdirAll(parent, 0775); err != nil {
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

func (analysis *Analysis) BackupMissing() {
	fmt.Printf("Backing up %v missing files to %v\n", len(analysis.Missing), analysis.Trees[0].Root)
	for root, paths := range analysis.Missing {
		for _, path := range paths {
			if err := safecopy(&analysis.Trees[0], root, path); err != nil {
				fmt.Fprintf(os.Stderr, "Couldn't backup %v: %v.\n", path, err)
			}
		}
	}
}

func (analysis *Analysis) Print() {
	fmt.Printf("\n\n======== ANALYSIS ========\n\n")
	if IncludeHidden {
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
}
