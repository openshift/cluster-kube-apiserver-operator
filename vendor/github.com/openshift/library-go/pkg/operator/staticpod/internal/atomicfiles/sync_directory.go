package atomicfiles

import (
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// SyncDirectory can be used to atomically synchronize target directory with the given file content map.
// This is done by populating a temporary directory, then atomically swapping it with the target directory.
// This effectively means that any extra files in the target directory are pruned.
//
// SyncDirectory is supposed to be used with secrets/configmaps, so typeName is expected to be "configmap" or "secret".
// This does not affect the logic, but it's included in error messages.
func SyncDirectory[C string | []byte](
	typeName string, o metav1.ObjectMeta, targetDir string, files map[string]C, filePerm os.FileMode,
) error {
	return syncDirectory(&realFS, typeName, o, targetDir, files, filePerm)
}

type fileSystem struct {
	MkdirAll              func(path string, perm os.FileMode) error
	MkdirTemp             func(dir, pattern string) (string, error)
	RemoveAll             func(path string) error
	WriteFile             func(name string, data []byte, perm os.FileMode) error
	SwapDirectoriesAtomic func(dirA, dirB string) error
}

var realFS = fileSystem{
	MkdirAll:              os.MkdirAll,
	MkdirTemp:             os.MkdirTemp,
	RemoveAll:             os.RemoveAll,
	WriteFile:             os.WriteFile,
	SwapDirectoriesAtomic: SwapDirectories,
}

func syncDirectory[C string | []byte](
	fs *fileSystem, typeName string, o metav1.ObjectMeta,
	targetDir string, files map[string]C, filePerm os.FileMode,
) error {
	// We are doing to prepare a tmp directory and write all files into that directory.
	// Then we are going to atomically swap the new data directory for the old one.
	// This is currently implemented as really atomically exchanging directories.
	//
	// The same goal of atomic swap could be implemented using symlinks much like AtomicWriter does in
	// https://github.com/kubernetes/kubernetes/blob/v1.34.0/pkg/volume/util/atomic_writer.go#L58
	// The reason we don't do that is that we already have a directory populated and watched that needs to we swapped,
	// in other words, it's for compatibility reasons. And if we were to migrate to the symlink approach,
	// we would anyway need to atomically turn the current data directory to a symlink.
	// This would all just increase complexity and require atomic swap on the OS level anyway.

	// In case the target directory does not exist, create it so that the directory not existing is not a special case.
	klog.Infof("Ensuring content directory %q exists ...", targetDir)
	if err := fs.MkdirAll(targetDir, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed creating content directory for %s: %s/%s: %w", typeName, o.Namespace, o.Name, err)
	}

	// Create a tmp source directory to be swapped.
	klog.Infof("Creating temporary directory to swap for %q ...", targetDir)
	tmpDir, err := fs.MkdirTemp(filepath.Dir(targetDir), filepath.Base(targetDir)+"-*")
	if err != nil {
		return fmt.Errorf("failed creating temporary directory for %s: %s/%s: %w", typeName, o.Namespace, o.Name, err)
	}
	defer func() {
		if err := fs.RemoveAll(tmpDir); err != nil {
			klog.Errorf("Failed to remove temporary directory %q during cleanup: %v", tmpDir, err)
		}
	}()

	// Populate the tmp directory with files.
	for filename, content := range files {
		fullFilename := filepath.Join(tmpDir, filename)
		klog.Infof("Writing %s manifest %q ...", typeName, fullFilename)

		if err := fs.WriteFile(fullFilename, []byte(content), filePerm); err != nil {
			return fmt.Errorf("failed writing file for %s: %s/%s: %w", typeName, o.Namespace, o.Name, err)
		}
	}

	// Swap directories atomically.
	klog.Infof("Atomically swapping target directory %q with temporary directory %q for %s: %s/%s ...", targetDir, tmpDir, typeName, o.Namespace, o.Name)
	if err := fs.SwapDirectoriesAtomic(targetDir, tmpDir); err != nil {
		return fmt.Errorf("failed swapping target directory %q with temporary directory %q for %s: %s/%s: %w", targetDir, tmpDir, typeName, o.Namespace, o.Name, err)
	}
	return nil
}
