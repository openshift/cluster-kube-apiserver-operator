package recovery

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"
)

var (
	Scheme = runtime.NewScheme()
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
	utilruntime.Must(corev1.AddToScheme(Scheme))
}

func ReadManifestToV1Pod(manifestPath string) (*corev1.Pod, error) {
	f, err := os.OpenFile(manifestPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %q: %v", manifestPath, err)
	}
	defer f.Close()

	buf := bytes.NewBuffer(nil)
	_, err = io.Copy(buf, f)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %v", manifestPath, err)
	}

	obj, err := runtime.Decode(Codecs.UniversalDecoder(corev1.SchemeGroupVersion), buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to decode file %q: %v", manifestPath, err)
	}

	// TODO: support conversions if the object is in different but convertible version
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil, fmt.Errorf("unsupported type: kubeApiserverStaticPodManifest is not type *corev1.Pod but %T", obj)
	}

	return pod, nil
}

func GetVolumeHostPathPath(volumeName string, volumes []corev1.Volume) (string, error) {
	for _, volume := range volumes {
		if volume.Name == volumeName {
			if volume.HostPath == nil {
				return "", errors.New("volume doesn't have hostPath set")
			}
			if volume.HostPath.Path == "" {
				return "", errors.New("volume hostPath shall not be empty")
			}

			return volume.HostPath.Path, nil
		}
	}

	return "", fmt.Errorf("volume %q not found", volumeName)
}

func EnsureFileContent(filePath string, data []byte) error {
	klog.V(1).Infof("Reconciling file %q", filePath)

	exists := true
	mode := os.FileMode(600) // default mode

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			exists = false
		} else {
			return fmt.Errorf("failed to stat file %q: %v", filePath, err)
		}
	}

	if exists {
		if fileInfo.IsDir() {
			return fmt.Errorf("file %q is a directory", filePath)
		}

		fileBytes, err := ioutil.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file %q: %v", filePath, err)
		}

		if bytes.Equal(fileBytes, data) {
			klog.V(1).Infof("%q already contains the same content", filePath)
			return nil
		}

		backupFilePath := filePath + time.Now().Format(".1989-12-01-23-59-59")
		err = os.Rename(filePath, backupFilePath)
		if err != nil {
			return fmt.Errorf("failed to rename the file %q into %q: %v", filePath, backupFilePath, err)
		}

		mode = fileInfo.Mode().Perm()
	} else {
		klog.Warningf("File %q doesn't exist.", filePath)
	}

	err = ioutil.WriteFile(filePath, data, mode)
	if err != nil {
		return fmt.Errorf("failed to write content into %q: %v", filePath, err)
	}

	klog.Infof("Wrote new content to file %q", filePath)

	return nil
}
