package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DockerImageDownloader struct {
	client *http.Client
	image  string
	tag    string
	token  string
}

type Token struct {
	Token       string    `json:"token"`
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
}

type ManifestEntry struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Platform  struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

type ManifestList struct {
	Manifests []ManifestEntry `json:"manifests"`
}

type LayerEntry struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}
type LayersList struct {
	Layers []LayerEntry `json:"layers"`
}

func NewDockerImageDownloader(imageAndTag string) *DockerImageDownloader {
	dl := &DockerImageDownloader{}
	parts := strings.SplitN(imageAndTag, ":", 2)
	dl.image = parts[0]
	if len(parts) == 1 || parts[1] == "" {
		dl.tag = "latest"
	} else {
		dl.tag = parts[1]
	}
	dl.client = &http.Client{Timeout: 10 * time.Second}
	dl.getToken()
	return dl
}

func (dl *DockerImageDownloader) getToken() {
	url := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/%s:pull", dl.image)
	resp, err := dl.client.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(resp.Body)
	if resp.StatusCode != 200 {
		log.Fatalf("status code error: %d %s", resp.StatusCode, resp.Status)
	}
	var token Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		log.Fatal(err)
	}
	dl.token = token.Token
}

func (dl *DockerImageDownloader) getDigests() LayersList {
	list := LayersList{}
	req, err := http.NewRequest("GET", fmt.Sprintf("https://registry.hub.docker.com/v2/library/%s/manifests/%s", dl.image, dl.tag), nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+dl.token)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := dl.client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(resp.Body)
	if resp.StatusCode != 200 {
		log.Fatalf("status code error: %d %s", resp.StatusCode, resp.Status)
	}
	var manifests ManifestList
	if err := json.NewDecoder(resp.Body).Decode(&manifests); err != nil {
		var layers LayersList
		if err := json.NewDecoder(resp.Body).Decode(&layers); err != nil {
			log.Fatal(err)
		} else {
			return layers
		}
	} else {
		for _, manifest := range manifests.Manifests {
			if manifest.Platform.OS == runtime.GOOS && manifest.Platform.Architecture == runtime.GOARCH {
				return dl.getLayers(manifest.Digest)
			}
		}
	}
	return list
}

func (dl *DockerImageDownloader) getLayers(digest string) LayersList {
	list := LayersList{}
	req, err := http.NewRequest("GET", fmt.Sprintf("https://registry.hub.docker.com/v2/library/%s/manifests/%s", dl.image, digest), nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+dl.token)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := dl.client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(resp.Body)
	if resp.StatusCode != 200 {
		log.Fatalf("status code error: %d %s", resp.StatusCode, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		log.Fatal(err)
	}
	return list
}

func (dl *DockerImageDownloader) DownloadAndUnpackLayers(tempDir string) {
	for _, layer := range dl.getDigests().Layers {
		digestNoSha := strings.Replace(layer.Digest, "sha256:", "", 1)
		tarballPath := filepath.Join(tempDir, fmt.Sprintf("%s.tar.gz", digestNoSha))
		req, err := http.NewRequest("GET", fmt.Sprintf("https://registry.hub.docker.com/v2/library/%s/blobs/%s", dl.image, layer.Digest), nil)
		if err != nil {
			log.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+dl.token)
		req.Header.Set("Accept", layer.MediaType)
		resp, err := dl.client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		if resp.StatusCode != 200 {
			log.Fatalf("status code error: %d %s", resp.StatusCode, resp.Status)
		}
		// write tarball
		out, err := os.Create(tarballPath)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := io.Copy(out, resp.Body); err != nil {
			log.Fatal(err)
		}
		// close out file handles
		err = resp.Body.Close()
		if err != nil {
			log.Fatal(err)
		}
		err = out.Close()
		if err != nil {
			log.Fatal(err)
		}
		err = dl.unTarball(tempDir, tarballPath)
		if err != nil {
			log.Fatal(err)
		}
		if err = os.Remove(tarballPath); err != nil {
			log.Fatal(err)
		}
	}

}

func (dl *DockerImageDownloader) unTarball(tempDir string, tarFile string) error {
	cmd := exec.Command("tar", "-C", tempDir, "-xzf", tarFile)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
