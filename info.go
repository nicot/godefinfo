package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/golang/gddo/gosrc"
)

type defInfo struct {
	Package  string
	Path     string
	Field    string
	Repo     string
	Unit     string
	UnitType string
}

func outputData(data ...interface{}) {
	output := fmt.Sprintln(data...)
	if !*useJSON {
		fmt.Print(output)
		return
	}
	datas := strings.Split(strings.Trim(output, "\n"), " ")
	info := defInfo{
		UnitType: "GoPackage",
	}
	if len(datas) > 0 {
		info = setRepo(info, datas[0])
	}
	if len(datas) > 1 {
		info.Path = datas[1]
	}
	if len(datas) > 2 {
		info.Field = datas[2]
	}
	json.NewEncoder(os.Stdout).Encode(info)
}

func setRepo(info defInfo, fullPath string) defInfo {
	// The funny thing is none of these are packages
	if gosrc.IsGoRepoPath(fullPath) {
		info.Repo = "github.com/golang/go/"
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
