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
	Repo     string
	Unit     string
	UnitType string
	Kind     string
}

func outputData(data ...interface{}) {
	// TODO main package files
	output := fmt.Sprintln(data...)
	if !*useJSON {
		fmt.Print(output)
		return
	}
	printStructured(output)
}

func printStructured(output string) {
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
		field := datas[2]
		info.Path = info.Path + "/" + field
	}
	if info.Path == "" {
		info.Kind = "package"
	}
	json.NewEncoder(os.Stdout).Encode(info)
}

func setRepo(info defInfo, fullPath string) defInfo {
	info.Unit = fullPath
	if gosrc.IsGoRepoPath(fullPath) {
		info.Repo = "github.com/golang/go"
		info.Package = "src/" + fullPath
	} else {
		paths := strings.Split(fullPath, "/")
		info.Repo = strings.Join(paths[0:3], "/")
		info.Package = strings.Join(paths[3:], "/")
	}
	return info
}
