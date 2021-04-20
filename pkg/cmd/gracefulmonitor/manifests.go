package gracefulmonitor

import (
	"fmt"
	"io/ioutil"
	"path"
	"strconv"
	"strings"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
)

type StaticPodManifest struct {
	Filename string
	Revision int
	Port     int
}

type StaticPodManifests []StaticPodManifest

func (m StaticPodManifest) Invalid() bool {
	return len(m.Filename) == 0
}

func (m StaticPodManifest) String() string {
	return fmt.Sprintf("r%d on port %d", m.Revision, m.Port)
}

// ActiveManifest returns the active manifest - the one with the
// smallest revision.
func (m StaticPodManifests) ActiveManifest() *StaticPodManifest {
	var active *StaticPodManifest
	for _, manifest := range m {
		if active == nil || manifest.Revision < active.Revision {
			active = &manifest
		}
	}
	return active
}

// ReadStaticPodManifest reads a static pod manifest from a file and
// returns its metadata - filename, revision and port.
func ReadStaticPodManifest(filename, containerName string) (*StaticPodManifest, error) {
	// Unmarshal the pod
	rawPodBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	pod, err := resourceread.ReadPodV1([]byte(rawPodBytes))
	if err != nil {
		return nil, err
	}

	// Parse the revision
	rawRevision := pod.Labels["revision"]
	revision, err := strconv.Atoi(rawRevision)
	if err != nil {
		return nil, err
	}

	// Find the port declared by the indicated container
	port := 0
	for _, container := range pod.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		if len(container.Ports) != 1 {
			template := "container %q should define only a single port"
			return nil, fmt.Errorf(template, containerName)
		}
		port = int(container.Ports[0].ContainerPort)
	}
	if port == 0 {
		return nil, fmt.Errorf("port for container %q not found", containerName)
	}

	return &StaticPodManifest{
		Filename: filename,
		Revision: revision,
		Port:     port,
	}, nil
}

func ReadAPIServerManifests(manifestDir string) (StaticPodManifests, error) {
	return ReadStaticPodManifests(manifestDir, "kube-apiserver-pod", "kube-apiserver")
}

// ReadStaticPodManifests reads the prefixed static pod manifests from
// disk and returns metadata about each one - filename, revision and
// port.
func ReadStaticPodManifests(manifestDir, podNamePrefix, containerName string) (StaticPodManifests, error) {
	files, err := ioutil.ReadDir(manifestDir)
	if err != nil {
		return nil, err
	}

	manifests := []StaticPodManifest{}
	for _, file := range files {
		// Look only for manifests of the form prefix.yaml
		if !strings.HasPrefix(file.Name(), podNamePrefix) {
			continue
		}

		filename := path.Join(manifestDir, file.Name())
		manifest, err := ReadStaticPodManifest(filename, containerName)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, *manifest)
	}
	return manifests, nil
}
