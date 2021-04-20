package gracefulmonitor

import (
	"fmt"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"k8s.io/klog/v2"
)

const apiChain = "OPENSHIFT-APISERVER-REWRITE"

func commonRule(targetPort, destinationPort int) []string {
	return []string{
		"-m",
		"addrtype",
		"--dst-type",
		"LOCAL",
		"-m",
		"tcp",
		"--dport",
		fmt.Sprintf("%d", targetPort),
		"-j",
		"DNAT",
		"--to-destination",
		fmt.Sprintf(":%d", destinationPort),
	}
}

func dnatRule(targetPort, destinationPort int) []string {
	rule := []string{
		"-p",
		"tcp",
	}
	return append(rule, commonRule(targetPort, destinationPort)...)
}

func establishedDNATRule(targetPort, destinationPort int) []string {
	rule := []string{
		"-p",
		"tcp",
		"-m",
		"state",
		"--state",
		"RELATED,ESTABLISHED",
	}
	return append(rule, commonRule(targetPort, destinationPort)...)
}

func ensureActiveRules(ipt *iptables.IPTables, portMap map[int]int) error {
	// Ensure chain
	exists, err := ipt.ChainExists("nat", apiChain)
	if err != nil {
		return err
	}
	if !exists {
		if err := ipt.NewChain("nat", apiChain); err != nil {
			return err
		}
	}

	// Ensure jump for traffic originating externally (PREROUTING) and
	// internally (OUTPUT).
	jumpRule := []string{"-j", apiChain}
	exists, err = ipt.Exists("nat", "PREROUTING", jumpRule...)
	if err != nil {
		return err
	}
	if !exists {
		if err := ipt.Insert("nat", "PREROUTING", 1, jumpRule...); err != nil {
			return err
		}
	}
	if err := ipt.AppendUnique("nat", "OUTPUT", jumpRule...); err != nil {
		return err
	}

	// Ensure the chain contains the desired dnat rules
	dnatRules := map[string][]string{}
	for target, destination := range portMap {
		rule := dnatRule(target, destination)
		// Index by concatinated rule to simplify lookup
		key := strings.Join(rule, " ")
		dnatRules[key] = rule
		klog.V(7).Infof("Ensuring nat rule: %s", key)
		if err := ipt.AppendUnique("nat", apiChain, rule...); err != nil {
			return err
		}
	}

	// TODO(marun) Discover transition rules
	// Search nat table
	// Find KUBE-SEP-* chains containing -j DNAT --to-destination <node ip>:6443
	// For each chain
	//   Find KUBE-SVC chain containing jump to chain -j KUBE-SEP-
	//   Find KUBE-SERVICES rule jumping to KUBE-SVC
	//     The -d address indicates service ips that need to be redirected
	nodeAddress := "192.168.126.11"
	serviceIPs := []string{
		"172.30.226.27/32", // This one - the openshift-kube-apiserver service - differs by cluster
		"172.30.0.1/32",    // This one appears to be consistently the first address
	}

	destinationPort := portMap[6443]
	for _, serviceIP := range serviceIPs {
		rule := []string{
			"-d",
			serviceIP,
			"-p",
			"tcp",
			"-m",
			"tcp",
			"--dport",
			"443",
			"-j",
			"DNAT",
			"--to-destination",
			fmt.Sprintf("%s:%d", nodeAddress, destinationPort),
		}
		key := strings.Join(rule, " ")
		dnatRules[key] = rule
		if err := ipt.AppendUnique("nat", apiChain, rule...); err != nil {
			return err
		}
	}

	return deleteUnknownRules(ipt, apiChain, dnatRules)
}

func deleteUnknownRules(ipt *iptables.IPTables, apiChain string, dnatRules map[string][]string) error {
	// Ensure the chain contains no other rules
	chainRules, err := ipt.List("nat", apiChain)
	if err != nil {
		return err
	}
	for _, rawRule := range chainRules {
		// Each rule will be prefixed with the <flag> <chain name>
		// (e.g. -A OPENSHIFT_APISERVER_REWRITE) that have to be stripped to be able to compare the rule content.
		rawRuleParts := strings.Split(rawRule, " ")

		// Rule defining the chain (-N <chain name>)
		if rawRuleParts[0] == "-N" {
			continue
		}

		// Strip the prefix
		rule := rawRuleParts[2:]

		joinedRule := strings.Join(rule, " ")
		if _, ok := dnatRules[joinedRule]; ok {
			// Known rule, do not delete
			continue
		}

		klog.V(7).Infof("Deleting nat rule: %s", joinedRule)
		if err := ipt.Delete("nat", apiChain, rule...); err != nil {
			return err
		}
	}

	return nil
}

func removeChain(ipt *iptables.IPTables) error {
	exists, err := ipt.ChainExists("nat", apiChain)
	if err != nil {
		return err
	}
	if !exists {
		// Chain and jump rules not present
		return nil
	}

	// Only attempt removal of jump rules if the api chain exists. If
	// the api chain does not exist, DeleteIfExists will return an
	// error complaining that the api chain is missing even if the
	// rule does not exist.
	for _, chain := range []string{"OUTPUT", "PREROUTING"} {
		err := ipt.DeleteIfExists("nat", chain, "-j", apiChain)
		if err != nil {
			return err
		}
	}
	err = ipt.ClearAndDeleteChain("nat", apiChain)
	if err != nil {
		return err
	}
	return nil
}
