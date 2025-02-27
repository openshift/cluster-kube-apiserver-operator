package checkapilb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/library-go/pkg/config/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	globalTimeout = 5 * time.Minute
	retryTimeout  = 5 * time.Second
)

// checkAPILBOpts holds values to drive the check-api-server command.
type checkAPILBOpts struct {
	ctx        context.Context
	KubeConfig string

	RestConfig *rest.Config
	KubeClient kubernetes.Interface
}

// NewRenderCommand creates a check-api-server command.
func NewCheckAPILBCommand(ctx context.Context) *cobra.Command {
	return newCheckAPILBCommand(ctx)
}

func newCheckAPILBCommand(ctx context.Context, testOverrides ...func(*checkAPILBOpts)) *cobra.Command {
	renderOpts := checkAPILBOpts{
		ctx: ctx,
	}
	for _, f := range testOverrides {
		f(&renderOpts)
	}
	cmd := &cobra.Command{
		Use:   "check-api-lb",
		Short: "Verify that API loadbalancer can serve API without bootstrap node",
		Run: func(cmd *cobra.Command, args []string) {
			if err := renderOpts.Validate(); err != nil {
				klog.Fatal(err)
			}
			if err := renderOpts.Complete(); err != nil {
				klog.Fatal(err)
			}
			if err := renderOpts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	renderOpts.AddFlags(cmd.Flags())

	return cmd
}

func (o *checkAPILBOpts) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.KubeConfig, "kubeconfig", o.KubeConfig, "kubeconfig file or empty")
}

// Validate verifies the inputs.
func (r *checkAPILBOpts) Validate() error {
	restConfig, err := client.GetKubeConfigOrInClusterConfig(r.KubeConfig, nil)
	if err != nil {
		klog.Fatalf("invalid kubeconfig set: %v", err)
	}
	r.RestConfig = rest.CopyConfig(restConfig)

	r.KubeClient = kubernetes.NewForConfigOrDie(restConfig)
	return nil
}

// Complete fills in missing values before command execution.
func (r *checkAPILBOpts) Complete() error {
	return nil
}

// Run contains the logic of the check-api-server command.
func (r *checkAPILBOpts) Run() error {
	pollCtx, pollCancel := context.WithTimeout(context.Background(), globalTimeout)
	defer pollCancel()

	var infra *configv1.Infrastructure

	expectedAddresses := 2
	attempt := 0
	return wait.PollImmediateUntil(time.Second, func() (done bool, err error) {
		retryCtx, retryCancel := context.WithTimeout(context.Background(), retryTimeout)
		defer retryCancel()

		attempt += 1
		klog.Infof("Attempt #%d", attempt)

		if infra == nil {
			configClient, err := configv1client.NewForConfig(r.RestConfig)
			if err != nil {
				return false, nil
			}
			infra, err = configClient.ConfigV1().Infrastructures().Get(retryCtx, "cluster", metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			if infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
				expectedAddresses = 1
			}
		}

		if err := r.checkAPIEndpoints(retryCtx, expectedAddresses); err != nil {
			klog.Infof("error checking kubernetes endpoints: %v", err)
			return false, nil
		}
		if err := r.checkLBUrls(retryCtx, infra); err != nil {
			klog.Infof("error checking apiserver via LB: %v", err)
			return false, nil
		}
		return true, nil
	}, pollCtx.Done())
}

func (r *checkAPILBOpts) checkLBUrls(ctx context.Context, infra *configv1.Infrastructure) error {
	for _, urlString := range []string{infra.Status.APIServerURL, infra.Status.APIServerInternalURL} {
		apiUrl, err := url.Parse(urlString)
		if err != nil {
			return fmt.Errorf("malformed url %s: %v", apiUrl.String(), err)
		}
		if err = r.checkUrl(ctx, apiUrl.String()); err != nil {
			return fmt.Errorf("error checking url %s: %v", apiUrl.String(), err)
		}
	}
	return nil
}

func (r *checkAPILBOpts) checkAPIEndpoints(ctx context.Context, expectedAddresses int) error {
	endpoint, err := r.KubeClient.CoreV1().Endpoints("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to list kubernetes endpoints: %v", err)
	}
	addresses := []string{}
	for i, subset := range endpoint.Subsets {
		for _, address := range subset.Addresses {
			klog.Infof("found %s address in %d subset", address.IP, i)
			addresses = append(addresses, address.IP)
		}
	}
	if len(addresses) < expectedAddresses {
		return fmt.Errorf("expected at least %d addresses, got %v", expectedAddresses, addresses)
	}
	return nil
}

func (r *checkAPILBOpts) checkUrl(ctx context.Context, url string) error {
	restConfig, err := clientcmd.BuildConfigFromFlags(url, r.KubeConfig)
	if err != nil {
		return fmt.Errorf("unable to build restConfig: %v", err)
	}
	restConfig.NegotiatedSerializer = serializer.NewCodecFactory(runtime.NewScheme())
	if err := rest.SetKubernetesDefaults(restConfig); err != nil {
		return fmt.Errorf("unable to set kube defaults: %v", err)
	}
	restClient, err := rest.UnversionedRESTClientFor(restConfig)
	if err != nil {
		return fmt.Errorf("unable to create rest client: %v", err)
	}
	var status int
	if err := restClient.Get().AbsPath("/readyz").Do(ctx).StatusCode(&status).Error(); err != nil {
		return fmt.Errorf("not yet ready: %v", err)
	}
	if status < 200 || status >= 400 {
		return fmt.Errorf("not yet ready: received http status %d", status)
	}
	klog.Infof("url %s is ready", url)
	return nil
}
