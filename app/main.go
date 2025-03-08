package main

import (
	"log"
	"os"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(os.Args) < 4 {
		log.Fatal("Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...")
	}

	env, err := NewContainerEnvironment(os.Args)
	if err != nil {
		log.Fatal(err)
	}
	// It appears that we cannot test previous stages once on the final stage of the challenge.
	// When we are asked to fetch and run a docker image, I don't know how we determine if we need to copy a binary
	// from the host fs or if the binary will be present in the image. For now, don't bother with trying to copy a
	// binary from the host fs.
	/*	err := env.CopyFile()
		if err != nil {
			log.Fatal(err)
		}*/
	code := env.RunCommand()

	if err := env.Close(); err != nil {
		log.Printf("Error during cleanup: %v", err)
	}
	os.Exit(code)
}
