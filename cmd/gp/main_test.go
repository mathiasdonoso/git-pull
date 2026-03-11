package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args[1:], err, out)
		}
	}
}

func makeCommit(t *testing.T, dir, message string) {
	t.Helper()
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", message},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args[1:], err, out)
		}
	}
}

// cloneWithUpstream creates a bare "remote" repo, clones it locally, adds a
// commit and pushes, then returns the local clone path. The clone is fully
// up-to-date with its upstream.
func cloneWithUpstream(t *testing.T) string {
	t.Helper()

	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	local := t.TempDir()
	if out, err := exec.Command("git", "clone", remote, local).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = local
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v\n%s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(local, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	makeCommit(t, local, "init")

	push := exec.Command("git", "push")
	push.Dir = local
	if out, err := push.CombinedOutput(); err != nil {
		t.Fatalf("git push: %v\n%s", err, out)
	}
	return local
}

// runMain calls main() directly with the given os.Args, capturing stdout.
// Global state (os.Args, os.Stdout) is restored via t.Cleanup.
func runMain(t *testing.T, args []string) string {
	t.Helper()

	origArgs := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = origArgs })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	main()

	w.Close()
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// ── RepositoryState.String ────────────────────────────────────────────────────

func TestRepositoryState_String(t *testing.T) {
	tests := []struct {
		name  string
		state RepositoryState
		want  string
	}{
		{"updated", Updated, "updated"},
		{"skipped", Skipped, "skipped (dirty)"},
		{"pull failed", PullFailed, "pull failed"},
		{"unknown state", RepositoryState(99), "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── IsRepository ─────────────────────────────────────────────────────────────

func TestIsRepository(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string
		want  bool
	}{
		{
			name: "git repository",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				initGitRepo(t, dir)
				return dir
			},
			want: true,
		},
		{
			name: "plain directory",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			if got := IsRepository(dir); got != tt.want {
				t.Errorf("IsRepository() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── PullIfClean ───────────────────────────────────────────────────────────────

func TestPullIfClean(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T) string
		wantState RepositoryState
	}{
		{
			name: "git status error returns skipped",
			// A plain directory (not a repo) makes `git status --porcelain` exit non-zero.
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			wantState: Skipped,
		},
		{
			name: "dirty repo is skipped",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				initGitRepo(t, dir)
				if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty"), 0644); err != nil {
					t.Fatalf("write file: %v", err)
				}
				return dir
			},
			wantState: Skipped,
		},
		{
			name: "clean repo with no upstream fails pull",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				initGitRepo(t, dir)
				if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644); err != nil {
					t.Fatalf("write file: %v", err)
				}
				makeCommit(t, dir, "init")
				return dir
			},
			wantState: PullFailed,
		},
		{
			name:      "clean repo up-to-date with upstream is updated",
			setup:     cloneWithUpstream,
			wantState: Updated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			res := make(chan Result, 1)
			PullIfClean(dir, res)
			got := <-res
			if got.State != tt.wantState {
				t.Errorf("state = %v, want %v", got.State, tt.wantState)
			}
			if got.Path != dir {
				t.Errorf("path = %q, want %q", got.Path, dir)
			}
		})
	}
}

// ── initLogger ────────────────────────────────────────────────────────────────

func TestInitLogger(t *testing.T) {
	tests := []struct {
		name      string
		debugEnv  string
		wantDebug bool
	}{
		{"debug=0", "0", false},
		{"debug=1", "1", true},
		{"debug unset", "", false},
	}

	// Restore the original default logger after all subtests.
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	// Baseline is a known non-debug logger so each subtest starts from a clean state.
	baseline := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slog.SetDefault(baseline)
			t.Setenv("DEBUG", tt.debugEnv)

			initLogger()

			got := slog.Default().Enabled(context.Background(), slog.LevelDebug)
			if got != tt.wantDebug {
				t.Errorf("debug enabled = %v, want %v", got, tt.wantDebug)
			}
		})
	}
}

// ── main ──────────────────────────────────────────────────────────────────────

func TestMain_PanicsOnMissingDirectory(t *testing.T) {
	origArgs := os.Args
	os.Args = []string{"gp", "/nonexistent/path/xyz987654"}
	defer func() { os.Args = origArgs }()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing directory, got none")
		}
	}()

	main()
}

func TestMainFunc(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) []string // returns os.Args to pass
		check func(t *testing.T, out string)
	}{
		{
			// Exercises the "no args → root = '.'" branch.
			name: "no args uses current directory",
			setup: func(t *testing.T) []string {
				root := t.TempDir()
				orig, err := os.Getwd()
				if err != nil {
					t.Fatalf("Getwd: %v", err)
				}
				if err := os.Chdir(root); err != nil {
					t.Fatalf("Chdir: %v", err)
				}
				t.Cleanup(func() { os.Chdir(orig) })
				return []string{"gp"} // no second arg
			},
		},
		{
			// Exercises !e.IsDir() (file skipped) and !IsRepository() (plain dir skipped).
			name: "skips files and non-repo subdirs",
			setup: func(t *testing.T) []string {
				root := t.TempDir()
				os.Mkdir(filepath.Join(root, "notarepo"), 0755)
				os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644)
				return []string{"gp", root}
			},
		},
		{
			// Exercises the result-printing path via a repo that has no upstream (→ pull failed).
			name: "repo with no upstream prints pull failed",
			setup: func(t *testing.T) []string {
				root := t.TempDir()
				repoDir := filepath.Join(root, "myrepo")
				os.Mkdir(repoDir, 0755)
				initGitRepo(t, repoDir)
				os.WriteFile(filepath.Join(repoDir, "f.txt"), []byte("x"), 0644)
				makeCommit(t, repoDir, "init")
				return []string{"gp", root}
			},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "pull failed") {
					t.Errorf("expected 'pull failed' in output:\n%s", out)
				}
			},
		},
		{
			// Exercises the full happy path: repo up-to-date with remote (→ updated).
			name: "repo up-to-date with upstream prints updated",
			setup: func(t *testing.T) []string {
				local := cloneWithUpstream(t)
				root := filepath.Dir(local)
				return []string{"gp", root}
			},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "updated") {
					t.Errorf("expected 'updated' in output:\n%s", out)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.setup(t)
			out := runMain(t, args)
			if tt.check != nil {
				tt.check(t, out)
			}
		})
	}
}
