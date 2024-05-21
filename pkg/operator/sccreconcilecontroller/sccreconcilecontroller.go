package sccreconcilecontroller

import (
	"context"
	"time"

	securityv1client "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	securityv1informers "github.com/openshift/client-go/security/informers/externalversions/security/v1"
	securityv1listers "github.com/openshift/client-go/security/listers/security/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

type sccReconcileController struct {
	sccClient securityv1client.SecurityV1Interface
	sccLister securityv1listers.SecurityContextConstraintsLister
}

var createOnlySCCs = []string{
	"anyuid",
	"hostaccess",
	"hostmount-anyuid",
	"hostnetwork",
	"nonroot",
	"restricted",
}

// NewSCCReconcileController reconciles the "ephemeral" and "csi" volumes into
// otherwise "create-only" reconciled SCCs to make these volumes available to
// regular users in upgraded clusters.
//
// TODO:(consider): In the past we tried to reconcile the original (non-v2) SCCs with CVO
// but that backfired badly as people still used the original SCC permission model
// directly writing to SCC's "users" and "groups" fields as compared to
// the newer RBAC approach.
// Consider using this controller to reconcile all the fields BUT "users" and "groups"
// for these older SCCs.
func NewSCCReconcileController(
	sccClient securityv1client.SecurityV1Interface,
	sccInformer securityv1informers.SecurityContextConstraintsInformer,
	recorder events.Recorder,
) (factory.Controller, error) {
	c := &sccReconcileController{
		sccClient: sccClient,
		sccLister: sccInformer.Lister(),
	}

	return factory.New().
		WithSync(c.sync).
		ResyncEvery(5*time.Minute).
		WithFilteredEventsInformersQueueKeyFunc(
			factory.ObjectNameToKey,
			factory.NamesFilter(createOnlySCCs...),
			sccInformer.Informer(),
		).
		ToController("SCCReconcileController", recorder.WithComponentSuffix("scc-reconcile-controller")), nil
}

func (c *sccReconcileController) sync(ctx context.Context, controllerContext factory.SyncContext) error {
	sccName := controllerContext.QueueKey()
	scc, err := c.sccLister.Get(sccName)
	if err != nil {
		return err
	}

	volumeSet := sets.New(scc.Volumes...)
	if volumeSet.Has("ephemeral") && volumeSet.Has("csi") {
		return nil
	}

	volumeSet.Insert("ephemeral", "csi")
	sccCopy := scc.DeepCopy()
	sccCopy.Volumes = volumeSet.UnsortedList()

	_, err = c.sccClient.SecurityContextConstraints().Update(ctx, sccCopy, v1.UpdateOptions{})
	return err
}
