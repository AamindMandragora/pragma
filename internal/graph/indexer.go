// Package graph builds and maintains the code graph in the session database:
// it parses the repository's Go sources into symbols and the calls between them
// so the agent can ask structural questions instead of grepping.
package graph

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/process"
)

// indexing writes the whole edge table, so two concurrent runs would fight
var indexMu sync.Mutex

// pruned wholesale rather than filtered per file. .agentignore cannot express this:
// filepath.Match has no ** and no directory semantics, so it can only answer
// questions about single paths and would still walk every leaf under .git
var skipDirs = map[string]bool{
	".git":         true,
	".agent":       true,
	"node_modules": true,
	"vendor":       true,
	"testdata":     true,
}

// every Go file worth indexing, as repo-relative paths. _test.go files are kept:
// a function's tests are real callers and are worth having in the graph
func goFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// an unreadable directory should cost us that directory, not the whole index
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// defense in depth so .agentignore is honoured the same way the file tools honour it
		if process.IsIgnored(path) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

// brings the graph up to date with what is on disk, doing nothing at all when
// nothing changed. safe to call before every query: the common case costs one
// hash per file and no writes
func EnsureIndexed(root string) error {
	indexMu.Lock()
	defer indexMu.Unlock()

	paths, err := goFiles(root)
	if err != nil {
		return err
	}

	// hash everything first so we know whether there is any work before doing any
	var onDisk = make(map[string]bool, len(paths))
	var contents = make(map[string][]byte, len(paths))
	var hashes = make(map[string]string, len(paths))
	var changed []string
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		onDisk[p] = true
		contents[p] = data
		hashes[p] = hashBytes(data)
		need, err := db.FileNeedsReindex(p, hashes[p])
		if err != nil {
			return err
		}
		if need {
			changed = append(changed, p)
		}
	}

	// anything the index still remembers but that is no longer on disk
	indexed, err := db.IndexedFiles()
	if err != nil {
		return err
	}
	var removed []string
	for _, p := range indexed {
		if !onDisk[p] {
			removed = append(removed, p)
		}
	}

	if len(changed) == 0 && len(removed) == 0 {
		return nil
	}

	for _, p := range removed {
		if err := db.ForgetFile(p); err != nil {
			return err
		}
	}

	// every file gets parsed even when only one changed, because edges are resolved
	// against the complete symbol set. a file that fails to parse (a syntax error
	// mid-edit, say) is dropped from this run rather than aborting it, and is left
	// out of graph_meta so the next call retries it
	var files []*parsedFile
	var byPath = make(map[string]*parsedFile, len(paths))
	for _, p := range paths {
		pf, err := parseFile(p, contents[p])
		if err != nil {
			continue
		}
		files = append(files, pf)
		byPath[p] = pf
	}

	for _, p := range changed {
		pf, ok := byPath[p]
		if !ok {
			continue
		}
		if err := db.ReplaceFileSymbols(p, pf.symbols); err != nil {
			return err
		}
		if err := db.SetFileIndexed(p, hashes[p]); err != nil {
			return err
		}
	}

	// edges are global: reindexing one file cascades away edges owned by files that
	// did not change, and its own calls may now land somewhere else entirely. patching
	// them incrementally is where the correctness bugs live, so they are rebuilt whole.
	// this only runs when something actually changed, which is what keeps it cheap
	return db.ReplaceEdges(collectEdges(files))
}
