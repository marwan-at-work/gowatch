package watcher2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type Config struct {
	GoPaths     []string
	NonGoPaths  []string
	BuildFlags  []string
	RuntimeArgs []string
	Vendor      bool
	PrintFiles  bool

	// Non serialized fields
	Stdout, Stderr io.Writer                `json:"-"`
	OnFileChange   func(file string)        `json:"-"`
	OnProcesExit   func(err error)          `json:"-"`
	Logf           func(s string, a ...any) `json:"-"`
}

func Run(ctx context.Context, c Config) error {
	if len(c.GoPaths) == 0 {
		c.GoPaths = []string{"."}
	}
	for _, a := range c.BuildFlags {
		if isOutputFlag(a) {
			return fmt.Errorf("-o build flag is disallowed because gowatch manages the go build for you")
		}
	}
	files, err := getUniqueFiles(ctx, c.GoPaths, c.NonGoPaths, c.Vendor)
	if err != nil {
		return fmt.Errorf("getUniqueFiles: %w", err)
	}
	if c.PrintFiles {
		fmt.Println(strings.Join(files, "\n"))
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("os.Getwd: %w", err)
	}

	tmpdir, err := os.MkdirTemp("", "gowatch")
	if err != nil {
		return fmt.Errorf("os.MkdirTemp: %w", err)
	}
	binpath := filepath.Join(tmpdir, "__gowatch")
	defer os.RemoveAll(tmpdir)

	if c.Logf == nil {
		c.Logf = log.Printf
	}
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}

	w := &watcher{
		c:        c,
		binpath:  binpath,
		wd:       wd,
		exitChan: make(chan error, 1),
	}
	return w.watch(ctx, files)
}

type watcher struct {
	c        Config
	binpath  string
	wd       string
	cmd      *exec.Cmd
	exitChan chan error
}

func (w *watcher) watch(ctx context.Context, files []string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	defer watcher.Close()
	for _, f := range files {
		err = watcher.Add(f)
		if err != nil {
			return fmt.Errorf("watcher.Add(%q): %w", f, err)
		}
	}

	for {
		err = w.start(ctx)
		if err != nil {
			w.c.Logf("watcher.start: %v", err)
		}
		select {
		case <-ctx.Done():
			// TODO: wrap dat shit up? MAYBE HAPPENS ON DEFURR?
			return ctx.Err()
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				w.c.OnFileChange(event.Name)
				w.c.Logf(color.MagentaString("modified file: %v", event.Name))
				err = w.stop(ctx)
				if err != nil {
					return fmt.Errorf("watcher.stop: %w", err)
				}
			}
		case err := <-watcher.Errors:
			w.c.Logf("watcher error: %v", err)
		case err := <-w.exitChan:
			w.c.OnProcesExit(err)
			w.c.Logf("error running binary: %v", err)
		}
	}
}

func (w *watcher) start(ctx context.Context) error {
	if err := w.build(ctx); err != nil {
		return err
	}
	return w.startBinary(ctx)
}

func (w *watcher) stop(ctx context.Context) error {
	if w.cmd == nil {
		return nil
	}
	// TODO: call cmd.Process.Kill() if need be and/or timeout.
	err := w.cmd.Process.Signal(os.Interrupt)
	if err != nil {
		return fmt.Errorf("process.Interrupt: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err = <-w.exitChan:
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			return fmt.Errorf("process.Wait: %w", err)
		}
	}
	return nil
}

func (w *watcher) build(ctx context.Context) error {
	args := append([]string{"build", "-o=" + w.binpath}, w.c.BuildFlags...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = w.wd // TODO: customizable
	cmd.Stdout = w.c.Stdout
	cmd.Stderr = w.c.Stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("goBuild: %w", err)
	}
	return nil
}

func (w *watcher) startBinary(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, w.binpath, w.c.RuntimeArgs...)
	cmd.Dir = w.wd
	cmd.Stdout = w.c.Stdout
	cmd.Stderr = w.c.Stderr
	cmd.Env = os.Environ()
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("cmd.Start: %w", err)
	}
	w.cmd = cmd
	go func() {
		err := cmd.Wait()
		w.exitChan <- err
	}()
	return nil
}

func isOutputFlag(f string) bool {
	return f == "-o" || f == "--o" || strings.HasPrefix(f, "-o=") || strings.HasPrefix(f, "--o=")
}

func getUniqueFiles(ctx context.Context, goDirs, nonGoDirs []string, vendor bool) ([]string, error) {
	files, err := getFiles(ctx, goDirs, vendor, true)
	if err != nil {
		return nil, fmt.Errorf("getGoFiles: %w", err)
	}
	nonGoFiles, err := getFiles(ctx, nonGoDirs, vendor, false)
	if err != nil {
		return nil, fmt.Errorf("getNonGoFiles: %w", err)
	}
	return uniq(files, nonGoFiles), nil
}

func uniq(slices ...[]string) []string {
	mp := map[string]struct{}{}
	final := []string{}
	for _, slc := range slices {
		for _, el := range slc {
			if _, ok := mp[el]; ok {
				continue
			}
			mp[el] = struct{}{}
			final = append(final, el)
		}
	}
	return final
}

func getFiles(ctx context.Context, paths []string, vendor, goFiles bool) ([]string, error) {
	files := []string{}
	for _, pathName := range paths {
		if !vendor && pathName == "vendor" {
			continue
		}
		fi, err := os.Stat(pathName)
		if err != nil {
			return nil, fmt.Errorf("error stating %q: %w", pathName, err)
		}
		if !fi.IsDir() {
			if goFiles && filepath.Ext(pathName) != ".go" {
				continue
			}
			files = append(files, pathName)
			continue
		}
		dirFiles, err := os.ReadDir(pathName)
		if err != nil {
			return nil, fmt.Errorf("os.ReadDir(%q): %w", pathName, err)
		}
		for _, df := range dirFiles {
			entryName := df.Name()
			if entryName == ".git" {
				continue
			}
			if df.IsDir() {
				innerDir := filepath.Join(pathName, entryName)
				dirFiles, err := getFiles(ctx, []string{innerDir}, vendor, goFiles)
				if err != nil {
					return nil, fmt.Errorf("getGoFiles(%q): %w", entryName, err)
				}
				files = append(files, dirFiles...)
				continue
			}
			if goFiles && filepath.Ext(entryName) != ".go" {
				continue
			}
			files = append(files, filepath.Join(pathName, df.Name()))
		}
	}
	return files, nil
}
