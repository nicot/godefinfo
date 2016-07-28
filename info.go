package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type defInfo struct {
	Pkg   string
	Sym   string
	Field string
}

func outputData(data ...interface{}) {
	if !*useJSON {
		fmt.Println(data)
	}
}

func printInfo(info defInfo) {
	if *useJSON {
		out := json.NewEncoder(os.Stdout)
		//info.UnitType = "GoPackage"
		//path: include main file package
		out.Encode(info)
		return
	}
	fmt.Println(info.Pkg, info.Sym, info.Field)
}
