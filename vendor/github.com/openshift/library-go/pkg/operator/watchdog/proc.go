package watchdog

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
)

// FindPidByName find the process name specified by name and return the PID of that process.
// If the process is not found, the bool is false.
// NOTE: This require container with shared process namespace (if run as side-car).
func FindPidByName(name string) (int, bool, error) {
	files, err := ioutil.ReadDir("/proc")
	if err != nil {
		return 0, false, err
	}
	// sort means we start with the directories with numbers
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		// only scan process directories (eg. /proc/1234)
		pid, err := strconv.Atoi(file.Name())
		if err != nil {
			continue
		}
		// read the /proc/123/exe symlink that points to a process
		name, err := os.Readlink(filepath.Join("/proc", file.Name(), "exe"))
		if err != nil {
			// TODO: We should report these (can be permission error?)
			continue
		}
		if path.Base(name) != name {
			continue
		}
		return pid, true, nil
	}
	return 0, false, nil
}
