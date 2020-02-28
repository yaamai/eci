package main

import (
	"flag"
	"github.com/containers/storage/pkg/reexec"
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"syscall"
)

func prepareReExec(procName string, opt *Option) *exec.Cmd {
	cmd := reexec.Command(procName)

	cmd.Args = append(cmd.Args, flag.Args()...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = getOptionEnv(opt)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS |
			syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUSER,
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getgid(),
				Size:        1,
			},
		},
	}

	return cmd
}

func run() {
	// parse flags (only subcommand)
	subcmd, opt, d, err := parseFlags(false)
	initLog(opt)
	container := d.(*Container)
	log.Info("opt:", opt, "data", d, subcmd, err)

	// set container directory env inside container
	container.Envs = append(container.Envs, getOptionEnv(opt)...)
	log.Println(opt, container.Envs)

	// get container directories
	container.Opt = opt

	err = container.prepare()
	if err != nil {
		log.Fatal(err)
	}

	err = container.run()
	if err != nil {
		log.Fatal(err)
	}
}

func init() {
	log.SetLevel(log.ErrorLevel)
	reexec.Register("run", run)
	if reexec.Init() {
		os.Exit(0)
	}
}

func initLog(opt *Option) {
	log.SetLevel(log.ErrorLevel)

	file, err := os.OpenFile(opt.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(file)
	} else {
		log.Info("Failed to log to file, using default stderr")
	}
}

func main() {
	// re-execute self in separated namespaces.
	if reexec.Init() {
		return
	}

	// parse flags (only subcommand)
	subcmd, opt, c, err := parseFlags(true)
	initLog(opt)
	log.Info(c, subcmd, err)

	// prepare re-execute commands
	cmd := prepareReExec(subcmd, opt)
	log.Println(cmd.Env)

	// execute and wait cmd
	if err := cmd.Start(); err != nil {
		log.Fatalf("Error starting the reexec.Command - %s\n", err)
	}

	if err := cmd.Wait(); err != nil {
		log.Fatalf("Error waiting for the reexec.Command - %s\n", err)
	}
}
