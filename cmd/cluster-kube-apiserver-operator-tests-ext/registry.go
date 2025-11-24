package main

import (
	"testing"

	g "github.com/onsi/ginkgo/v2"

	"github.com/openshift/cluster-kube-apiserver-operator/test/e2e"
)

type goTest struct {
	Name        string
	Description string
	TestFunc    func(t testing.TB)
}

var tests = []goTest{
	{
		Name:        "TestIntegrationWithOTE",
		Description: "Description",
		TestFunc:    e2e.TestIntegrationWithOTE,
	},
}

func registerGoTestAsGinkgoTests() {
	for _, t := range tests {
		var _ = g.Describe(t.Description, func() {
			g.It(t.Name, func() {
				t.TestFunc(g.GinkgoTB())
			})
		})
	}
}
