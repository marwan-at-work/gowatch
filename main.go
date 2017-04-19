package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

var path string

func main() {
	var err error
	path, err = os.Getwd()
	if err != nil {
		log.Fatalf("could not get current working directory: %v", err.Error())
	}

	watch(runCmd())
}

func killCmd(cmd *exec.Cmd) (err error) {
	if err = cmd.Process.Kill(); err != nil {
		log.Fatal(err)
	}

	_, err = cmd.Process.Wait()
	return
}

func runCmd() *exec.Cmd {
	_, dirName := filepath.Split(path)
	sub := exec.Command("go", "build")
	sub.Dir = path
	_, err := sub.Output()
	if err != nil {
		switch err.(type) {
		case *exec.ExitError:
			log.Fatal(string(err.(*exec.ExitError).Stderr))
		default:
			log.Fatal(err)
		}
	}

	cmd := exec.Command("./" + dirName)
	cmd.Dir = path
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		log.Fatal(err)
	}

	return cmd
}

func watch(cmd *exec.Cmd) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	done := make(chan bool)
	go func() {
		for event := range watcher.Events {
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println(color.MagentaString("modified file: %v", event.Name))
				if cmdErr := killCmd(cmd); cmdErr != nil {
					log.Fatal(cmdErr)
				}
				cmd = runCmd()
			}
		}
	}()

	errs := []error{}
	for _, p := range getFiles(path) {
		errs = append(errs, watcher.Add(p))
	}

	for _, err = range errs {
		if err != nil {
			log.Fatal(err)
		}
	}
	<-done
}

func getFiles(path string) []string {
	results := []string{}
	folder, _ := os.Open(path)
	defer folder.Close()

	files, _ := folder.Readdir(-1)
	for _, file := range files {
		fileName := file.Name()
		newPath := path + "/" + fileName

		isValidDir := file.IsDir() &&
			!strings.HasPrefix(fileName, ".") &&
			fileName != "vendor"

		isValidFile := !file.IsDir() &&
			strings.HasSuffix(fileName, ".go") &&
			!strings.HasSuffix(fileName, "_test.go")

		if isValidDir {
			results = append(results, getFiles(newPath)...)
		} else if isValidFile {
			results = append(results, newPath)
		}
	}

	return results
}
