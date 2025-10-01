package securestore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/argon2"
)

var magic = []byte("MSENC1") // 6 bytes magic header

type BundleItem struct {
	SrcPath     string
	ArchivePath string // path inside the archive
}

type kdfParams struct {
	timeCost uint32
	memoryKB uint32
	threads  uint8
	salt     []byte
	nonce    []byte
}

// defaultKDF returns recommended parameters.
func defaultKDF() kdfParams {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce)
	return kdfParams{
		timeCost: 2,
		memoryKB: 64 * 1024, // 64 MiB
		threads:  4,
		salt:     salt,
		nonce:    nonce,
	}
}

func deriveKey(p kdfParams, password []byte) []byte {
	return argon2.IDKey(password, p.salt, p.timeCost, p.memoryKB, p.threads, 32)
}

// writeHeader writes magic, version, kdf params, salt, nonce to w.
func writeHeader(w io.Writer, p kdfParams) error {
	// magic [6] + version [1]
	if _, err := w.Write(magic); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint8(1)); err != nil {
		return err
	}
	// timeCost u32, memoryKB u32, threads u8
	if err := binary.Write(w, binary.LittleEndian, p.timeCost); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, p.memoryKB); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, p.threads); err != nil {
		return err
	}
	// salt len u16 + salt
	if err := binary.Write(w, binary.LittleEndian, uint16(len(p.salt))); err != nil {
		return err
	}
	if _, err := w.Write(p.salt); err != nil {
		return err
	}
	// nonce len u8 + nonce
	if err := binary.Write(w, binary.LittleEndian, uint8(len(p.nonce))); err != nil {
		return err
	}
	if _, err := w.Write(p.nonce); err != nil {
		return err
	}
	return nil
}

// readHeader parses header and returns kdf params.
func readHeader(r io.Reader) (kdfParams, error) {
	var hdr [6]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return kdfParams{}, err
	}
	if !bytes.Equal(hdr[:], magic) {
		return kdfParams{}, errors.New("invalid magic header")
	}
	var version uint8
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return kdfParams{}, err
	}
	if version != 1 {
		return kdfParams{}, fmt.Errorf("unsupported version: %d", version)
	}

	var p kdfParams
	if err := binary.Read(r, binary.LittleEndian, &p.timeCost); err != nil {
		return kdfParams{}, err
	}
	if err := binary.Read(r, binary.LittleEndian, &p.memoryKB); err != nil {
		return kdfParams{}, err
	}
	if err := binary.Read(r, binary.LittleEndian, &p.threads); err != nil {
		return kdfParams{}, err
	}
	var saltLen uint16
	if err := binary.Read(r, binary.LittleEndian, &saltLen); err != nil {
		return kdfParams{}, err
	}
	p.salt = make([]byte, saltLen)
	if _, err := io.ReadFull(r, p.salt); err != nil {
		return kdfParams{}, err
	}
	var nonceLen uint8
	if err := binary.Read(r, binary.LittleEndian, &nonceLen); err != nil {
		return kdfParams{}, err
	}
	p.nonce = make([]byte, nonceLen)
	if _, err := io.ReadFull(r, p.nonce); err != nil {
		return kdfParams{}, err
	}
	return p, nil
}

// createTarGz builds a tar.gz from items and returns bytes.
func createTarGz(items []BundleItem) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, it := range items {
		if it.SrcPath == "" || it.ArchivePath == "" {
			continue
		}
		info, err := os.Stat(it.SrcPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			root := it.SrcPath
			base := filepath.ToSlash(it.ArchivePath)
			// Walk and add files under directory
			_ = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if fi.IsDir() {
					// Optionally write a directory header for completeness
					rel, _ := filepath.Rel(root, path)
					arcDir := filepath.ToSlash(filepath.Join(base, rel))
					if arcDir == base {
						// root dir header
						hdr := &tar.Header{
							Name:    arcDir + "/",
							Mode:    int64(fi.Mode().Perm()),
							ModTime: fi.ModTime(),
						}
						_ = tw.WriteHeader(hdr)
					}
					return nil
				}
				rel, _ := filepath.Rel(root, path)
				arcName := filepath.ToSlash(filepath.Join(base, rel))
				f, e := os.Open(path)
				if e != nil {
					return nil
				}
				defer f.Close()
				hdr := &tar.Header{
					Name:    arcName,
					Size:    fi.Size(),
					Mode:    int64(fi.Mode().Perm()),
					ModTime: fi.ModTime(),
				}
				if e := tw.WriteHeader(hdr); e != nil {
					return nil
				}
				_, _ = io.Copy(tw, f)
				return nil
			})
			continue
		}
		f, err := os.Open(it.SrcPath)
		if err != nil {
			continue
		}
		hdr := &tar.Header{
			Name:    filepath.ToSlash(it.ArchivePath),
			Size:    info.Size(),
			Mode:    int64(info.Mode().Perm()),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = f.Close()
			return nil, err
		}
		if _, err := io.Copy(tw, f); err != nil {
			_ = f.Close()
			return nil, err
		}
		_ = f.Close()
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes(), nil
}

// EncryptFiles bundles items into a tar.gz and writes encrypted file at outPath using password.
func EncryptFiles(password []byte, items []BundleItem, outPath string) error {
	plaintext, err := createTarGz(items)
	if err != nil {
		return err
	}
	p := defaultKDF()
	key := deriveKey(p, password)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	sealed := gcm.Seal(nil, p.nonce, plaintext, nil)
	// write header + ciphertext
	tmp := outPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := writeHeader(f, p); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(sealed); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, outPath)
}

// DecryptToDir reads encrypted bundle at inPath and extracts into destDir.
func DecryptToDir(password []byte, inPath, destDir string) error {
	f, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer f.Close()
	p, err := readHeader(f)
	if err != nil {
		return err
	}
	key := deriveKey(p, password)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	// read rest as ciphertext
	ct, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	pt, err := gcm.Open(nil, p.nonce, ct, nil)
	if err != nil {
		return errors.New("invalid password or corrupted data")
	}
	// untar gz into destDir
	gz, err := gzip.NewReader(bytes.NewReader(pt))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		outPath := filepath.Join(destDir, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, hdr.FileInfo().Mode())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		_ = out.Close()
		// preserve modtime best-effort
		_ = os.Chtimes(outPath, time.Now(), hdr.ModTime)
	}
	return nil
}
