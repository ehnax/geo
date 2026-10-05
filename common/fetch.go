package common

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Say prints one line of the build log.
func Say(s string) { fmt.Println(s) }

// Sayf prints one formatted line of the build log.
func Sayf(format string, a ...any) { fmt.Printf(format+"\n", a...) }

// Human formats a byte count.
func Human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HexSum is the SHA-256 of some bytes, in hex.
func HexSum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SHA256Of is the SHA-256 of a file, in hex, or "unreadable".
func SHA256Of(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "unreadable"
	}
	return HexSum(raw)
}

// WriteSumFile writes the "<hex>  <name>" line a router checks a download against.
func WriteSumFile(path, name string, blob []byte) error {
	line := fmt.Sprintf("%s  %s\n", HexSum(blob), name)
	return os.WriteFile(path, []byte(line), 0o644)
}

// Fetch downloads a .dat into the cache, or reuses the copy already there.
func Fetch(dir, name, url string) (string, error) {
	return FetchTo(dir, name+".dat", url)
}

// FetchTo downloads url into dir/file, reusing the cache; the db-ip file name carries
// the month, so a stale month is never served.
func FetchTo(dir, file, url string) (string, error) {
	if dir == "" {
		return "", errors.New("-cache must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, file)
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		Sayf("  cached    %s (%s)", file, Human(fi.Size()))
		return path, nil
	}

	Sayf("  download  %s", url)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "geo-build/1.0")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %s", url, resp.Status)
	}

	// Write .part then rename, so an interrupted run cannot cache a truncated file.
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if n == 0 {
		os.Remove(tmp)
		return "", fmt.Errorf("GET %s: empty body", url)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	Sayf("  fetched   %s (%s)", file, Human(n))
	return path, nil
}

// ReadBlob refuses an empty file: that is a download failure, not an empty tag.
func ReadBlob(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return raw, nil
}
