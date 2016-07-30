package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"path/filepath"

	"github.com/golang/gddo/gosrc"
)

type defInfo struct {
	Package  string
	Path     string
	Repo     string
	Unit     string
	UnitType string
	Kind     string
	File     string
}

func outputData(data ...interface{}) {
	output := fmt.Sprintln(data...)
	if !*useJSON {
		fmt.Print(output)
		return
	}
	datas := strings.Split(strings.Trim(output, "\n"), " ")
	file := filepath.Base(*filename)
	info := defInfo{
		UnitType: "GoPackage",
		File:     file,
	}
	if len(datas) > 0 {
		info = setRepo(info, datas[0])
	}
	if len(datas) > 1 {
		info.Path = datas[1]
	}
	if len(datas) > 2 {
		field := datas[2]
		info.Path = info.Path + "/" + field
	}
	if info.Path == "" {
		info.Kind = "package"
	}
	json.NewEncoder(os.Stdout).Encode(info)
	// TODO main package files
	// TODO package tree urls
}

func setRepo(info defInfo, fullPath string) defInfo {
	// The funny thing is none of these are packages
	if gosrc.IsGoRepoPath(fullPath) {
		info.Repo = "github.com/golang/go"
	} else {
		paths := strings.Split(fullPath, "/")
		paths = paths[0:3]
		info.Repo = strings.Join(paths, "/")
	}
	// Don't ask me:
	info.Package = fullPath
	info.Unit = fullPath
	return info
}

var x int
var y string
