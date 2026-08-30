package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
)

const ChunkSize = 256 * 1024

func ComputeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening file for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing file: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func SendFile(w http.ResponseWriter, path string, offset int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening file for send: %w", err)
	}
	defer f.Close()

	h := sha256.New()

	if offset > 0 {
		if _, err := io.CopyN(h, f, offset); err != nil {
			return "", fmt.Errorf("hashing prefix for resume: %w", err)
		}
	}

	mw := io.MultiWriter(h, w)
	buf := make([]byte, ChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, writeErr := mw.Write(buf[:n]); writeErr != nil {
				return "", fmt.Errorf("writing chunk: %w", writeErr)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("reading file: %w", readErr)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// StreamFile writes the file from offset without computing a hash. The caller
// must have already hashed the file (pass 1); this is pass 2, so the file is
// only read twice, never hashed twice. Returns an error if the offset exceeds
// the file size (the prefix skip hits EOF before offset bytes).
func StreamFile(w http.ResponseWriter, path string, offset int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening file for send: %w", err)
	}
	defer f.Close()

	if offset > 0 {
		if _, err := io.CopyN(io.Discard, f, offset); err != nil {
			return fmt.Errorf("skipping prefix for resume (offset %d): %w", offset, err)
		}
	}

	buf := make([]byte, ChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("writing chunk: %w", writeErr)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("reading file: %w", readErr)
		}
	}

	return nil
}

func ReceiveFile(r *http.Request, destPath string) (string, int64, error) {
	tmpPath := destPath + ".presync-tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return "", 0, fmt.Errorf("creating temp file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	mw := io.MultiWriter(h, f)

	buf := make([]byte, ChunkSize)
	var total int64
	for {
		n, readErr := r.Body.Read(buf)
		if n > 0 {
			if _, writeErr := mw.Write(buf[:n]); writeErr != nil {
				return "", 0, fmt.Errorf("writing chunk: %w", writeErr)
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, fmt.Errorf("reading request body: %w", readErr)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), total, nil
}

func AtomicRename(tmpPath, realPath string) error {
	return os.Rename(tmpPath, realPath)
}
