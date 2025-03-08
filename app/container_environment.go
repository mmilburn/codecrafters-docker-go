package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type ContainerEnvironment struct {
	command string
	args    []string
	path    string
	dl      *DockerImageDownloader
}

func NewContainerEnvironment(args []string) *ContainerEnvironment {
	env := &ContainerEnvironment{command: args[3], args: args[4:], dl: NewDockerImageDownloader(args[2])}
	env.initFS()
	env.mknull()
	env.dl.DownloadAndUnpackLayers(env.path)
	return env
}

func (env *ContainerEnvironment) initFS() {
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		log.Fatal(err)
	}
	env.path = tmpDir
}

func (env *ContainerEnvironment) CopyFile() error {
	dst := filepath.Join(env.path, strings.TrimLeft(env.command, "/"))
	dir := filepath.Dir(dst)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	in, err := os.Open(env.command)
	if err != nil {
		return err
	}
	defer func(in *os.File) {
		err := in.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(in)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	stat, err := in.Stat()
	if err != nil {
		return err
	}
	if err := out.Chmod(stat.Mode()); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

func (env *ContainerEnvironment) Close() {
	err := os.RemoveAll(env.path)
	if err != nil {
		log.Fatal(err)
	}
}

// Should work for most linux systems...
func (env *ContainerEnvironment) mkdev(major, minor uint32) uint64 {
	return (uint64(major) << 8) | uint64(minor)
}

func (env *ContainerEnvironment) mknull() {
	if err := os.MkdirAll(filepath.Join(env.path, "dev"), 0755); err != nil {
		log.Fatal(fmt.Errorf("mknull: failed to create dev dir: %v", err))
	}
	err := syscall.Mknod(filepath.Join(env.path, "dev/null"), syscall.S_IFCHR|0666, int(env.mkdev(1, 3)))
	if err != nil {
		if !errors.Is(err, syscall.EEXIST) {
			log.Fatal(fmt.Errorf("mknull: failed to mknod: %v", err))
		}
	}
}

func (env *ContainerEnvironment) chroot() {
	if err := syscall.Chroot(env.path); err != nil {
		log.Fatal(fmt.Errorf("chroot failed: %v", err))
	}
	if err := syscall.Chdir("/"); err != nil {
		log.Fatal(fmt.Errorf("chdir failed: %v", err))
	}
}

func (env *ContainerEnvironment) RunCommand() int {
	exitCode := 0
	env.chroot()
	if err := syscall.Unshare(syscall.CLONE_NEWPID); err != nil {
		log.Fatal(err)
	}
	cmd := exec.Command(env.command, env.args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	stdoutBytes, _ := io.ReadAll(stdout)
	stderrBytes, _ := io.ReadAll(stderr)
	err = cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	fmt.Printf(string(stdoutBytes))
	_, err = fmt.Fprint(os.Stderr, string(stderrBytes))
	if err != nil {
		log.Fatal(err)
	}
	return exitCode
}
