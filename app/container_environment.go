package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ContainerEnvironment represents the environment for running a containerized command
type ContainerEnvironment struct {
	command  string
	args     []string
	rootPath string
	dl       *DockerImageDownloader
}

// NewContainerEnvironment creates a new container environment
func NewContainerEnvironment(args []string) (*ContainerEnvironment, error) {
	if len(args) < 4 {
		return nil, errors.New("insufficient arguments: need at least image, command, and args")
	}

	dl, err := NewDockerImageDownloader(args[2])
	if err != nil {
		return nil, fmt.Errorf("failed to create image downloader: %w", err)
	}

	env := &ContainerEnvironment{
		command: args[3],
		args:    args[4:],
		dl:      dl,
	}

	if err := env.initFS(); err != nil {
		return nil, err
	}

	if err := env.setupDevices(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := env.dl.DownloadAndUnpackLayers(ctx, env.rootPath); err != nil {
		return nil, fmt.Errorf("failed to download and unpack image: %w", err)
	}

	return env, nil
}

// initFS initializes the container filesystem
func (env *ContainerEnvironment) initFS() error {
	tmpDir, err := os.MkdirTemp("", "container-")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}

	env.rootPath = tmpDir
	return nil
}

// CopyFile copies a file from the host to the container
func (env *ContainerEnvironment) CopyFile() error {
	// Validate command exists
	if env.command == "" {
		return errors.New("empty command provided")
	}

	dst := filepath.Join(env.rootPath, strings.TrimLeft(env.command, "/"))
	dir := filepath.Dir(dst)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	src, err := os.Open(env.command)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", env.command, err)
	}
	defer src.Close()

	dest, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, src); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	stat, err := src.Stat()
	if err != nil {
		return fmt.Errorf("failed to get source file stats: %w", err)
	}

	if err := dest.Chmod(stat.Mode()); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
}

// Close cleans up the container environment
func (env *ContainerEnvironment) Close() error {
	if env.rootPath == "" {
		return nil
	}

	return os.RemoveAll(env.rootPath)
}

// mkdev creates a device number from major and minor numbers
func (env *ContainerEnvironment) mkdev(major, minor uint32) uint64 {
	return (uint64(major) << 8) | uint64(minor)
}

// setupDevices creates necessary device files in the container
func (env *ContainerEnvironment) setupDevices() error {
	devPath := filepath.Join(env.rootPath, "dev")
	if err := os.MkdirAll(devPath, 0755); err != nil {
		return fmt.Errorf("failed to create /dev directory: %w", err)
	}

	// Create /dev/null
	nullPath := filepath.Join(devPath, "null")
	err := syscall.Mknod(nullPath, syscall.S_IFCHR|0666, int(env.mkdev(1, 3)))
	if err != nil && !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("failed to create /dev/null: %w", err)
	}

	return nil
}

// prepare performs all preparatory steps before running the command
func (env *ContainerEnvironment) prepare() error {
	// Change root to container filesystem
	if err := syscall.Chroot(env.rootPath); err != nil {
		return fmt.Errorf("chroot failed: %w", err)
	}

	// Change directory to root within the new filesystem
	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir failed: %w", err)
	}

	// Create a new PID namespace
	if err := syscall.Unshare(syscall.CLONE_NEWPID); err != nil {
		return fmt.Errorf("failed to create PID namespace: %w", err)
	}

	return nil
}

// RunCommand runs the command in the container and returns its exit code
func (env *ContainerEnvironment) RunCommand() int {
	if err := env.prepare(); err != nil {
		log.Fatalf("Failed to prepare container environment: %v", err)
	}

	cmd := exec.Command(env.command, env.args...)

	// Set up pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("Failed to create stderr pipe: %v", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start command: %v", err)
	}

	// Capture output
	stdoutCh := make(chan []byte)
	stderrCh := make(chan []byte)

	go func() {
		data, _ := io.ReadAll(stdout)
		stdoutCh <- data
	}()

	go func() {
		data, _ := io.ReadAll(stderr)
		stderrCh <- data
	}()

	// Get command output
	stdoutData := <-stdoutCh
	stderrData := <-stderrCh

	// Wait for command to complete and get exit code
	var exitCode int
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			log.Printf("Error waiting for command: %v", err)
			exitCode = 1
		}
	}

	// Write output to stdout and stderr
	fmt.Print(string(stdoutData))
	fmt.Fprint(os.Stderr, string(stderrData))

	return exitCode
}
