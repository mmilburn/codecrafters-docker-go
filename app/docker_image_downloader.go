package main

import (
	"context"
	"encoding/json"
	"errors"
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

// DockerImageDownloader handles fetching and extracting Docker images
type DockerImageDownloader struct {
	client    *http.Client
	image     string
	tag       string
	token     string
	tokenExp  time.Time
	userAgent string
}

// tokenResponse represents the authentication token from Docker registry
type tokenResponse struct {
	Token       string    `json:"token"`
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	IssuedAt    time.Time `json:"issued_at"`
}

// manifestEntry represents an entry in a Docker manifest list
type manifestEntry struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Platform  struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

// manifestList represents a Docker manifest list
type manifestList struct {
	Manifests []manifestEntry `json:"manifests"`
}

// layerEntry represents a layer in a Docker image
type layerEntry struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

// layersList represents the layers in a Docker image
type layersList struct {
	Layers []layerEntry `json:"layers"`
}

// NewDockerImageDownloader creates a new Docker image downloader
func NewDockerImageDownloader(imageAndTag string) (*DockerImageDownloader, error) {
	parts := strings.SplitN(imageAndTag, ":", 2)
	if len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("invalid image format, expected image:tag or image")
	}

	image := parts[0]
	tag := "latest"
	if len(parts) > 1 && parts[1] != "" {
		tag = parts[1]
	}

	dl := &DockerImageDownloader{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		image:     image,
		tag:       tag,
		userAgent: "go-docker-client/1.0",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := dl.refreshToken(ctx); err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	return dl, nil
}

// refreshToken gets a new authentication token from Docker registry
func (dl *DockerImageDownloader) refreshToken(ctx context.Context) error {
	// Only refresh if token is expired or not set
	if dl.token != "" && time.Now().Before(dl.tokenExp) {
		return nil
	}

	url := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/%s:pull", dl.image)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", dl.userAgent)

	resp, err := dl.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed with status: %d %s", resp.StatusCode, resp.Status)
	}

	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return err
	}

	dl.token = token.Token
	// If ExpiresIn is available, set expiration time
	if token.ExpiresIn > 0 {
		dl.tokenExp = time.Now().Add(time.Duration(token.ExpiresIn-60) * time.Second)
	} else {
		// Default to 1 hour if not specified
		dl.tokenExp = time.Now().Add(1 * time.Hour)
	}

	return nil
}

// getDigests retrieves the layers of the Docker image
func (dl *DockerImageDownloader) getDigests(ctx context.Context) (layersList, error) {
	if err := dl.refreshToken(ctx); err != nil {
		return layersList{}, err
	}

	url := fmt.Sprintf("https://registry.hub.docker.com/v2/library/%s/manifests/%s", dl.image, dl.tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return layersList{}, err
	}

	req.Header.Set("Authorization", "Bearer "+dl.token)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Set("User-Agent", dl.userAgent)

	resp, err := dl.client.Do(req)
	if err != nil {
		return layersList{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return layersList{}, fmt.Errorf("failed to get manifest with status: %d %s", resp.StatusCode, resp.Status)
	}

	// Try to decode as manifest list first
	var manifests manifestList
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return layersList{}, err
	}

	if err := json.Unmarshal(bodyBytes, &manifests); err == nil && len(manifests.Manifests) > 0 {
		// Found a manifest list, look for matching platform
		for _, manifest := range manifests.Manifests {
			if manifest.Platform.OS == runtime.GOOS && manifest.Platform.Architecture == runtime.GOARCH {
				return dl.getLayers(ctx, manifest.Digest)
			}
		}
		return layersList{}, errors.New("no matching platform found in manifest list")
	}

	// If not a manifest list, try as direct layers list
	var layers layersList
	if err := json.Unmarshal(bodyBytes, &layers); err != nil {
		return layersList{}, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return layers, nil
}

// getLayers retrieves the layers of a specific manifest
func (dl *DockerImageDownloader) getLayers(ctx context.Context, digest string) (layersList, error) {
	if err := dl.refreshToken(ctx); err != nil {
		return layersList{}, err
	}

	url := fmt.Sprintf("https://registry.hub.docker.com/v2/library/%s/manifests/%s", dl.image, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return layersList{}, err
	}

	req.Header.Set("Authorization", "Bearer "+dl.token)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Set("User-Agent", dl.userAgent)

	resp, err := dl.client.Do(req)
	if err != nil {
		return layersList{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return layersList{}, fmt.Errorf("failed to get layers with status: %d %s", resp.StatusCode, resp.Status)
	}

	var list layersList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return layersList{}, err
	}

	return list, nil
}

// DownloadAndUnpackLayers downloads and extracts all layers of the Docker image
func (dl *DockerImageDownloader) DownloadAndUnpackLayers(ctx context.Context, destDir string) error {
	layers, err := dl.getDigests(ctx)
	if err != nil {
		return fmt.Errorf("failed to get image digests: %w", err)
	}

	for _, layer := range layers.Layers {
		digestNoSha := strings.Replace(layer.Digest, "sha256:", "", 1)
		tarballPath := filepath.Join(destDir, fmt.Sprintf("%s.tar.gz", digestNoSha))

		// log.Printf("Downloading layer %d/%d: %s", _+1, len(layers.Layers), digestNoSha)

		if err := dl.downloadLayer(ctx, layer, tarballPath); err != nil {
			return fmt.Errorf("failed to download layer %s: %w", digestNoSha, err)
		}

		if err := dl.extractTarball(destDir, tarballPath); err != nil {
			return fmt.Errorf("failed to extract layer %s: %w", digestNoSha, err)
		}

		if err := os.Remove(tarballPath); err != nil {
			log.Printf("Warning: failed to remove temporary tarball %s: %v", tarballPath, err)
		}
	}

	return nil
}

// downloadLayer downloads a single layer
func (dl *DockerImageDownloader) downloadLayer(ctx context.Context, layer layerEntry, tarballPath string) error {
	if err := dl.refreshToken(ctx); err != nil {
		return err
	}

	url := fmt.Sprintf("https://registry.hub.docker.com/v2/library/%s/blobs/%s", dl.image, layer.Digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+dl.token)
	req.Header.Set("Accept", layer.MediaType)
	req.Header.Set("User-Agent", dl.userAgent)

	resp, err := dl.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d %s", resp.StatusCode, resp.Status)
	}

	out, err := os.Create(tarballPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// extractTarball extracts a tarball to the destination directory
func (dl *DockerImageDownloader) extractTarball(destDir, tarballPath string) error {
	cmd := exec.Command("tar", "-C", destDir, "-xzf", tarballPath)
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
