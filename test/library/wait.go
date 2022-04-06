package library

import (
	"context"
	"strconv"

	"github.com/openshift/library-go/pkg/operator/resource/retry"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	watch2 "k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/watch"
)

// WaitForAPIServerPodsToStabilizeOnRevision returns the current revision of the kube apiserver
// pods if all the revisions are same and the revisionConditionFunc returns true.
func WaitForAPIServerPodsToStabilizeOnRevision(ctx context.Context, client *corev1client.CoreV1Client, revisionConditionFunc func(int) bool) (int, error) {

	// how many control plane nodes?
	var controlPlaneNodeCount int
	err := retry.RetryOnConnectionErrors(ctx, func(ctx context.Context) (bool, error) {
		nodes, err := client.Nodes().List(ctx, v1.ListOptions{LabelSelector: "node-role.kubernetes.io/master"})
		if err != nil {
			return false, err
		}
		controlPlaneNodeCount = len(nodes.Items)
		return true, nil
	})

	// the revision all pods were synced to (captured by podRevisionsSynced)
	var revision int

	// pod revisions (captured by podRevisionsSynced)
	pods := map[string]string{}

	// condition func returns true when all pods are on same revision
	podRevisionsSynced := func(event watch2.Event) (bool, error) {
		switch event.Type {
		case watch2.Added, watch2.Modified:
			pod := event.Object.(*corev1.Pod)
			pods[pod.Name] = pod.Labels["revision"]
		case watch2.Deleted:
			pod := event.Object.(*corev1.Pod)
			delete(pods, pod.Name)
		}
		revisions := sets.NewString()
		for _, r := range pods {
			revisions.Insert(r)
		}
		if len(pods) == controlPlaneNodeCount && len(revisions) == 1 {
			r, _ := revisions.PopAny()
			revision, err = strconv.Atoi(r)
			if err != nil {
				return false, err
			}
			return revisionConditionFunc(revision), nil
		}
		return false, nil
	}

	lw := cache.NewFilteredListWatchFromClient(client.RESTClient(), "pods", "openshift-kube-apiserver", func(options *v1.ListOptions) { options.LabelSelector = "apiserver=true" })
	_, err = watch.UntilWithSync(ctx, lw, &corev1.Pod{}, nil, podRevisionsSynced)

	return revision, err
}

// RevisionConditionFunc returns true if the revision is the one it is interested in.
type RevisionConditionFunc func(int) bool

// AnyRevision will return true always
func AnyRevision() RevisionConditionFunc {
	return func(r int) bool {
		return true
	}
}

// LaterRevisionThan only returns true if the revision is newer that a certain revision.
func LaterRevisionThan(c int) RevisionConditionFunc {
	return func(r int) bool {
		return r > c
	}
}
