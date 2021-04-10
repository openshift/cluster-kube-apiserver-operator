package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

func MirrorPodNameForNode(staticPodName, nodeName string) string {
	return staticPodName + "-" + nodeName
}

// GetStaticPod assumes a single mirror pod for each node.
func GetStaticPod(ctx context.Context, podsGetter corev1client.PodsGetter, namespace, staticPodName, nodeName string) (*corev1.Pod, error) {
	return podsGetter.Pods(namespace).Get(ctx, MirrorPodNameForNode(staticPodName, nodeName), metav1.GetOptions{})
}

// StaticPodFunc retrieves the active static pod for a node
type StaticPodFunc func(ctx context.Context, podsGetter corev1client.PodsGetter, namespace, staticPodName, nodeName string) (*corev1.Pod, error)
