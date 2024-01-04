package watcher

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
	"golang.org/x/tools/go/packages"
)

type Config struct {
	Dir             string
	AdditionalFiles []string
	BuildFlags      []string
	RuntimeArgs     []string
	Vendor          bool
	PrintFiles      bool
	Env             []string

	// Non serialized fields
	Stdout, Stderr io.Writer                `json:"-"`
	OnFileChange   func(file string)        `json:"-"`
	OnProcessStart func()                   `json:"-"`
	OnProcessExit  func(err error)          `json:"-"`
	Logf           func(s string, a ...any) `json:"-"`
}

func Run(ctx context.Context, c Config) error {
	for _, a := range c.BuildFlags {
		if isOutputFlag(a) {
			return fmt.Errorf("-o build flag is disallowed because gowatch manages the go build for you")
		}
	}

	s := set{}
	if len(c.AdditionalFiles) > 0 {
		for _, pattern := range c.AdditionalFiles {
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return err
			}
			s.add(matches...)
		}
	}

	var err error
	if c.Dir == "" {
		c.Dir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("os.Getwd: %w", err)
		}
	}

	goFiles, err := listGoFiles(c.Dir)
	if err != nil {
		return fmt.Errorf("error listing go files: %w", err)
	}
	s.add(goFiles...)

	if c.PrintFiles {
		fmt.Println(strings.Join(s.slice(), "\n"))
		return nil
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
	if c.OnFileChange == nil {
		c.OnFileChange = func(string) {}
	}
	if c.OnProcessStart == nil {
		c.OnProcessStart = func() {}
	}
	if c.OnProcessExit == nil {
		c.OnProcessExit = func(error) {}
	}

	return (&watcher{
		c:        c,
		binpath:  binpath,
		exitChan: make(chan error, 1),
	}).watch(ctx, s.slice())
}

type watcher struct {
	c        Config
	binpath  string
	cmd      *exec.Cmd
	exitChan chan error
	env      []string
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

	err = w.start(ctx)
	if err != nil {
		w.c.OnProcessExit(err)
		w.c.Logf("error starting binary: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			if err == nil {
				err = errors.Join(<-w.exitChan, ctx.Err())
			}
			return err
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				w.c.Logf(color.MagentaString("modified file: %v", event.Name))
				w.c.OnFileChange(event.Name)
				err := w.restart(ctx, event.Name)
				if err != nil {
					w.c.OnProcessExit(err)
					w.c.Logf("error restarting binary: %v", err)
				}
			}
		case err := <-watcher.Errors:
			w.c.Logf("watcher error: %v", err)
		case err := <-w.exitChan:
			w.c.OnProcessExit(err)
			w.c.Logf("process exited unexpectedly: %v", err)
		}
	}
}

func (w *watcher) start(ctx context.Context) error {
	if err := w.build(ctx); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	w.c.OnProcessStart()
	return w.startBinary(ctx)
}

func (w *watcher) restart(ctx context.Context, file string) error {
	if err := w.stop(ctx); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	if err := w.start(ctx); err != nil {
		return fmt.Errorf("start: %v", err)
	}
	return nil
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
		w.cmd = nil
	}
	return nil
}

func (w *watcher) build(ctx context.Context) error {
	args := append([]string{"build", "-o=" + w.binpath}, w.c.BuildFlags...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = w.c.Dir
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
	cmd.Dir = w.c.Dir
	cmd.Stdout = w.c.Stdout
	cmd.Stderr = w.c.Stderr
	cmd.Env = append(os.Environ(), w.c.Env...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}

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

func listGoFiles(wd string) ([]string, error) {
	s := set{}
	cfg := &packages.Config{
		Mode: packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:  wd,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("error loading module: %w", err)
	}
	filesFromPkg(pkgs[0], pkgs[0].Module.Path, s)
	return s.slice(), nil
}

func filesFromPkg(pkg *packages.Package, prefix string, s set) {
	s.add(pkg.GoFiles...)
	for importPath, innerPkg := range pkg.Imports {
		if !strings.HasPrefix(importPath, prefix) {
			continue
		}
		filesFromPkg(innerPkg, prefix, s)
	}
}

type set map[string]struct{}

// addSlice adds each element of es to s.
func (s set) add(es ...string) {
	for _, e := range es {
		s[e] = struct{}{}
	}
}

// slice returns the elements of the set as a slice. The elements will not be
// in any particular order.
func (s set) slice() []string {
	es := make([]string, 0, len(s))
	for k := range s {
		es = append(es, k)
	}
	return es
}
