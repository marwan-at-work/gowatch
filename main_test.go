package main

import (
	"context"
	"os"
	"testing"
)

func TestGetFiles(t *testing.T) {
	path, err := os.Getwd()
	if err != nil {
		t.Fatal("could not get cwd", err.Error())
	}

	files, err := getFiles(context.Background(), []string{path}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	mainFile := path + "/main.go"
	mainTest := path + "/main_test.go"

	for _, f := range files {
		if f != mainFile && f != mainTest {
			t.Error("wrong file to watch,", f)
		}
	}
}
