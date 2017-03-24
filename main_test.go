package main

import (
	"os"
	"testing"
)

func TestGetFiles(t *testing.T) {
	path, err := os.Getwd()
	if err != nil {
		t.Fatal("could not get cwd", err.Error())
	}

	files := getFiles(path)
	mainFile := path + "/main.go"
	mainTest := path + "/main_test.go"

	for _, f := range files {
		if f != mainFile && f != mainTest {
			t.Error("wrong file to watch,", f)
		}
	}
}
