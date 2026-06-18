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
	"time"
)

type RepositoryState int

const (
	Updated RepositoryState = iota
	Skipped
	PullFailed
	Errored
	RateLimited
	SkippedHTTP
)

var stateName = map[RepositoryState]string{
	Updated:     "updated",
	Skipped:     "skipped (dirty)",
	PullFailed:  "pull failed",
	Errored:     "error",
	RateLimited: "rate limited",
	SkippedHTTP: "skipped (http remote)",
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

	// default to 250 requests per minute to stay well under gitlab's ssh rate limit
	ticker := time.NewTicker(240 * time.Millisecond)
	defer ticker.Stop()

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
		<-ticker.C
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
	slog.Debug("running: git rev-parse --show-toplevel")
	c := exec.Command("git", "rev-parse", "--show-toplevel")
	c.Dir = root

	stdout, err := c.CombinedOutput()
	if err != nil {
		return false
	}

	slog.Debug(fmt.Sprintf("output: %s", stdout))

	toplevel := string(bytes.TrimSpace(stdout))

	abs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false
	}

	return toplevel == resolved
}

func isHTTPRemote(root string) bool {
	slog.Debug("running: git remote get-url origin")
	c := exec.Command("git", "remote", "get-url", "origin")
	c.Dir = root

	stdout, err := c.CombinedOutput()
	if err != nil {
		return false
	}

	url := bytes.TrimSpace(stdout)

	return bytes.HasPrefix(url, []byte("http://")) || bytes.HasPrefix(url, []byte("https://"))
}

func PullIfClean(root string, res chan<- Result) {
	if isHTTPRemote(root) {
		slog.Debug(fmt.Sprintf("repo %s uses an http(s) remote, skipping", root))
		res <- Result{
			Path:  root,
			State: SkippedHTTP,
		}
		return
	}

	slog.Debug("running: git status --porcelain")
	c := exec.Command("git", "status", "--porcelain")
	c.Dir = root
	stdout, err := c.CombinedOutput()

	if err != nil {
		slog.Debug(fmt.Sprintf("repo %s status check failed with error: %v", root, err))
		res <- Result{
			Path:  root,
			State: Errored,
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
	out, err := p.CombinedOutput()
	if err != nil {
		state := PullFailed
		if isRateLimited(out) {
			state = RateLimited
		}
		slog.Debug(fmt.Sprintf("repo %s failed to git pull (%s) with error: %v", root, state, err))
		res <- Result{
			Path:  root,
			State: state,
		}
		return
	}

	res <- Result{
		Path:  root,
		State: Updated,
	}
}

// rateLimitSignatures are substrings GitLab emits when an SSH operation is
// throttled. Matched case-insensitively against the failed pull's output.
var rateLimitSignatures = [][]byte{
	[]byte("rate limit"),
	[]byte("too many requests"),
	[]byte("429"),
}

func isRateLimited(out []byte) bool {
	lower := bytes.ToLower(out)
	for _, sig := range rateLimitSignatures {
		if bytes.Contains(lower, sig) {
			return true
		}
	}
	return false
}

