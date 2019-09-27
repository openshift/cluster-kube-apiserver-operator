package main

import (
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/openshift/cluster-kube-apiserver-operator/tls"
)

func main() {
	fmt.Printf("# TLS Documentation\n\n")
	for _, r := range tls.DocumentedResources {
		printMarkdown(os.Stdout, 1, r)
	}
}

func printMarkdown(w io.Writer, lvl int, r interface{}) {
	if v := reflect.ValueOf(r); v.Type().Kind() == reflect.Ptr {
		printMarkdown(w, lvl, v.Elem().Interface())
		return
	}

	prefix := "## "

	switch r := r.(type) {
	case tls.InputSecret:
		fmt.Fprintf(w, "%s%s/%s\n\nSecret provided by the %s.\n", prefix, r.Namespace, r.Name, r.ProvidedBy)
	case tls.InputConfigMap:
		fmt.Fprintf(w, "%s%s/%s\n\nConfigMap provided by the %s.", prefix, r.Namespace, r.Name, r.ProvidedBy)
	case tls.RotatedCertificate:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.Namespace, r.Name)
		fmt.Fprintf(w, "Rotated key, validity %v, refreshed every %v.\n", r.Validity, r.Refresh)
		fmt.Fprintf(w, "* CA-bundle %s/%s.\n", r.CABundle.Namespace, r.CABundle.Name)
		fmt.Fprintf(w, "* signer %s/%s, validity %v, refreshed every %v.\n", r.Signer.Namespace, r.Signer.Name, r.Signer.Validity, r.Signer.Refresh)
	case tls.RotatedSigner:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.RotatedCertificates[0].Signer.Namespace, r.RotatedCertificates[0].Signer.Name)
		fmt.Fprintf(w, "Rotation signer updated from key rotation controller for:\n")
		for _, rc := range r.RotatedCertificates {
			fmt.Fprintf(w, "<details><summary>%s/%s</summary><blockquote>\n\n", rc.Namespace, rc.Name)
			printMarkdown(w, lvl+1, rc)
			fmt.Fprintf(w, "</blockquote></summary></details>\n")
		}
	case tls.RotatedCABundle:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.RotatedCertificates[0].CABundle.Namespace, r.RotatedCertificates[0].CABundle.Name)
		fmt.Fprintf(w, "Rotation CA bundle updated from key rotation controller for:\n")
		for _, rc := range r.RotatedCertificates {
			fmt.Fprintf(w, "<details><summary>%s/%s</summary><blockquote>\n\n", rc.Namespace, rc.Name)
			printMarkdown(w, lvl+1, rc)
			fmt.Fprintf(w, "</blockquote></summary></details>\n")
		}
	case tls.CombinedCABundle:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.Namespace, r.Name)
		fmt.Fprintf(w, "CA bundle ConfigMap containing these CAs:")
		for _, from := range r.From {
			fmt.Fprintf(w, "<details><summary>%s/%s</summary><blockquote>\n\n", from.ToConfigMap().Namespace, from.ToConfigMap().Name)
			printMarkdown(w, lvl+1, from)
			fmt.Fprintf(w, "</blockquote></summary></details>\n")
		}
	case tls.SyncedSecret:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.Namespace, r.Name)
		fmt.Fprintf(w, "<details><summary>Copied from Secret %s/%s</summary><blockquote>\n\n", r.From.ToSecret().Namespace, r.From.ToSecret().Name)
		printMarkdown(w, lvl+1, r.From)
		fmt.Fprintf(w, "</blockquote></summary></details>\n")
	case tls.SyncedConfigMap:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.Namespace, r.Name)
		fmt.Fprintf(w, "<details><summary>Copied from ConfigMap %s/%s</summary><blockquote>\n\n", r.From.ToConfigMap().Namespace, r.From.ToConfigMap().Name)
		printMarkdown(w, lvl+1, r.From)
		fmt.Fprintf(w, "</blockquote></summary></details>\n")
	case tls.ConfigMap:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.Namespace, r.Name)
		fmt.Fprintf(w, "ConfigMap\n")
	case tls.Secret:
		fmt.Fprintf(w, "%s%s/%s\n\n", prefix, r.Namespace, r.Name)
		fmt.Fprintf(w, "Secret\n")
	default:
		fmt.Fprintf(os.Stderr, "SKIPPING unknown object: %#v\n", r)
	}

	fmt.Fprintf(w, "\n")
}
