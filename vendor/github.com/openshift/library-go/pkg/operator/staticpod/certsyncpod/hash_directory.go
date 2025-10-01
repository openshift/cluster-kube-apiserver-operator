package certsyncpod

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"io/fs"
	"path/filepath"
)

func hashDirectory(root string) ([]byte, error) {
	var b bytes.Buffer
	if err := filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// We do not hash the root directory entry itself.
		if path == root {
			return nil
		}

		b.WriteString(path)
		binary.Write(&b, binary.LittleEndian, info.ModTime().UnixNano())
		return nil
	}); err != nil {
		return nil, err
	}

	return md5.New().Sum(b.Bytes()), nil
}
