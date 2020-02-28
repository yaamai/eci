package main

import (
	"os"
	"path/filepath"
	"strings"
	// force load overlay driver
	_ "github.com/containers/storage/drivers/overlay"
)

func ContainString(list []string, s string) bool {
	for _, elem := range list {
		if elem == s {
			return true
		}
	}
	return false
}

func getPathEnv(env []string) string {
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			return e
		}
	}
	return ""
}

func getAbsolutePath(name string, pathEnv string) string {
	pathEnvKeyVal := strings.Split(pathEnv, "=")
	pathEnvVal := pathEnvKeyVal[1]
	pathList := strings.Split(pathEnvVal, ":")

	// first, test name as abspath
	pathList = append([]string{""}, pathList...)

	for _, path := range pathList {
		absPath := filepath.Join(path, name)
		if _, err := os.Stat(absPath); err == nil {
			return absPath
		}
	}

	return ""
}
