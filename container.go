package main

import (
	"encoding/json"
	"errors"
	"github.com/containers/common/pkg/unshare"
	"github.com/containers/storage"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	// force load overlay driver
	_ "github.com/containers/storage/drivers/overlay"
	"github.com/creack/pty"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh/terminal"
	"io"
	"os/signal"
	// "github.com/containers/storage/pkg/idtools"
)

type ImageConfig struct {
	Env []string `json:"env"`
}

type ImageBigData struct {
	Config ImageConfig `json:"config"`
}

func getImageEnvs(store *storage.Store, imageId string, bigDataNames []string) ([]string, error) {
	// FIXME: bigDataNames may be determine by "manifest" -> config.XXX -> "sha256:..."
	// but, generally first bigdata contains iamge info.
	bigdataBytes, err := (*store).ImageBigData(imageId, bigDataNames[0])
	if err != nil {
		return nil, err
	}

	bigdata := ImageBigData{}
	err = json.Unmarshal(bigdataBytes, &bigdata)
	if err != nil {
		return nil, err
	}

	return bigdata.Config.Env, nil
}

// return image.TopLayer, image.Envs and errors.
func getImageInfoByName(name string, store *storage.Store) (string, []string, error) {
	images, err := (*store).Images()
	if err != nil {
		return "", nil, err
	}

	for _, image := range images {
		if ContainString(image.Names, name) {
			env, err := getImageEnvs(store, image.ID, image.BigDataNames)
			if err != nil {
				return "", nil, err
			}

			return image.TopLayer, env, nil
		}
	}

	return "", nil, errors.New("Image not found")
}

type Container struct {
	store *storage.Store
	Opt   *Option

	Envs      []string
	ImageEnvs []string
	Vols      []string
	Image     string
	Args      []string
	WorkDir   string
	Tty       bool
	Detach    bool
}

func mountProc(newroot string) error {
	mounts := []struct {
		mkdir  bool
		source string
		target string
		fstype string
		flags  uint
		data   string
	}{
		{true, "tmpfs", filepath.Join(newroot, "/dev"), "tmpfs", 0, "mode=755"},
		{true, "tmpfs", filepath.Join(newroot, "/tmp"), "tmpfs", 0, "mode=1777"},
		{true, "devpts", filepath.Join(newroot, "/dev/pts"), "devpts", 0, "mode=620,ptmxmode=666"},
		{true, "proc", filepath.Join(newroot, "/proc"), "proc", 0, ""},
		{true, "shm", filepath.Join(newroot, "/dev/shm"), "tmpfs", 0, ""},
		{false, "/dev/tty", filepath.Join(newroot, "/dev/tty"), "dev", syscall.MS_BIND, ""},
		{false, "/dev/urandom", filepath.Join(newroot, "/dev/urandom"), "devtmpfs", syscall.MS_BIND, ""},
		{false, "/dev/random", filepath.Join(newroot, "/dev/random"), "devtmpfs", syscall.MS_BIND, ""},
		{false, "/dev/null", filepath.Join(newroot, "/dev/null"), "devtmpfs", syscall.MS_BIND, ""},
		{false, "/dev/full", filepath.Join(newroot, "/dev/full"), "devtmpfs", syscall.MS_BIND, ""},
		{false, "/dev/zero", filepath.Join(newroot, "/dev/zero"), "devtmpfs", syscall.MS_BIND, ""},
		{false, "/dev/fuse", filepath.Join(newroot, "/dev/fuse"), "dev", syscall.MS_BIND, ""},
		{false, "/etc/resolv.conf", filepath.Join(newroot, "/etc/resolv.conf"), "none", syscall.MS_BIND, ""},
	}

	for _, m := range mounts {
		if m.mkdir {
			os.MkdirAll(m.target, 0755)
		} else {
			os.Create(m.target)
		}
		log.Println(m)
		if err := syscall.Mount(m.source, m.target, m.fstype, uintptr(m.flags), m.data); err != nil {
			return err
		}
	}

	return nil
}

func (c *Container) initStore() error {
	storeOpt, err := storage.DefaultStoreOptions(unshare.IsRootless(), unshare.GetRootlessUID())
	if err != nil {
		return err
	}
	storeOpt.RunRoot = c.Opt.RunRoot
	storeOpt.GraphRoot = c.Opt.GraphRoot
	storeOpt.GraphDriverOptions = strings.Split(c.Opt.StorageOpt, ",")

	store, err := storage.GetStore(storeOpt)
	if err != nil {
		return err
	}

	c.store = &store
	return nil
}

func (c *Container) mountRoot() (string, error) {

	imageTopLayer, envs, err := getImageInfoByName(c.Image, c.store)
	if err != nil {
		return "", err
	}

	// append image env to container
	c.ImageEnvs = envs

	newroot, err := (*c.store).Mount(imageTopLayer, "")
	if err != nil {
		return "", err
	}

	if err := syscall.Mount(newroot, newroot, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return "", err
	}

	return newroot, nil
}

func (c *Container) mountVolumes() error {
	for _, volMap := range c.Vols {
		volMapArray := strings.Split(volMap, ":")
		src := filepath.Join("/.pivot_root", volMapArray[1])
		dest := volMapArray[0]

		if err := os.MkdirAll(dest, 0700); err != nil {
			return err
		}

		if err := syscall.Mount(src, dest, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return err
		}
	}

	return nil
}

func (c *Container) cleanupPivot() error {
	if err := os.Chdir(c.WorkDir); err != nil {
		return err
	}

	old := "/.pivot_root"
	if err := syscall.Unmount(old, syscall.MNT_DETACH); err != nil {
		return err
	}

	return nil
}

func (c *Container) makeDeviceLinks() error {
	links := [][]string{
		{"/dev/pts/ptmx", "/dev/ptmx"},
		{"/dev/pts/0", "/dev/console"},
		{"/proc/self/fd", "/dev/fd"},
		{"/proc/self/fd/2", "/dev/stderr"},
		{"/proc/self/fd/0", "/dev/stdin"},
		{"/proc/self/fd/1", "/dev/stdout"},
	}

	for _, link := range links {
		err := os.Symlink(link[0], link[1])
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Container) pivot(path string) error {
	old := filepath.Join(path, "/.pivot_root")
	if err := os.MkdirAll(old, 0700); err != nil {
		return err
	}

	if err := syscall.PivotRoot(path, old); err != nil {
		return err
	}

	return nil
}

func (c *Container) prepare() error {
	if err := c.initStore(); err != nil {
		return err
	}

	rootPath, err := c.mountRoot()
	if err != nil {
		return err
	}

	err = mountProc(rootPath)
	if err != nil {
		return err
	}

	if err = c.pivot(rootPath); err != nil {
		log.Fatal(err)
	}

	err = c.mountVolumes()
	if err != nil {
		return err
	}

	err = c.cleanupPivot()
	if err != nil {
		return err
	}

	err = c.makeDeviceLinks()
	if err != nil {
		return err
	}
	return nil
}

func initStdin(ptmx *os.File) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				log.Printf("error resizing pty: %s", err)
			}
		}
	}()
	ch <- syscall.SIGWINCH // Initial resize.
	// Set stdin in raw mode.
	oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil
	}
	return func() { _ = terminal.Restore(int(os.Stdin.Fd()), oldState) }
}

func runWithTty(cmd *exec.Cmd, r io.Reader, w io.Writer, initTty bool, detach bool) (func(), func(), error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	closePty := func() { _ = ptmx.Close() }

	restoreTermios := func() {}
	if initTty {
		restoreTermios = initStdin(ptmx)
	}

	go func() {
		_, err := io.Copy(ptmx, r)
		log.Warnf("waiting for the tty - %s\n", err)
	}()

	// wait command output end
	readFunc := func() {
		_, err = io.Copy(w, ptmx)
		log.Warnf("waiting for the tty - %s\n", err)
	}

	readFunc()

	return closePty, restoreTermios, nil
}

func (c *Container) run() (int, error) {
	// concat image defiend env and user supplied env
	envs := append(c.ImageEnvs, c.Envs...)
	cmd := exec.Command(getAbsolutePath(c.Args[0], getPathEnv(envs)))
	cmd.Args = c.Args
	// cmd := exec.Command(c.Args[0], c.Args[1:]...)
	cmd.Env = envs
	log.Debug("Executing command:", cmd, c.Args)

	rc := -1
	if c.Tty {
		if c.Detach {
			r, w := io.Pipe()
			closePty, _, err := runWithTty(cmd, r, w, false, true)
			if err != nil {
				log.Warn("tty err", err)
				return -1, err
			}
			defer closePty()
		} else {
			closePty, restoreTermios, err := runWithTty(cmd, os.Stdin, os.Stdout, true, false)
			if err != nil {
				log.Warn("tty err", err)
				return -1, err
			}
			defer closePty()
			defer restoreTermios()
		}
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return -1, err
		}
	}
	if err := cmd.Wait(); err != nil {
		log.Warnf("Error waiting for the reexec.Command - %s\n", err)
	}
	rc = cmd.ProcessState.ExitCode()
	log.Debug("Cmd exit code =", rc)

	return rc, nil
}
