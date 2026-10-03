package insecurereadyz

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/sys/unix"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/klog/v2"
)

// readyzOpts holds values to drive the readyz proxy.
type readyzOpts struct {
	insecurePort uint16
	delegate     string
}

// NewInsecureReadyzCommand creates a insecure-readyz command.
func NewInsecureReadyzCommand() *cobra.Command {
	opts := readyzOpts{
		insecurePort: 6080,
		delegate:     "https://localhost:6443/readyz",
	}
	cmd := &cobra.Command{
		Use:   "insecure-readyz",
		Short: "Proxy the /readyz endpoint insecurely on an HTTP port",
		Run: func(cmd *cobra.Command, args []string) {
			if err := opts.Validate(); err != nil {
				klog.Fatal(err)
			}
			if err := opts.Complete(); err != nil {
				klog.Fatal(err)
			}
			if err := opts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	opts.AddFlags(cmd.Flags())

	return cmd
}

func (r *readyzOpts) AddFlags(fs *pflag.FlagSet) {
	fs.Uint16Var(&r.insecurePort, "insecure-port", r.insecurePort, "Listen on this port")
	fs.StringVar(&r.delegate, "delegate-url", r.delegate, "The URL the insecure /readyz endpoint proxies to")
}

// Validate verifies the inputs.
func (r *readyzOpts) Validate() error {
	_, err := url.Parse(r.delegate)
	if err != nil {
		return fmt.Errorf("invalid delegate-url: %v", err)
	}

	if r.insecurePort == 0 {
		return fmt.Errorf("insecure-port must be between 1 and 65535")
	}

	return nil
}

// Complete fills in missing values before command execution.
func (r *readyzOpts) Complete() error {
	return nil
}

// Run contains the logic of the insecure-readyz command.
func (r *readyzOpts) Run() error {
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		resp, err := client.Get(r.delegate)
		if err != nil {
			http.Error(w, "couldn't contact kube-apiserver", http.StatusInternalServerError)
			klog.Warningf("Failed to get %q: %v", r.delegate, err)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, "failed to read response from kube-apiserver", http.StatusInternalServerError)
			klog.Warningf("Failed to read the response body: %v", err)
			return
		}

		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	})

	shutdownCtx, cancel := context.WithCancel(context.Background())
	shutdownHandler := server.SetupSignalHandler()

	addr := fmt.Sprintf("0.0.0.0:%d", r.insecurePort)
	klog.Infof("Listening on %s", addr)

	server := &http.Server{
		Addr:        addr,
		Handler:     mux,
		BaseContext: func(_ net.Listener) context.Context { return shutdownCtx },
	}
	go func() {
		defer cancel()
		<-shutdownHandler
		klog.Infof("Received SIGTERM or SIGINT signal, shutting down server.")
		server.Shutdown(shutdownCtx)
	}()

	c := net.ListenConfig{}
	c.Control = permitAddressReuse
	ln, err := c.Listen(shutdownCtx, "tcp", addr)
	if err != nil {
		return err
	}
	err = server.Serve(ln)
	if err == http.ErrServerClosed {
		err = nil
	}
	<-shutdownCtx.Done()
	return err
}

func permitAddressReuse(network, addr string, conn syscall.RawConn) error {
	return conn.Control(func(fd uintptr) {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
			klog.Warningf("failed to set SO_REUSEADDR on socket: %v", err)
		}
	})
}
