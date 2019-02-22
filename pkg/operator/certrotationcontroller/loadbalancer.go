package certrotationcontroller

import (
	"fmt"
	"strings"

	"github.com/golang/glog"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

func (c *CertRotationController) syncLoadBalancerHostnames() error {
	hostnames := sets.NewString()

	infrastructureConfig, err := c.infrastructureLister.Get("cluster")
	if err != nil {
		return err
	}
	hostname := infrastructureConfig.Status.APIServerURL
	hostname = strings.Replace(hostname, "https://", "", 1)
	hostname = hostname[0:strings.LastIndex(hostname, ":")]

	hostnames.Insert(hostname)

	glog.V(2).Infof("syncing loadbalancer hostnames: %v", hostnames.List())
	c.loadBalancer.setHostnames(hostnames.List())
	return nil
}

func (c *CertRotationController) runLoadBalancerHostnames() {
	for c.processLoadBalancerHostnames() {
	}
}

func (c *CertRotationController) processLoadBalancerHostnames() bool {
	dsKey, quit := c.loadBalancerHostnamesQueue.Get()
	if quit {
		return false
	}
	defer c.loadBalancerHostnamesQueue.Done(dsKey)

	err := c.syncLoadBalancerHostnames()
	if err == nil {
		c.loadBalancerHostnamesQueue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.loadBalancerHostnamesQueue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *CertRotationController) loadBalancerHostnameEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.loadBalancerHostnamesQueue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.loadBalancerHostnamesQueue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.loadBalancerHostnamesQueue.Add(workQueueKey) },
	}
}
