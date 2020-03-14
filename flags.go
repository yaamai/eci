package main

import (
	"github.com/pkg/errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

type Option struct {
	RunRoot    string
	GraphRoot  string
	Log        string
	StorageOpt string
}

type StringList []string

func (i *StringList) String() string {
	return strings.Join([]string(*i), ",")
}

func (i *StringList) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func parseRunFlags(args []string) (*Container, error) {
	var runEnvFlags, runVolFlags StringList
	var workingDir string
	var tty, detach bool

	runFlags := flag.NewFlagSet("run", flag.ContinueOnError)
	runFlags.Var(&runEnvFlags, "e", "environment vars")
	runFlags.Var(&runVolFlags, "v", "volume bind")
	runFlags.StringVar(&workingDir, "w", "/", "working directory")
	runFlags.BoolVar(&tty, "t", false, "allocate tty")
	runFlags.BoolVar(&detach, "d", false, "detach")
	runFlags.Parse(args)

	c := Container{}
	c.Envs = runEnvFlags
	c.Vols = runVolFlags
	c.WorkDir = workingDir
	c.Tty = tty
	c.Detach = detach

	if runFlags.NArg() < 1 {
		return nil, errors.New("image name not passed")
	}

	runArgs := runFlags.Args()
	c.Image = runArgs[0]
	c.Args = runArgs[1:]
	return &c, nil
}

func getOptionEnv(opt *Option) []string {
	return []string{
		fmt.Sprintf("_CONTAINER_RUN_ROOT=%s", opt.RunRoot),
		fmt.Sprintf("_CONTAINER_GRAPH_ROOT=%s", opt.GraphRoot),
		fmt.Sprintf("_CONTAINER_STORAGE_OPT=%s", opt.StorageOpt),
		fmt.Sprintf("_CONTAINER_LOG=%s", opt.Log),
	}
}

func getContainerRootDirectory() (string, string) {
	runRoot := os.Getenv("_CONTAINER_RUN_ROOT")
	graphRoot := os.Getenv("_CONTAINER_GRAPH_ROOT")

	if runRoot == "" {
		runRoot = fmt.Sprintf("/run/user/%d/containers", os.Getuid())
	}
	if graphRoot == "" {
		graphRoot = fmt.Sprintf("%s/.local/share/containers/storage", os.Getenv("HOME"))
	}

	return runRoot, graphRoot
}

func getLogPath() string {
	// set log output
	path := os.Getenv("_CONTAINER_LOG")
	if path == "" {
		path = "/tmp/container.log"
	}
	return path
}

func getStorageOpt() string {
	opt := os.Getenv("_CONTAINER_STORAGE_OPT")
	if opt == "" {
		opt = ".mount_program=/usr/bin/fuse-overlayfs"
	}
	return opt
}

func parseFlags(onlySubcmd bool) (string, *Option, interface{}, error) {

	runRoot, graphRoot := getContainerRootDirectory()
	logPath := getLogPath()
	storageOpt := getStorageOpt()
	flag.StringVar(&storageOpt, "storage-opt", storageOpt, "storage option")
	flag.StringVar(&graphRoot, "graph-root", graphRoot, "graph root directory")
	flag.StringVar(&runRoot, "run-root", runRoot, "working directory")
	flag.StringVar(&logPath, "log", logPath, "log filename")
	flag.Parse()

	opt := Option{RunRoot: runRoot, GraphRoot: graphRoot, Log: logPath, StorageOpt: storageOpt}

	if flag.NArg() <= 0 {
		return "", nil, nil, errors.New("subcommand not passed")
	}

	args := flag.Args()

	if onlySubcmd {
		return args[0], &opt, nil, nil
	}

	if args[0] == "run" {
		c, err := parseRunFlags(args[1:])
		return args[0], &opt, c, errors.Wrap(err, "failed to parse run flags")
	}

	return "", nil, nil, errors.New("subcommand not found")
}
