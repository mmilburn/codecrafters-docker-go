# About the Project

This is a finished Go implementation for the
["Build Your Own Docker" Challenge](https://codecrafters.io/challenges/docker).

This code implements functionality for all stages (and extensions) of the
challenge as of 2025-03-08.

## What can it do?

The program can pull an image from [Docker Hub](https://hub.docker.com/) and
run a command under process and filesystem isolation.

## Running the Code
This code uses linux-specific syscalls so will be run _inside_ a Docker container.

Please ensure you have [Docker installed](https://docs.docker.com/get-docker/)
locally.

Next, add a [shell alias](https://shapeshed.com/unix-alias/):

```sh
alias mydocker='docker build -t mydocker . && docker run --cap-add="SYS_ADMIN" mydocker'
```

(The `--cap-add="SYS_ADMIN"` flag is required to create
[PID Namespaces](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html))

You can now execute the code and run a simple command like this:

```sh
mydocker run alpine:latest cat /etc/issue
```

## Test Run Video

A short video of the code being run in the codecrafters test environment:

https://github.com/user-attachments/assets/2d4d50d3-f3aa-4411-be1d-1c7b34b5d7c0