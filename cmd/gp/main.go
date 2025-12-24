package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"text/tabwriter"
)

type RepositoryState int

const (
	Updated RepositoryState = iota
	Skipped
	PullFailed
)

var stateName = map[RepositoryState]string{
	Updated:    "updated",
	Skipped:    "skipped (dirty)",
	PullFailed: "pull failed",
}

type Result struct {
	Path  string
	State RepositoryState
}

func (rs RepositoryState) String() string {
	s, ok := stateName[rs]
	if ok {
		return s
	}

	return "?"
}

func main() {
	initLogger()

	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		panic(err)
	}

	results := make(chan Result)
	workers := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		path := filepath.Join(root, e.Name())

		slog.Debug(fmt.Sprintf("processing path: %s", path))

		if !IsRepository(path) {
			continue
		}

		workers++
		go PullIfClean(path, results)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	for i := 0; i < workers; i++ {
		res := <-results
		fmt.Fprintf(w, "%s\t%s\n", res.Path, res.State)
	}

	w.Flush()
}

func initLogger() {
	d, _ := strconv.Atoi(os.Getenv("DEBUG"))
	if d != 1 {
		return
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	slog.SetDefault(slog.New(handler))
	slog.Debug("debug mode enabled")
}

func IsRepository(root string) bool {
	slog.Debug("running: git rev-parse --is-inside-work-tree")
	c := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	c.Dir = root

	stdout, err := c.CombinedOutput()
	if err != nil {
		return false
	}

	slog.Debug(fmt.Sprintf("output: %v", stdout))

	clean := bytes.TrimSpace(stdout)

	return bytes.Equal(clean, []byte("true"))
}

func PullIfClean(root string, res chan<- Result) {
	slog.Debug("running: git status --porcelain")
	c := exec.Command("git", "status", "--porcelain")
	c.Dir = root
	stdout, err := c.CombinedOutput()

	if err != nil {
		slog.Debug(fmt.Sprintf("repo %s not clean with error: %v", root, err))
		res <- Result{
			Path:  root,
			State: Skipped,
		}
		return
	}

	if len(stdout) != 0 {
		slog.Debug(fmt.Sprintf("repo %s not clean", root))
		res <- Result{
			Path:  root,
			State: Skipped,
		}
		return
	}

	slog.Debug("running: git pull --ff-only")
	p := exec.Command("git", "pull", "--ff-only")
	p.Dir = root
	_, err = p.CombinedOutput()
	if err != nil {
		slog.Debug(fmt.Sprintf("repo %s failed to git pull with error: %v", root, err))
		res <- Result{
			Path:  root,
			State: PullFailed,
		}
		return
	}

	res <- Result{
		Path:  root,
		State: Updated,
	}
}
