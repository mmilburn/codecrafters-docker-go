package main

import (
	"errors"
	"fmt"
	// We are not allowed to change go.mod
	// "golang.org/x/sys/unix"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func copyFile(src, dst string) error {
	dir := filepath.Dir(dst)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	in, err := os.Open(src)
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

func rmrf(path string) {
	err := os.RemoveAll(path)
	if err != nil {
		log.Fatal(err)
	}
}

// Should work for most linux systems...
func mkdev(major, minor uint32) uint64 {
	return (uint64(major) << 8) | uint64(minor)
}

func mknull(tmpPath string) {
	if err := os.MkdirAll(filepath.Join(tmpPath, "dev"), 0755); err != nil {
		log.Fatal(fmt.Errorf("mknull: failed to create dev dir: %v", err))
	}
	err := syscall.Mknod(filepath.Join(tmpPath, "dev/null"), syscall.S_IFCHR|0666, int(mkdev(1, 3)))
	if err != nil {
		if !errors.Is(err, syscall.EEXIST) {
			log.Fatal(fmt.Errorf("mknull: failed to mknod: %v", err))
		}
	}
}

func chroot(tmpPath string) {
	if err := syscall.Chroot(tmpPath); err != nil {
		log.Fatal(fmt.Errorf("chroot failed: %v", err))
	}
	if err := syscall.Chdir("/"); err != nil {
		log.Fatal(fmt.Errorf("chdir failed: %v", err))
	}
}

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	exitCode := 0
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		log.Fatal(err)
	}
	defer rmrf(tmpDir)
	destCommand := filepath.Join(tmpDir, strings.TrimLeft(command, "/"))
	if copyFile(command, destCommand) != nil {
		log.Fatal(err)
	}
	mknull(tmpDir)
	chroot(tmpDir)
	if err := syscall.Unshare(syscall.CLONE_NEWPID); err != nil {
		log.Fatal(err)
	}
	cmd := exec.Command(command, args...)
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
	fmt.Fprint(os.Stderr, string(stderrBytes))
	os.Exit(exitCode)
}
